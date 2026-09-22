// Package sandbox resolves a policy into a launch plan and renders it for a backend.
package sandbox

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stonewall-sh/stonewall/v2/internal/policy"
)

// Plan is a policy resolved against the host. Every path is absolute with symlinks resolved.
type Plan struct {
	Project       string
	Cwd           string
	Home          string
	Readonly      []string
	ReadonlyFiles []string // policy files outside the project: write-denied, never mounted
	HiddenDirs    []string
	HiddenFiles   []string
	ExposeRead    []string
	ExposeWrite   []string
	Bins          map[string]string // name -> resolved host path
	Shims         map[string]shim   // name -> fixed argv to exec it through (every Bins name; see shim.go)
	BinDir        string            // temp dir of symlinks named after Bins; the caller removes it
	Argv          []string          // BinDir/<agent> followed by the agent's args
	Env           []string          // host environment with PATH replaced by BinDir
	Warnings      []string          // launch-time advice for the user, e.g. an allowed script whose interpreter is not allowed
}

// Build resolves pol for the project at project, launched from cwd, running agentArgv.
// readonlyFiles are absolute host paths (the policy file and its local includes); the ones inside
// the project go to Readonly, the ones outside to ReadonlyFiles. Paths that do not exist on the
// host are skipped, except the agent binary, which must resolve.
func Build(pol policy.Policy, project, cwd string, readonlyFiles []string, agentArgv []string) (*Plan, error) {
	if len(agentArgv) == 0 {
		return nil, errors.New("no agent given")
	}
	var err error
	p := &Plan{Bins: map[string]string{}}
	if p.Project, err = realpath(project); err != nil {
		return nil, err
	}
	if p.Cwd, err = realpath(cwd); err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if p.Home, err = realpath(home); err != nil {
		return nil, err
	}
	for _, rel := range pol.Project.Readonly {
		if abs, ok := existing(filepath.Join(p.Project, rel)); ok && inside(p.Project, abs) {
			p.Readonly = append(p.Readonly, abs)
		}
	}
	for _, f := range readonlyFiles {
		abs, ok := existing(f)
		switch {
		case !ok:
		case inside(p.Project, abs):
			if !slices.Contains(p.Readonly, abs) {
				p.Readonly = append(p.Readonly, abs)
			}
		case !slices.Contains(p.ReadonlyFiles, abs):
			p.ReadonlyFiles = append(p.ReadonlyFiles, abs)
		}
	}
	for _, rel := range pol.Project.Hidden {
		abs, ok := existing(filepath.Join(p.Project, rel))
		if !ok || !inside(p.Project, abs) {
			continue
		}
		if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
			p.HiddenDirs = append(p.HiddenDirs, abs)
		} else {
			p.HiddenFiles = append(p.HiddenFiles, abs)
		}
	}
	if self, err := os.Executable(); err == nil { // stonewall itself: the agent must neither read nor run it
		if abs, ok := existing(self); ok {
			p.HiddenFiles = append(p.HiddenFiles, abs)
		}
	}
	resolve := func(e string) (string, bool) {
		if strings.HasPrefix(e, "~/") {
			e = filepath.Join(p.Home, e[2:])
		}
		return existing(e)
	}
	for _, e := range pol.Expose.Write {
		if abs, ok := resolve(e); ok {
			p.ExposeWrite = append(p.ExposeWrite, abs)
		}
	}
	for _, e := range pol.Expose.Read {
		if abs, ok := resolve(e); ok {
			p.ExposeRead = append(p.ExposeRead, abs)
		}
	}

	agent := filepath.Base(agentArgv[0])
	if !slices.Contains(pol.Bin.Allowed, agent) {
		return nil, fmt.Errorf("agent %q is not allowed: add it to bin.allowed in %s", agent, policy.FileName)
	}
	agentPath, err := exec.LookPath(agentArgv[0])
	if err != nil {
		return nil, fmt.Errorf("agent %q not found on PATH", agentArgv[0])
	}
	if p.Bins[agent], err = realpath(agentPath); err != nil {
		return nil, err
	}
	for _, name := range pol.Bin.Allowed {
		if _, taken := p.Bins[name]; taken {
			continue
		}
		path, err := exec.LookPath(name)
		if err != nil {
			continue // not installed on this host
		}
		if p.Bins[name], err = realpath(path); err != nil {
			return nil, err
		}
	}
	// Every bin is shimmed: its BinDir entry runs through a copy of stonewall itself (see shim.go)
	// instead of a direct symlink to the real file, so nothing ever depends on the kernel's own "#!"
	// handling — Landlock refuses that even when both a script and its interpreter are individually
	// exec-allowed. A plain binary shims to just itself; a script shims to its interpreter followed by
	// itself. Same mechanism, same result, on every platform.
	//
	// A script's interpreter must still be listed in bin.allowed by the user, exactly as before —
	// stonewall never adds it on its own, and allowing it always means the agent can run it directly
	// too: Landlock (and Seatbelt) grant "may execute this path" to the whole sandboxed process tree,
	// not to a specific process within it, so there is no way to permit it for the script alone.
	shims := map[string]shim{}
	for _, name := range slices.Sorted(maps.Keys(p.Bins)) {
		shims[name] = shim{Argv: []string{p.Bins[name]}}
		if interp := interpreter(p.Bins[name]); interp != "" {
			// #!/usr/bin/env X needs env itself exec-able, same as it needs X; grants nothing beyond
			// what's already allowed, since PATH and the kernel exec allow-list both still gate X.
			if _, ok := p.Bins["env"]; !ok {
				if path, err := exec.LookPath("env"); err == nil {
					if resolved, err := realpath(path); err == nil {
						p.Bins["env"] = resolved
					}
				}
			}
			if interpPath, ok := p.Bins[interp]; !ok {
				p.Warnings = append(p.Warnings, fmt.Sprintf("%s needs %s, which is not in bin.allowed — allowing it also lets the agent run it directly", name, interp))
			} else {
				shims[name] = shim{Argv: []string{interpPath, p.Bins[name]}}
			}
		} else if abs := absoluteInterpreter(p.Bins[name]); abs != "" {
			// #!/usr/bin/perl-style scripts used to run regardless of bin.allowed, since absolute exec
			// bypassed PATH restriction; now the kernel allow-list gates them too, so warn the same way.
			resolved, err := realpath(abs)
			if err != nil || !slices.Contains(slices.Collect(maps.Values(p.Bins)), resolved) {
				p.Warnings = append(p.Warnings, fmt.Sprintf("%s needs %s, which is not in bin.allowed — allowing it also lets the agent run it directly", name, abs))
			} else {
				shims[name] = shim{Argv: []string{resolved, p.Bins[name]}}
			}
		}
	}
	p.Shims = shims
	if p.BinDir, err = MakeBinDir(shims); err != nil {
		return nil, err
	}
	p.Argv = append([]string{filepath.Join(p.BinDir, agent)}, agentArgv[1:]...)
	p.Env = childEnv(os.Environ(), p.BinDir)
	return p, nil
}

// MakeBinDir creates a temp directory with one symlink per shim, every one pointing at a single
// copy of the running stonewall binary (see writeSelfShim / ExecShim in shim.go), and returns its
// resolved path.
func MakeBinDir(shims map[string]shim) (string, error) {
	dir, err := os.MkdirTemp("", "stonewall-bin-")
	if err != nil {
		return "", err
	}
	// Resolved up front, not just on return: the shim symlinks below target dir itself, and must
	// agree with the BinDir value callers (Landlock's allow-list, bwrap's mount) use.
	if dir, err = realpath(dir); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	if len(shims) > 0 {
		if err := writeSelfShim(dir, shims); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}
	for name := range shims {
		if err := os.Symlink(filepath.Join(dir, shimBinaryName), filepath.Join(dir, name)); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}

// shebangFields returns the whitespace-split fields after "#!" in a script's first line, or nil for
// binaries and scripts with no shebang.
func shebangFields(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, 256)
	n, _ := f.Read(buf)
	line, _, _ := strings.Cut(string(buf[:n]), "\n")
	if !strings.HasPrefix(line, "#!") {
		return nil
	}
	return strings.Fields(line[2:])
}

// interpreter returns the program a script's "#!/usr/bin/env X" line looks up on PATH.
// It returns "" for binaries and for scripts that name their interpreter by absolute path.
func interpreter(path string) string {
	fields := shebangFields(path)
	if len(fields) == 0 || filepath.Base(fields[0]) != "env" {
		return ""
	}
	for _, a := range fields[1:] {
		if !strings.HasPrefix(a, "-") { // skip env flags such as -S
			return a
		}
	}
	return ""
}

// absoluteInterpreter returns the interpreter path for a script naming it directly, e.g.
// "#!/usr/bin/perl" — the counterpart interpreter() doesn't cover, since that's "#!/usr/bin/env X"
// indirection only. Returns "" for binaries, env-style scripts, and scripts with no shebang.
func absoluteInterpreter(path string) string {
	fields := shebangFields(path)
	if len(fields) == 0 || filepath.Base(fields[0]) == "env" {
		return ""
	}
	return fields[0]
}

func realpath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// existing returns the resolved path and whether it exists on the host.
func existing(p string) (string, bool) {
	r, err := realpath(p)
	return r, err == nil
}

// inside reports whether path is dir or below it. Both must be resolved.
func inside(dir, path string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(os.PathSeparator))
}

// childEnv returns the host environment without SSH_AUTH_SOCK and with PATH replaced by dir.
func childEnv(env []string, dir string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") && !strings.HasPrefix(kv, "SSH_AUTH_SOCK=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+dir, stonewallShimDirEnv+"="+dir)
}
