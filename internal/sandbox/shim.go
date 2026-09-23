package sandbox

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// shimBinaryName is the copy of the running stonewall binary MakeBinDir places in BinDir, shared by
// every bin.allowed entry (see Build in plan.go).
const shimBinaryName = ".stonewall-shim"

// shimSidecarName is the JSON file next to shimBinaryName, written only when there are shims.
const shimSidecarName = ".stonewall-shim.json"

// stonewallShimDirEnv carries BinDir into the sandboxed child (set by childEnv, alongside PATH=), so
// a shim invocation can find the sidecar.
const stonewallShimDirEnv = "STONEWALL_SHIM_DIR"

// stonewallRestrictedEnv marks that this process tree already went through restrictSelfExec, so a
// nested shim call (one shim invoking another) doesn't redo it: the restriction is already inherited
// by every descendant, and reapplying it would just stack a redundant extra layer.
const stonewallRestrictedEnv = "STONEWALL_RESTRICTED"

// shim is one bin.allowed entry's fixed argv prefix; trailing args from the invoker are appended.
type shim struct {
	Argv []string `json:"argv"`
}

// ExecShim is main's first call. Invoked as "stonewall", returns immediately. Invoked as anything
// else, resolves itself via the sidecar and execs into {argv..., args...}, or fails hard — never
// falls through to the normal CLI.
func ExecShim() {
	if filepath.Base(os.Args[0]) == "stonewall" {
		return
	}
	dir := os.Getenv(stonewallShimDirEnv)
	if dir == "" {
		fmt.Fprintf(os.Stderr, "stonewall: %q is not a known shim alias\n", os.Args[0])
		os.Exit(127)
	}
	data, err := os.ReadFile(filepath.Join(dir, shimSidecarName))
	if err != nil {
		fmt.Fprintf(os.Stderr, "stonewall: shim lookup failed: %v\n", err)
		os.Exit(1)
	}
	var shims map[string]shim
	if err := json.Unmarshal(data, &shims); err != nil {
		fmt.Fprintf(os.Stderr, "stonewall: shim lookup failed: %v\n", err)
		os.Exit(1)
	}
	s, ok := shims[filepath.Base(os.Args[0])]
	if !ok || len(s.Argv) == 0 {
		fmt.Fprintf(os.Stderr, "stonewall: %q is not a known shim alias\n", os.Args[0])
		os.Exit(127)
	}
	environ := os.Environ()
	if os.Getenv(stonewallRestrictedEnv) == "" {
		if _, warning := restrictSelfExec(execTargets(dir, shims)); warning != "" {
			fmt.Fprintf(os.Stderr, "stonewall: %s\n", warning)
		}
		environ = append(environ, stonewallRestrictedEnv+"=1")
	}
	argv := append(append([]string{}, s.Argv...), os.Args[1:]...)
	err = syscall.Exec(s.Argv[0], argv, environ)
	fmt.Fprintf(os.Stderr, "stonewall: exec %s failed: %v\n", s.Argv[0], err)
	os.Exit(1)
}

// execTargets returns every path any shim might exec, plus the shim binary itself, deduplicated and
// sorted for deterministic output.
func execTargets(dir string, shims map[string]shim) []string {
	targets := map[string]string{}
	add := func(p string) { targets[p] = p }
	add(filepath.Join(dir, shimBinaryName))
	for _, s := range shims {
		for _, p := range s.Argv {
			add(p)
		}
	}
	return sortedValues(targets)
}

// writeSelfShim copies the running binary into dir once and writes the sidecar. Called by MakeBinDir
// only when shims is non-empty.
func writeSelfShim(dir string, shims map[string]shim) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = realpath(self); err != nil {
		return err
	}
	if err := copyFile(self, filepath.Join(dir, shimBinaryName), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(shims)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, shimSidecarName), data, 0o644)
}

// copyFile always copies, never hardlinks: MkdirTemp's directory and the running binary's own path
// are commonly on different filesystems (a hardlink would routinely hit EXDEV), and this runs once
// per launch on a single small static binary — not worth hardlink-with-fallback complexity.
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
