package policy

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Loader resolves a policy file and its includes into one effective policy.
type Loader struct {
	// Ask shows body under title and asks question; false means no. Body is empty for a new remote policy,
	// the diff for a changed one, and the fetch dates for the staleness prompt.
	Ask      func(title, body, question string) bool
	Updating func()                       // optional; called when an update of the remote policies starts
	Client   *http.Client                 // https includes; tests inject an httptest client
	Now      func() time.Time             // defaults to time.Now; tests set it
	Report   func(results []UpdateResult) // optional; receives what a staleness refresh did
}

// Load parses path, resolves each include in order and merges them with the local file applied last. It returns
// the effective policy and the absolute paths of every local file involved (local includes, cache files, the lock
// file and the cache directory); the caller makes those inside the project read-only. Remote includes come from
// the cache; one without a lock entry or cache file runs Update first, as does a stale cache when the user agrees.
func (l Loader) Load(path string) (Policy, []string, error) {
	local, err := Load(path)
	if err != nil {
		return Policy{}, nil, err
	}
	r, err := l.resolver(path)
	if err != nil {
		return Policy{}, nil, err
	}
	if err := l.refresh(r, local.Include); err != nil {
		return Policy{}, nil, err
	}
	sources, err := r.includes(local.Include)
	if err != nil {
		return Policy{}, nil, err
	}
	if len(remoteURLs(local.Include)) > 0 {
		r.files = append(r.files, r.cacheDir(), r.lockPath)
		if dir := filepath.Dir(r.lockPath); exists(dir) { // the directory itself, so it cannot be renamed away
			r.files = append(r.files, dir)
		}
	}
	if err := r.save(); err != nil {
		return Policy{}, nil, err
	}
	eff, err := Merge(append(sources, local)...)
	if err != nil {
		return Policy{}, nil, err
	}
	return eff, absAll(r.files), nil
}

// refresh runs Update when a remote include has no reviewed copy, and offers it when the cache is stale;
// a declined offer is snoozed for a day.
func (l Loader) refresh(r *resolve, includes []string) error {
	urls := remoteURLs(includes)
	if len(urls) == 0 {
		return nil
	}
	days, stale := r.stale(urls)
	switch {
	case r.needsUpdate(urls):
		return l.runUpdate(r, includes)
	case stale && !r.snoozed():
		title := fmt.Sprintf("Cached remote policies are older than %d days and might be outdated.", days)
		if r.ask(title, r.fetchedList(urls), "Update them now?") {
			return l.runUpdate(r, includes)
		}
		r.lock.SnoozedUntil = r.now().Add(24 * time.Hour)
		r.dirty = true
	}
	return nil
}

// runUpdate refreshes the remote includes during a load, reports the results and returns their Problem.
func (l Loader) runUpdate(r *resolve, includes []string) error {
	results := r.update(includes)
	if l.Report != nil {
		l.Report(results)
	}
	if err := r.save(); err != nil {
		return err
	}
	return Problem(results)
}

// includes resolves the include list in order, refusing anything it cannot read. It records the local
// files it used, so a second pass after an update starts from a clean list.
func (r *resolve) includes(list []string) ([]Policy, error) {
	r.files = nil
	sources := make([]Policy, 0, len(list))
	for _, inc := range list {
		var p Policy
		var err error
		if strings.Contains(inc, "://") {
			p, err = r.remote(inc)
		} else {
			var abs string
			p, abs, err = readLocalInclude(r.path, inc)
			if err == nil {
				r.files = append(r.files, abs)
			}
		}
		if err != nil {
			return nil, err
		}
		if len(p.Include) > 0 {
			return nil, fmt.Errorf("include %s: nested includes are not supported", inc)
		}
		sources = append(sources, p)
	}
	return sources, nil
}

// readLocalInclude resolves inc against the directory of path (or $HOME for ~/) and parses it.
// A relative entry must stay inside the tree of path; ~/ and absolute entries may live anywhere.
func readLocalInclude(path, inc string) (Policy, string, error) {
	abs, relative := inc, false
	switch {
	case strings.HasPrefix(inc, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return Policy{}, "", err
		}
		abs = filepath.Join(home, inc[2:])
	case !filepath.IsAbs(inc):
		abs = filepath.Join(filepath.Dir(path), inc)
		relative = true
	}
	abs, err := filepath.Abs(abs)
	if err != nil {
		return Policy{}, "", err
	}
	if relative {
		if err := insideTree(filepath.Dir(path), abs, inc); err != nil {
			return Policy{}, abs, err
		}
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return Policy{}, abs, fmt.Errorf("include %s: not found", inc)
	}
	p, err := parseInclude(inc, b)
	return p, abs, err
}

// insideTree refuses abs when it resolves outside dir: a ../ entry, or a symlink pointing out of the
// project. A path that cannot be resolved does not exist; the read reports that instead.
func insideTree(dir, abs, inc string) error {
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil
	}
	root, err := filepath.EvalSymlinks(dir)
	if err == nil && (real == root || strings.HasPrefix(real, root+string(filepath.Separator))) {
		return nil
	}
	return fmt.Errorf("include %s: resolves to %s, outside the project", inc, real)
}

func parseInclude(inc string, b []byte) (Policy, error) {
	p, err := Parse(b)
	if err != nil {
		return Policy{}, fmt.Errorf("include %s: %w", inc, err)
	}
	return p, nil
}

func absAll(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if a, err := filepath.Abs(p); err == nil {
			p = a
		}
		out = append(out, p)
	}
	return out
}

// replace writes content over path through a temp file in the same directory, so a crash leaves the old file.
func replace(path string, content []byte) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".stonewall-tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(f.Name(), mode); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// AddInclude appends inc to the include list of the policy file at path, leaving everything else as written,
// comments included. A missing include key is added after the leading comments. It reports false when inc is
// already listed.
// ponytail: line-based; a flow-style list (include: [a, b]) is not handled and reported instead.
func AddInclude(path, inc string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	lines := strings.SplitAfter(string(b), "\n")
	key, at, head, indent := -1, -1, -1, "  " // the include: line, the line to insert after, the first key, the item indent
scan:
	for i, line := range lines {
		item := strings.TrimSpace(line)
		if j := strings.Index(item, "#"); j >= 0 && !strings.HasPrefix(item, "-") {
			item = strings.TrimSpace(item[:j])
		}
		switch {
		case key < 0 && strings.HasPrefix(item, "include:"):
			if rest := strings.TrimSpace(item[len("include:"):]); rest != "" {
				return false, fmt.Errorf("%s: include list is not one item per line, add %s by hand", path, inc)
			}
			key, at = i, i
		case key < 0 && item != "":
			if head < 0 {
				head = i
			}
		case key >= 0 && strings.HasPrefix(item, "-"):
			if itemValue(item) == inc {
				return false, nil
			}
			indent = line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			at = i
		case key >= 0 && item != "":
			break scan // the next key
		}
	}
	entry := indent + "- " + inc + "\n"
	if key < 0 { // no include key: in front of the first key, or at the end of a file without keys
		entry = "include:\n" + entry
		at = head - 1
		if head < 0 {
			at = len(lines) - 1
		}
	}
	var out strings.Builder
	for i, line := range lines {
		if i == at+1 {
			out.WriteString(entry)
		}
		out.WriteString(line)
		if i == at && line != "" && !strings.HasSuffix(line, "\n") {
			out.WriteString("\n")
		}
	}
	if at+1 >= len(lines) {
		out.WriteString(entry)
	}
	return true, replace(path, []byte(out.String()))
}

// RemoveInclude drops inc from the include list of the policy file at path, leaving everything else as
// written. It reports false when inc is not listed.
func RemoveInclude(path, inc string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var kept []string
	key, items := -1, 0
	inList, removed := false, false
	for _, line := range strings.SplitAfter(string(b), "\n") {
		item := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(item, "include:"):
			inList, key = true, len(kept)
		case inList && strings.HasPrefix(item, "-"):
			if itemValue(item) == inc {
				removed = true
				continue
			}
			items++
		case inList && item != "" && !strings.HasPrefix(item, "#"):
			inList = false // the next key
		}
		kept = append(kept, line)
	}
	if !removed {
		return false, nil
	}
	if items == 0 { // a bare include: is null, which the schema refuses
		kept = slices.Delete(kept, key, key+1)
	}
	return true, replace(path, []byte(strings.Join(kept, "")))
}

// itemValue is the unquoted value of a "- value # comment" list item.
func itemValue(item string) string {
	value := strings.TrimSpace(item[1:])
	if j := strings.Index(value, " #"); j >= 0 {
		value = strings.TrimSpace(value[:j])
	}
	return strings.Trim(value, "\"'")
}
