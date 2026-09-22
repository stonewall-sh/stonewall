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

// shim is one bin.allowed entry's fixed argv prefix: the real binary alone for a plain executable,
// or the interpreter followed by the script for one that needs it invoked explicitly instead of
// relying on the kernel's own "#!" substitution, which Landlock blocks even when both the script and
// its interpreter are individually exec-allowed. The invoker's own trailing args are appended after.
type shim struct {
	Argv []string `json:"argv"`
}

// ExecShim is main's very first call, before any CLI setup. Invoked as "stonewall" it returns
// immediately — the normal CLI runs. Invoked as anything else, it resolves itself via the BinDir
// sidecar and execs straight into {argv..., args...}, or fails hard; it never falls through into the
// normal CLI. Gating on the invoked name rather than an env var means stripping STONEWALL_SHIM_DIR
// from a copy's environment can't be used to reach the real CLI — only naming it "stonewall" does,
// which is the intended way to run it.
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
	argv := append(append([]string{}, s.Argv...), os.Args[1:]...)
	err = syscall.Exec(s.Argv[0], argv, os.Environ())
	fmt.Fprintf(os.Stderr, "stonewall: exec %s failed: %v\n", s.Argv[0], err)
	os.Exit(1)
}

// writeSelfShim copies the running binary into dir once and writes the sidecar mapping every shim
// alias to its argv. Called by MakeBinDir only when shims is non-empty.
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
