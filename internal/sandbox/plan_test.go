package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stonewall-sh/stonewall/v2/internal/policy"
)

func TestBuild(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	proj := filepath.Join(tmp, "proj")
	outside := filepath.Join(tmp, "outside")
	for _, d := range []string{filepath.Join(home, "exposed"), filepath.Join(home, "exposed-ro"), filepath.Join(proj, ".git"), filepath.Join(proj, "secrets"), filepath.Join(proj, "src"), filepath.Join(outside, "secret")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(proj, ".env"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(proj, policy.FileName), []byte("project:\n  readonly: [.git]"), 0o644)
	if err := os.Symlink(filepath.Join(tmp, "outside"), filepath.Join(proj, "link")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "/nowhere")

	pol := policy.Policy{
		Project: policy.Project{
			Readonly: []string{".git", "missing", "../outside", "link"},
			Hidden:   []string{".env", "secrets", "nope", "link"},
		},
		Bin:    policy.Bin{Allowed: []string{"sh", "definitely-not-a-binary"}},
		Expose: policy.Expose{Write: []string{"~/exposed", "~/absent"}, Read: []string{"~/exposed-ro"}},
	}
	// A file outside the project in readonlyFiles is write-denied through ReadonlyFiles; the policy file
	// and a local include are kept in Readonly.
	os.WriteFile(filepath.Join(proj, "extra.yml"), []byte("bin:\n  allowed: [sh]"), 0o644)
	os.WriteFile(filepath.Join(outside, "outside.yml"), []byte("bin:\n  allowed: [sh]"), 0o644)
	roFiles := []string{filepath.Join(proj, policy.FileName), filepath.Join(proj, "extra.yml"), filepath.Join(outside, "outside.yml"), filepath.Join(proj, ".git")}
	p, err := Build(pol, proj, filepath.Join(proj, "src"), roFiles, []string{"sh", "-c", "true"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(p.BinDir)

	real := func(s string) string { r, _ := filepath.EvalSymlinks(s); return r }
	eq := func(name string, got, want []string) {
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: got %v want %v", name, got, want)
		}
	}
	eq("Readonly", p.Readonly, []string{real(filepath.Join(proj, ".git")), real(filepath.Join(proj, policy.FileName)), real(filepath.Join(proj, "extra.yml"))})
	outsideYml := real(filepath.Join(outside, "outside.yml"))
	if slices.Contains(p.Readonly, outsideYml) {
		t.Error("readonly file outside the project mounted into Readonly")
	}
	eq("ReadonlyFiles", p.ReadonlyFiles, []string{outsideYml})
	eq("HiddenDirs", p.HiddenDirs, []string{real(filepath.Join(proj, "secrets"))})
	self, _ := os.Executable()
	self, _ = realpath(self)
	eq("HiddenFiles", p.HiddenFiles, []string{real(filepath.Join(proj, ".env")), self})
	eq("ExposeWrite", p.ExposeWrite, []string{real(filepath.Join(home, "exposed"))})
	eq("ExposeRead", p.ExposeRead, []string{real(filepath.Join(home, "exposed-ro"))})
	// Verify escaping symlinks are skipped
	if slices.Contains(p.Readonly, real(outside)) || slices.Contains(p.Readonly, real(filepath.Join(proj, "link"))) {
		t.Error("escape symlink in readonly")
	}
	if slices.Contains(p.HiddenDirs, real(outside)) || slices.Contains(p.HiddenDirs, real(filepath.Join(proj, "link"))) {
		t.Error("escape symlink in hiddendirs")
	}
	if slices.Contains(p.HiddenFiles, real(outside)) || slices.Contains(p.HiddenFiles, real(filepath.Join(proj, "link"))) {
		t.Error("escape symlink in hiddenfiles")
	}
	if p.Project != real(proj) || p.Cwd != real(filepath.Join(proj, "src")) || p.Home != real(home) {
		t.Errorf("project/cwd/home: %s %s %s", p.Project, p.Cwd, p.Home)
	}
	if _, ok := p.Bins["definitely-not-a-binary"]; ok {
		t.Error("unresolvable bin kept")
	}
	if p.Argv[0] != filepath.Join(p.BinDir, "sh") || p.Argv[1] != "-c" || p.Argv[2] != "true" {
		t.Errorf("argv: %v", p.Argv)
	}
	if target, err := os.Readlink(p.Argv[0]); err != nil || target != p.Bins["sh"] {
		t.Errorf("symlink target %q, err %v, want %q", target, err, p.Bins["sh"])
	}
	if !slices.Contains(p.Env, "PATH="+p.BinDir) {
		t.Errorf("PATH not set to bin dir: %v", p.Env)
	}
	for _, kv := range p.Env {
		if strings.HasPrefix(kv, "SSH_AUTH_SOCK=") {
			t.Errorf("SSH_AUTH_SOCK leaked into Env: %v", p.Env)
		}
	}
	if _, err := Build(pol, proj, proj, nil, []string{"no-such-agent-xyz"}); err == nil {
		t.Error("missing agent accepted")
	}
	if _, err := Build(pol, proj, proj, nil, []string{"cat"}); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("unlisted agent accepted: %v", err)
	}
}

func TestInterpreter(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"env", "#!/usr/bin/env perl\nprint 1;\n", "perl"},
		{"envflags", "#!/usr/bin/env -S node --x\n", "node"},
		{"absolute", "#!/usr/bin/perl\nprint 1;\n", ""},
		{"binary", "\x7fELF\x02\x01\x01\x00binarydata", ""},
	}
	for _, c := range cases {
		if got := interpreter(write(c.name, c.content)); got != c.want {
			t.Errorf("interpreter(%s): got %q want %q", c.name, got, c.want)
		}
	}
}

func TestBuildWarnings(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	dir := t.TempDir()
	scripts := map[string]string{
		"envscript": "#!/usr/bin/env perl\nprint 1;\n",
		"envflags":  "#!/usr/bin/env -S python3 -u\n",
		"direct":    "#!/bin/sh\ntrue\n",
	}
	for name, content := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	pol := policy.Policy{Bin: policy.Bin{Allowed: []string{"sh", "envscript", "envflags", "direct"}}}
	p, err := Build(pol, tmp, tmp, nil, []string{"sh"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(p.BinDir)
	want := []string{"envflags needs python3, which is not in bin.allowed", "envscript needs perl, which is not in bin.allowed"}
	if strings.Join(p.Warnings, ",") != strings.Join(want, ",") {
		t.Errorf("Warnings: got %v want %v", p.Warnings, want)
	}

	pol2 := policy.Policy{Bin: policy.Bin{Allowed: []string{"sh", "envscript", "envflags", "direct", "perl", "python3"}}}
	p2, err := Build(pol2, tmp, tmp, nil, []string{"sh"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(p2.BinDir)
	if len(p2.Warnings) != 0 {
		t.Errorf("Warnings with perl/python3 allowed: got %v want none", p2.Warnings)
	}
}
