//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// TestApplyLandlock is a real syscall-level self-check. It must run in a subprocess: landlock_restrict_self
// is irreversible for the calling process, and would otherwise poison the rest of this test binary's
// ability to exec anything not in the allow-list.
func TestApplyLandlock(t *testing.T) {
	if os.Getenv("STONEWALL_LANDLOCK_TEST_CHILD") == "1" {
		testApplyLandlockChild(t)
		return
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestApplyLandlock$", "-test.v")
	cmd.Env = append(os.Environ(), "STONEWALL_LANDLOCK_TEST_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, out)
	}
}

// testApplyLandlockChild checks applyLandlock's actual guarantee: exec of a plain ELF binary that
// was explicitly allowed succeeds, exec of one that wasn't fails. It deliberately does not test
// direct exec of an allowed shebang script — Landlock refuses the kernel's own "#!" substitution
// even when both the script and its interpreter are individually allowed (confirmed against real
// Linux Landlock sandboxing tooling), so production code never relies on that path; see shim.go,
// which invokes the interpreter explicitly instead.
func testApplyLandlockChild(t *testing.T) {
	dir := t.TempDir()
	denied := writeExecutable(t, dir, "denied")

	trueBin, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}

	ok, warning := applyLandlock([]string{trueBin})
	if !ok {
		t.Fatalf("applyLandlock reported not applied: %s", warning)
	}
	if err := exec.Command(trueBin).Run(); err != nil {
		t.Errorf("allowed plain ELF denied: %v", err)
	}
	if err := exec.Command(denied).Run(); err == nil {
		t.Error("denied path was allowed to exec")
	}
}

// TestRestrictSelfExec is the same self-check as TestApplyLandlock, but through restrictSelfExec —
// the actual entry point ExecShim uses — to confirm its LockOSThread handling doesn't break the
// guarantee: the exec right after must land on the same restricted thread.
func TestRestrictSelfExec(t *testing.T) {
	if os.Getenv("STONEWALL_LANDLOCK_TEST_CHILD") == "1" {
		testRestrictSelfExecChild(t)
		return
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestRestrictSelfExec$", "-test.v")
	cmd.Env = append(os.Environ(), "STONEWALL_LANDLOCK_TEST_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child failed: %v\n%s", err, out)
	}
}

func testRestrictSelfExecChild(t *testing.T) {
	dir := t.TempDir()
	denied := writeExecutable(t, dir, "denied")

	trueBin, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}

	locked, warning := restrictSelfExec([]string{trueBin})
	if !locked {
		t.Fatalf("restrictSelfExec reported not applied: %s", warning)
	}
	if err := exec.Command(trueBin).Run(); err != nil {
		t.Errorf("allowed plain ELF denied: %v", err)
	}
	if err := exec.Command(denied).Run(); err == nil {
		t.Error("denied path was allowed to exec")
	}
}

// TestDynamicLoader checks the fix for a real CI failure: applyLandlock allowed /usr/bin/true but
// exec of it still got denied, because a dynamically linked binary's own ELF interpreter (ld.so)
// also needs to be exec-allowed, and nothing was granting that. /bin/sh is dynamically linked on
// every mainstream Linux distro (including this test's CI runners), so it should report one.
func TestDynamicLoader(t *testing.T) {
	if dynamicLoader("/bin/sh") == "" {
		t.Error("dynamicLoader(/bin/sh) = \"\", want a real ELF interpreter path")
	}
	if got := dynamicLoader("/nonexistent-xyz"); got != "" {
		t.Errorf("dynamicLoader(missing) = %q, want \"\"", got)
	}
	dir := t.TempDir()
	notELF := writeExecutable(t, dir, "not-elf") // a "#!/bin/sh" shebang script, not an ELF file
	if got := dynamicLoader(notELF); got != "" {
		t.Errorf("dynamicLoader(non-ELF) = %q, want \"\"", got)
	}
}

func writeExecutable(t *testing.T, dir, name string) string {
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestApplyLandlockFallback covers the old-kernel/unsupported path, which can't be exercised on CI's
// recent-kernel runners: inject an ENOSYS response and confirm applyLandlock reports it as non-fatal.
func TestApplyLandlockFallback(t *testing.T) {
	orig := landlockCreateRuleset
	landlockCreateRuleset = func(attr *unix.LandlockRulesetAttr, size, flags uintptr) (int, unix.Errno) {
		return -1, unix.ENOSYS
	}
	t.Cleanup(func() { landlockCreateRuleset = orig })

	ok, warning := applyLandlock([]string{"/bin/sh"})
	if ok {
		t.Error("applyLandlock reported ok despite an injected ENOSYS")
	}
	if warning == "" {
		t.Error("applyLandlock returned no warning despite an injected ENOSYS")
	}
}
