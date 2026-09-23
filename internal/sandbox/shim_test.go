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
	if want := (shim{Argv: []string{interp, script}}); !slices.Equal(got["envscript"].Argv, want.Argv) {
		t.Fatalf("sidecar = %+v, want %+v", got["envscript"], want)
	}
	targets := execTargets(binDir, got)
	for _, want := range []string{interp, script, filepath.Join(binDir, shimBinaryName)} {
		if !slices.Contains(targets, want) {
			t.Errorf("execTargets = %v, missing %q", targets, want)
		}
	}

	cmd := exec.Command(alias)
	cmd.Env = append(os.Environ(), stonewallShimDirEnv+"="+binDir)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "shimmed-ok") {
		t.Fatalf("shim exec: err=%v out=%q", err, out)
	}
}

// TestMakeBinDirPlainShim confirms a plain binary (no interpreter involved) works the same way: its
// argv is just itself, and running its alias forwards trailing args to the real binary. Uses a real
// ELF (echo), not a shebang script: that's exactly the case restrictSelfExec (real Landlock) refuses
// to exec directly — a shebang fixture here would pass on this session's non-Landlock test rig but
// fail for real on Linux CI, so it would prove nothing about the case this test exists to cover.
func TestMakeBinDirPlainShim(t *testing.T) {
	real, err := exec.LookPath("echo")
	if err != nil {
		t.Skip("no echo on PATH")
	}
	if real, err = realpath(real); err != nil {
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

	cmd := exec.Command(alias, "plain-ok")
	cmd.Env = append(os.Environ(), stonewallShimDirEnv+"="+binDir)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "plain-ok") {
		t.Fatalf("shim exec: err=%v out=%q", err, out)
	}
}

// TestExecShimRestrictsOnce confirms the fix for the layering finding: a nested shim call (one shim
// invoking another, the STONEWALL_RESTRICTED marker already set) must not attempt restrictSelfExec
// again — real Landlock stacks a ruleset layer per attempt, capped at 16 by the kernel, so redoing it
// on every hop of a deep tool chain can silently exhaust that cap. Observed here via the env each
// exec'd process actually sees, platform-independently (restrictSelfExec itself is a no-op off Linux).
func TestExecShimRestrictsOnce(t *testing.T) {
	envBin, err := exec.LookPath("env")
	if err != nil {
		t.Skip("no env on PATH")
	}
	if envBin, err = realpath(envBin); err != nil {
		t.Fatal(err)
	}

	shims := map[string]shim{"tool": {Argv: []string{envBin}}}
	binDir, err := MakeBinDir(shims)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(binDir)
	alias := filepath.Join(binDir, "tool")

	for _, tt := range []struct {
		name       string
		presetMark string
	}{
		{"unset before top-level call", ""},
		{"already set before nested call", "1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(alias)
			cmd.Env = append(os.Environ(), stonewallShimDirEnv+"="+binDir)
			if tt.presetMark != "" {
				cmd.Env = append(cmd.Env, stonewallRestrictedEnv+"="+tt.presetMark)
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("shim exec: %v\n%s", err, out)
			}
			marker := stonewallRestrictedEnv + "="
			if n := strings.Count(string(out), marker); n != 1 {
				t.Fatalf("%s appears %d times in child env, want exactly 1:\n%s", marker, n, out)
			}
			if tt.presetMark != "" && !strings.Contains(string(out), marker+tt.presetMark) {
				t.Fatalf("preset value %q was not preserved:\n%s", tt.presetMark, out)
			}
		})
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
