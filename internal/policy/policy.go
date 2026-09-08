// Package policy loads and merges Stonewall sandbox policies.
package policy

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// FileName is the repo-local policy file, found at the project root.
const FileName = ".stonewall.yml"

// Meta describes a policy to people and to the policy index. It has no effect on the sandbox.
type Meta struct {
	Name        string `yaml:"name,omitempty"`
	URL         string `yaml:"url,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// Policy is a parsed .stonewall.yml. All fields are optional.
type Policy struct {
	Meta    *Meta    `yaml:"policy,omitempty"`  // who and what the policy is; Merge drops it
	Include []string `yaml:"include,omitempty"` // policies applied before this file, in order; local paths or https:// URLs
	Bin     Bin      `yaml:"bin"`
	Project Project  `yaml:"project"`
	Expose  Expose   `yaml:"expose"`
}

// Bin controls what is on PATH inside the sandbox.
type Bin struct {
	Allowed []string `yaml:"allowed"`          // program names on PATH
	Denied  []string `yaml:"denied,omitempty"` // program names removed from an earlier source's allowed
}

// Project holds restrictions on paths relative to the project root.
type Project struct {
	Hidden   []string `yaml:"hidden"`             // content unreadable
	Readonly []string `yaml:"readonly"`           // read but not change
	Writable []string `yaml:"writable,omitempty"` // paths released from an earlier source's hidden or readonly
}

// Expose grants access to host paths outside the project (~/ or absolute).
type Expose struct {
	Read  []string `yaml:"read"`
	Write []string `yaml:"write"`          // read-write
	None  []string `yaml:"none,omitempty"` // paths whose exposure by an earlier source is withdrawn
}

// FindRoot walks up from dir and returns the nearest directory holding FileName or .git, else dir itself.
func FindRoot(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if exists(filepath.Join(d, FileName)) || exists(filepath.Join(d, ".git")) {
			return d
		}
		if filepath.Dir(d) == d {
			return dir
		}
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Load reads and parses a policy file. It does not resolve includes; see Loader.
func Load(path string) (Policy, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, err
	}
	p, err := Parse(b)
	if err != nil {
		return Policy{}, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// Parse checks YAML against the policy schema and decodes it. Unknown keys and malformed entries are reported
// with their path; an empty document is an empty policy.
func Parse(b []byte) (Policy, error) {
	var doc any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return Policy{}, err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	if err := schema().Validate(doc); err != nil {
		return Policy{}, err
	}
	var p Policy
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil && !errors.Is(err, io.EOF) {
		return Policy{}, err
	}
	return p, nil
}

// Scaffold returns the policy proposed for a project whose first launch runs agent.
//
//go:embed scaffold.yml
var scaffoldYML string

// Scaffold renders scaffold.yml for a project whose first launch runs agent: the base policy, plus the
// Claude Code policy when the agent is claude.
func Scaffold(agent string) string {
	includes := []string{"https://stonewall.sh/policies/base.yml"}
	if strings.HasPrefix(filepath.Base(agent), "claude") {
		includes = append(includes, "https://stonewall.sh/policies/claude.yml")
	}
	var b strings.Builder
	if err := template.Must(template.New("scaffold").Parse(scaffoldYML)).Execute(&b, struct{ Includes []string }{includes}); err != nil {
		panic(err) // the template is embedded and fixed
	}
	return b.String()
}

// WriteScaffold creates path holding content. It fails if path exists.
func WriteScaffold(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
