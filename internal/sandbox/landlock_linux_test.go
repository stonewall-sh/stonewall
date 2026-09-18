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

func testApplyLandlockChild(t *testing.T) {
	dir := t.TempDir()
	allowed := writeExecutable(t, dir, "allowed")
	denied := writeExecutable(t, dir, "denied")

	// sh must stay exec-able too: the kernel re-execs it under the hood for the "#!/bin/sh" shebang.
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}

	ok, warning := applyLandlock([]string{allowed, sh})
	if !ok {
		t.Fatalf("applyLandlock reported not applied: %s", warning)
	}
	if err := exec.Command(allowed).Run(); err != nil {
		t.Errorf("allowed path denied: %v", err)
	}
	if err := exec.Command(denied).Run(); err == nil {
		t.Error("denied path was allowed to exec")
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

// TestRestrictExecSkipsSetuidBwrap covers the setuid-bwrap exception without touching real Landlock
// state: restrictExec must return before ever locking the OS thread or calling applyLandlock.
func TestRestrictExecSkipsSetuidBwrap(t *testing.T) {
	dir := t.TempDir()
	fake := writeExecutable(t, dir, "fake-bwrap")
	if err := os.Chmod(fake, 0o755|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}

	locked, warning := restrictExec(map[string]string{"sh": "/bin/sh"}, fake)
	if locked {
		t.Error("restrictExec locked the thread for a setuid bwrap")
	}
	if warning == "" {
		t.Error("restrictExec returned no warning for a setuid bwrap")
	}
}
