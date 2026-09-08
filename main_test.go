package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stonewall-sh/stonewall/v2/internal/policy"
)

func TestRootCmd(t *testing.T) {
	t.Run("no args prints help", func(t *testing.T) {
		cmd := newRootCmd()
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !strings.Contains(stdout.String(), "USAGE") {
			t.Errorf("stdout missing USAGE:\n%s", stdout.String())
		}
	})

	t.Run("subcommand help comes from its definition", func(t *testing.T) {
		var stdout bytes.Buffer
		cmd := newRootCmd()
		cmd.SetOut(&stdout)
		cmd.SetArgs([]string{"policy", "include", "--help"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"USAGE", "stonewall policy include <url|path>", "GLOBAL OPTIONS", "--policy FILE"} {
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("subcommand help missing %q:\n%s", want, stdout.String())
			}
		}
	})

	t.Run("--help prints help", func(t *testing.T) {
		cmd := newRootCmd()
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"--help"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !strings.Contains(stdout.String(), "USAGE") {
			t.Errorf("stdout missing USAGE:\n%s", stdout.String())
		}
	})

	t.Run("-p with no value errors", func(t *testing.T) {
		cmd := newRootCmd()
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"-p"})
		if err := cmd.Execute(); err == nil {
			t.Error("expected an error for -p with no value")
		}
	})

	t.Run("first run writes the policy without asking", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(wd)
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		stdin = strings.NewReader("n\n") // decline the remote includes the scaffold pulls in
		defer func() { stdin = os.Stdin }()

		cmd := newRootCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"--plain", "-n", "sh", "-c", "true"})
		_ = cmd.Execute() // fails on the untrusted includes; the scaffold must be on disk regardless
		b, err := os.ReadFile(filepath.Join(dir, ".stonewall.yml"))
		if err != nil || !strings.Contains(string(b), "include:") {
			t.Fatalf("scaffold not written: %v", err)
		}
	})

	t.Run("launch refuses a policy that fails the schema", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".stonewall.yml"), []byte("bin:\n  allowed: [/bin/sh]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(wd)
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}

		cmd := newRootCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"--plain", "-n", "sh", "-c", "true"})
		err = cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), ".stonewall.yml") || !strings.Contains(err.Error(), "/bin/allowed/0") {
			t.Errorf("launch with an invalid policy: %v", err)
		}
	})

	t.Run("interspersed off passes --weird through", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(wd)
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}

		cmd := newRootCmd()
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"-n", "sh", "-c", "--weird"})
		err = cmd.Execute()
		// interspersed-off means "--weird" is a positional arg for the agent, never a
		// stonewall flag; cobra/pflag would report an "unknown flag" error if it were parsed.
		if err != nil && strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("--weird was parsed as a stonewall flag: %v", err)
		}
	})
}

func TestPickPolicies(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_index.yml" {
			fmt.Fprintf(w, "policies:\n  - name: Base\n    url: %[1]s/base.yml\n    description: Bare minimum.\n  - name: Claude Code\n    url: %[1]s/claude.yml\n    description: For Claude Code.\n", srv.URL)
			return
		}
		io.WriteString(w, "bin:\n  allowed: [cat]\n")
	}))
	defer srv.Close()
	defer func(u string) { policy.IndexURL = u }(policy.IndexURL)
	policy.IndexURL = srv.URL + "/_index.yml"
	httpClient = srv.Client()
	defer func() { httpClient = nil }()

	dir := t.TempDir()
	path := filepath.Join(dir, ".stonewall.yml")
	if err := os.WriteFile(path, []byte("include:\n  - "+srv.URL+"/base.yml\nbin:\n  allowed: [sh]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// down, tick Claude, up, untick Base, enter; then "y" trusts the new remote policy
	stdin = strings.NewReader("\x1b[B \x1b[A \ry\n")
	defer func() { stdin = os.Stdin }()

	cmd := newRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--plain", "-p", path, "policy", "pick"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("pick: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "/base.yml") || !strings.Contains(string(b), "  - "+srv.URL+"/claude.yml\n") {
		t.Errorf("include list after pick:\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, ".stonewall", "policies", "lock.yml")); err != nil {
		t.Errorf("new include was not reviewed and cached: %v", err)
	}

	// Esc leaves the file alone.
	before := string(b)
	stdin = strings.NewReader("\x1b")
	cmd = newRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--plain", "-p", path, "policy", "pick"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("cancelled pick: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != before {
		t.Errorf("cancel changed the file:\n%s", after)
	}
}

func TestValidateCommand(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yml")
	if err := os.WriteFile(good, []byte("bin:\n  allowed: [cat]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.yml")
	if err := os.WriteFile(bad, []byte("bin:\n  allowed: [/bin/cat]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "include: [other.yml]\nbin:\n  allowed: [cat]\n")
	}))
	defer srv.Close()
	httpClient = srv.Client()
	defer func() { httpClient = nil }()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	run := func(ref string) error {
		cmd := newRootCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"--plain", "policy", "validate", ref})
		return cmd.Execute()
	}
	if err := run(good); err != nil {
		t.Errorf("valid file: %v", err)
	}
	if err := run(bad); err == nil || !strings.Contains(err.Error(), "/bin/allowed/0") {
		t.Errorf("invalid file: %v", err)
	}
	if err := run(srv.URL + "/p.yml"); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Errorf("remote policy with includes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".stonewall")); err == nil {
		t.Error("validate wrote a policy directory")
	}
}
