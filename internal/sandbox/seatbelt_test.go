package sandbox

import (
	"errors"
	"strings"
	"testing"
)

func TestSeatbeltProfile(t *testing.T) {
	got := SeatbeltProfile(testPlan())
	want := `(version 1)
(allow default)
(deny process-exec*)
(deny file-read* file-write* (subpath "/home/u"))
(allow file-read-metadata (literal "/home/u"))
(allow file-read* file-write* (subpath "/home/u/proj"))
(allow file-read* file-write* (subpath "/home/u/.claude"))
(allow file-read* (subpath "/home/u/.gitconfig"))
(deny file-write* (subpath "/home/u/.gitconfig"))
(deny file-write* (subpath "/home/u/proj/.git"))
(deny file-write* (subpath "/home/u/proj/.stonewall.yml"))
(deny file-write* (subpath "/etc/stonewall/ci.yml"))
(deny file-read* file-write* process-exec* (subpath "/home/u/proj/secrets"))
(deny file-read* file-write* process-exec* (subpath "/home/u/proj/.env"))
(allow file-read* (subpath "/home/u/.local/share/claude/claude"))
(allow process-exec (literal "/home/u/.local/share/claude/claude"))
(allow file-read* (subpath "/home/u/.nvm/node"))
(allow process-exec (literal "/home/u/.nvm/node"))
(allow process-exec (literal "/usr/bin/git"))
(allow process-exec (literal "/usr/bin/sh"))
(allow file-read* (subpath "/tmp/stonewall-bin-1"))
`
	if got != want {
		t.Fatalf("\ngot:\n%s\nwant:\n%s", got, want)
	}
	args := SeatbeltArgs(testPlan())
	if args[0] != "-p" || args[1] != got || args[2] != "/tmp/stonewall-bin-1/claude" || args[3] != "--resume" {
		t.Fatalf("args: %q", args)
	}
	if q := sbpl(`/a "b"\c`); q != `"/a \"b\"\\c"` {
		t.Fatalf("sbpl: %s", q)
	}
}

// TestSeatbeltProfileDeniesUnlistedExec proves the core property this feature exists for: exec is
// denied by default, and only bins actually in Plan.Bins get an allow rule.
func TestSeatbeltProfileDeniesUnlistedExec(t *testing.T) {
	got := SeatbeltProfile(testPlan())
	if !strings.Contains(got, "(deny process-exec*)") {
		t.Fatal("profile does not deny exec by default")
	}
	if strings.Contains(got, `(allow process-exec (literal "/usr/bin/curl"))`) {
		t.Fatal("unlisted binary got an exec allow rule")
	}
}

// TestSeatbeltProfileShellVariant guards a real regression: /bin/sh isn't a symlink, so allowing it
// alone isn't enough — the OS re-execs it to whatever /var/select/sh points at. evalSymlinks is faked
// to a distinctive path here to prove the resolution is genuinely dynamic, not hardcoded.
func TestSeatbeltProfileShellVariant(t *testing.T) {
	orig := evalSymlinks
	evalSymlinks = func(path string) (string, error) {
		if path == "/var/select/sh" {
			return "/opt/homebrew/bin/dash", nil
		}
		return orig(path)
	}
	t.Cleanup(func() { evalSymlinks = orig })

	p := testPlan()
	p.Bins["sh"] = "/bin/sh"
	got := SeatbeltProfile(p)
	if !strings.Contains(got, `(allow process-exec (literal "/bin/sh"))`) {
		t.Fatal("missing allow for /bin/sh itself")
	}
	if !strings.Contains(got, `(allow process-exec (literal "/opt/homebrew/bin/dash"))`) {
		t.Fatal("missing allow for the resolved /var/select/sh target")
	}
	if strings.Contains(got, `(allow process-exec (literal "/bin/bash"))`) {
		t.Fatal("fell back to the hardcoded default despite a resolvable /var/select/sh")
	}
}

// TestSeatbeltProfileShellVariantFallback covers hosts with no /var/select/sh (pre-Catalina): the
// resolution must fall back to /bin/bash rather than leaving /bin/sh's real target unallowed.
func TestSeatbeltProfileShellVariantFallback(t *testing.T) {
	orig := evalSymlinks
	evalSymlinks = func(path string) (string, error) { return "", errors.New("no such file") }
	t.Cleanup(func() { evalSymlinks = orig })

	p := testPlan()
	p.Bins["sh"] = "/bin/sh"
	got := SeatbeltProfile(p)
	if !strings.Contains(got, `(allow process-exec (literal "/bin/bash"))`) {
		t.Fatal("missing fallback allow for /bin/bash when /var/select/sh doesn't resolve")
	}
}
