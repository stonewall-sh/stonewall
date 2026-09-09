package sandbox

import "testing"

func TestSeatbeltProfile(t *testing.T) {
	got := SeatbeltProfile(testPlan())
	want := `(version 1)
(allow default)
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
(allow file-read* (subpath "/home/u/.nvm/node"))
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
