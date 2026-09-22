package sandbox

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestMain gives a subprocess spawned from this test binary the same alias-dispatch behavior as real
// main(): when it's invoked with STONEWALL_SHIM_DIR set — the way the tests below start one — ExecShim
// replaces it before the normal test machinery ever runs. The test binary itself is never named
// "stonewall", so calling ExecShim unconditionally here would wrongly treat the top-level test run
// as an unknown alias; only do it when a subprocess actually set up that scenario.
func TestMain(m *testing.M) {
	if os.Getenv(stonewallShimDirEnv) != "" {
		ExecShim()
	}
	os.Exit(m.Run())
}

// TestMakeBinDirScriptShim is the end-to-end regression test for the original bug: a bin.allowed
// entry that's a script gets routed through its interpreter explicitly instead of relying on the
// kernel's own "#!" handling. It doesn't need real Landlock active — this is a general exec-plumbing
// fix that also happens to be what makes the Landlock-specific case work, so it runs on every
// platform.
func TestMakeBinDirScriptShim(t *testing.T) {
	dir := t.TempDir()
	interp, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH")
	}
	if interp, err = realpath(interp); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho shimmed-ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	shims := map[string]shim{"envscript": {Argv: []string{interp, script}}}
	binDir, err := MakeBinDir(shims)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(binDir)

	alias := filepath.Join(binDir, "envscript")
	target, err := os.Readlink(alias)
	if err != nil || target != filepath.Join(binDir, shimBinaryName) {
		t.Fatalf("envscript symlink target = %q, err %v, want the self-shim", target, err)
	}

	data, err := os.ReadFile(filepath.Join(binDir, shimSidecarName))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]shim
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if want := (shim{Argv: []string{interp, script}}); got["envscript"].Argv == nil || !slices.Equal(got["envscript"].Argv, want.Argv) {
		t.Fatalf("sidecar = %+v, want %+v", got["envscript"], want)
	}

	cmd := exec.Command(alias)
	cmd.Env = append(os.Environ(), stonewallShimDirEnv+"="+binDir)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "shimmed-ok") {
		t.Fatalf("shim exec: err=%v out=%q", err, out)
	}
}

// TestMakeBinDirPlainShim confirms a plain binary (no interpreter involved) works the same way: its
// argv is just itself, and running its alias produces the real binary's own output.
func TestMakeBinDirPlainShim(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real-tool")
	if err := os.WriteFile(real, []byte("#!/bin/sh\necho plain-ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := realpath(real)
	if err != nil {
		t.Fatal(err)
	}

	shims := map[string]shim{"tool": {Argv: []string{real}}}
	binDir, err := MakeBinDir(shims)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(binDir)

	alias := filepath.Join(binDir, "tool")
	if target, err := os.Readlink(alias); err != nil || target != filepath.Join(binDir, shimBinaryName) {
		t.Fatalf("tool symlink target = %q, err %v, want the self-shim", target, err)
	}

	cmd := exec.Command(alias)
	cmd.Env = append(os.Environ(), stonewallShimDirEnv+"="+binDir)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "plain-ok") {
		t.Fatalf("shim exec: err=%v out=%q", err, out)
	}
}

// TestMakeBinDirEmpty confirms an empty plan (no bins at all) writes no self-copy or sidecar.
func TestMakeBinDirEmpty(t *testing.T) {
	binDir, err := MakeBinDir(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(binDir)

	if _, err := os.Stat(filepath.Join(binDir, shimBinaryName)); !os.IsNotExist(err) {
		t.Errorf("self-shim written despite no shims: err=%v", err)
	}
}
