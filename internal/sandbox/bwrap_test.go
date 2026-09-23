package sandbox

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func testPlan() *Plan {
	return &Plan{
		Project:       "/home/u/proj",
		Cwd:           "/home/u/proj/src",
		Home:          "/home/u",
		Readonly:      []string{"/home/u/proj/.git", "/home/u/proj/.stonewall.yml"},
		ReadonlyFiles: []string{"/etc/stonewall/ci.yml"},
		HiddenDirs:    []string{"/home/u/proj/secrets"},
		HiddenFiles:   []string{"/home/u/proj/.env"},
		ExposeWrite:   []string{"/home/u/.claude"},
		ExposeRead:    []string{"/home/u/.gitconfig"},
		Bins:          map[string]string{"claude": "/home/u/.local/share/claude/claude", "sh": "/usr/bin/sh", "git": "/usr/bin/git", "node": "/home/u/.nvm/node"},
		Shims: map[string]shim{
			"claude": {Argv: []string{"/home/u/.local/share/claude/claude"}},
			"sh":     {Argv: []string{"/usr/bin/sh"}},
			"git":    {Argv: []string{"/usr/bin/git"}},
			"node":   {Argv: []string{"/home/u/.nvm/node"}},
		},
		BinDir: "/tmp/stonewall-bin-1",
		Argv:   []string{"/tmp/stonewall-bin-1/claude", "--resume"},
	}
}

func TestBwrapArgs(t *testing.T) {
	readlink = func(p string) (string, error) {
		if p == "/bin" {
			return "usr/bin", nil
		}
		return "", errors.New("not a symlink")
	}
	t.Cleanup(func() { readlink = os.Readlink })

	got := strings.Join(BwrapArgs(testPlan()), " ")
	want := strings.Join([]string{
		"--unshare-pid --die-with-parent --proc /proc --dev /dev --tmpfs /tmp",
		"--ro-bind-try /usr /usr --ro-bind-try /etc /etc --symlink usr/bin /bin --ro-bind-try /sbin /sbin",
		"--ro-bind-try /lib /lib --ro-bind-try /lib32 /lib32 --ro-bind-try /lib64 /lib64 --ro-bind-try /opt /opt",
		"--ro-bind-try /run/systemd/resolve /run/systemd/resolve --ro-bind-try /run/resolvconf /run/resolvconf --ro-bind-try /run/NetworkManager /run/NetworkManager",
		"--tmpfs /home/u",
		"--bind /home/u/proj /home/u/proj",
		"--bind /home/u/.claude /home/u/.claude --ro-bind /home/u/.gitconfig /home/u/.gitconfig",
		"--ro-bind /home/u/proj/.git /home/u/proj/.git --ro-bind /home/u/proj/.stonewall.yml /home/u/proj/.stonewall.yml",
		"--tmpfs /home/u/proj/secrets",
		"--ro-bind /dev/null /home/u/proj/.env",
		"--ro-bind /home/u/.local/share/claude/claude /home/u/.local/share/claude/claude --ro-bind /home/u/.nvm/node /home/u/.nvm/node",
		"--ro-bind /tmp/stonewall-bin-1 /tmp/stonewall-bin-1",
		"--setenv PATH /tmp/stonewall-bin-1 --chdir /home/u/proj/src --",
		"/tmp/stonewall-bin-1/claude --resume",
	}, " ")
	if got != want {
		t.Fatalf("\ngot:  %s\nwant: %s", got, want)
	}
}
