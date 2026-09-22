//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// landlockUnavailable is returned whenever Landlock can't be applied, for any reason: old kernel,
// disabled LSM, or a setup failure. It's always non-fatal — the caller falls back to PATH-only.
const landlockUnavailable = "Landlock unavailable on this kernel (needs Linux 5.13+); exec is only PATH-restricted, not kernel-enforced"

// landlockCreateRuleset wraps the landlock_create_ruleset(2) syscall. A nil attr with size 0 probes
// the supported ABI version instead of creating a ruleset. Swappable so a test can inject an
// unsupported-kernel response without a real old kernel.
var landlockCreateRuleset = func(attr *unix.LandlockRulesetAttr, size, flags uintptr) (fd int, errno unix.Errno) {
	r1, _, e := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(attr)), size, flags)
	return int(r1), e
}

// landlock_restrict_self is per-thread, not per-process, and Go can move a goroutine to a different
// OS thread at any syscall. So the fork right after this needs to happen on the same locked thread.
func restrictExec(bins map[string]string, bwrapPath string, extra ...string) (locked bool, warning string) {
	if fi, err := os.Stat(bwrapPath); err == nil && fi.Mode()&os.ModeSetuid != 0 {
		// no_new_privs is inherited across exec and would break bwrap's setuid escalation.
		return false, "bwrap is setuid on this system, skipping Landlock so its privilege escalation still works; exec is only PATH-restricted, not kernel-enforced"
	}

	paths := make([]string, 0, len(bins)+1+len(extra))
	for _, p := range bins {
		paths = append(paths, p)
	}
	paths = append(paths, bwrapPath)
	paths = append(paths, extra...)

	runtime.LockOSThread()
	ok, warn := applyLandlock(paths)
	if !ok {
		runtime.UnlockOSThread()
		return false, warn
	}
	return true, ""
}

// applyLandlock restricts the calling process, and everything it fork+execs from here on for its
// entire life, to executing only the given paths. The restriction is irreversible and inherited
// across fork and exec. ok reports whether it was applied; when false the caller should continue
// PATH-only rather than fail the launch.
func applyLandlock(paths []string) (ok bool, warning string) {
	if _, errno := landlockCreateRuleset(nil, 0, unix.LANDLOCK_CREATE_RULESET_VERSION); errno != 0 {
		return false, landlockUnavailable
	}

	attr := unix.LandlockRulesetAttr{Access_fs: unix.LANDLOCK_ACCESS_FS_EXECUTE}
	rulesetFD, errno := landlockCreateRuleset(&attr, unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return false, landlockUnavailable
	}
	defer unix.Close(rulesetFD)

	for _, path := range paths {
		fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			return false, fmt.Sprintf("Landlock setup failed opening %s (%v); exec is only PATH-restricted, not kernel-enforced", path, err)
		}
		beneath := unix.LandlockPathBeneathAttr{Allowed_access: unix.LANDLOCK_ACCESS_FS_EXECUTE, Parent_fd: int32(fd)}
		_, _, addErrno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(rulesetFD), unix.LANDLOCK_RULE_PATH_BENEATH,
			uintptr(unsafe.Pointer(&beneath)), 0, 0, 0)
		unix.Close(fd)
		if addErrno != 0 {
			return false, fmt.Sprintf("Landlock setup failed for %s (%v); exec is only PATH-restricted, not kernel-enforced", path, addErrno)
		}
	}

	// Required by landlock_restrict_self(2) for an unprivileged caller; set only after every rule
	// succeeded, so a failed setup above never leaves this (irreversible) bit set for nothing.
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return false, landlockUnavailable
	}
	if _, _, restrictErrno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(rulesetFD), 0, 0); restrictErrno != 0 {
		return false, landlockUnavailable
	}
	return true, ""
}
