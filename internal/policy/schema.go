package policy

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// SchemaURL is where the policy schema is published. The embedded copy is the same file.
const SchemaURL = "https://stonewall.sh/schema.json"

//go:embed schema.json
var schemaJSON string

// schema compiles the embedded schema once. It is fixed, so a failure is a build defect.
var schema = sync.OnceValue(func() *jsonschema.Schema {
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schemaJSON))
	if err != nil {
		panic(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(SchemaURL, doc); err != nil {
		panic(err)
	}
	return c.MustCompile(SchemaURL)
})

// Validate parses b, which checks it against the published schema, and then checks its lists for conflicts.
// It returns the parsed policy, or an error listing every violation with the path of each.
func Validate(b []byte) (Policy, error) {
	p, err := Parse(b)
	if err != nil {
		return Policy{}, err
	}
	if _, err := Merge(p); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// Validate checks the policy at ref, an https:// URL or a local path. A remote policy is fetched under
// the rules of a download and may not have includes of its own; nothing is cached or recorded.
func (l Loader) Validate(ref string) error {
	if strings.Contains(ref, "://") {
		u, err := remoteURL(ref)
		if err != nil {
			return err
		}
		_, _, err = l.download(u)
		return err
	}
	if strings.HasPrefix(ref, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		ref = filepath.Join(home, ref[2:])
	}
	b, err := os.ReadFile(ref)
	if err != nil {
		return err
	}
	_, err = Validate(b)
	return err
}
