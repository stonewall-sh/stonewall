package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"
)

// evalSymlinks is swappable so tests can fix macOS's shell-selector resolution without touching the
// real filesystem, mirroring bwrap.go's readlink.
var evalSymlinks = filepath.EvalSymlinks

// shVariant returns the real binary /bin/sh execs as. /bin/sh isn't a symlink — the OS re-execs it via
// the admin-configurable selector /var/select/sh (default /bin/bash). Falls back to /bin/bash
// pre-Catalina, where that selector doesn't exist.
func shVariant() string {
	if real, err := evalSymlinks("/var/select/sh"); err == nil {
		return real
	}
	return "/bin/bash"
}

// SeatbeltProfile renders the plan as a macOS sandbox profile. Later rules override earlier ones.
func SeatbeltProfile(p *Plan) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n(deny process-exec*)\n")
	fmt.Fprintf(&b, "(deny file-read* file-write* (subpath %s))\n", sbpl(p.Home))
	fmt.Fprintf(&b, "(allow file-read-metadata (literal %s))\n", sbpl(p.Home)) // stat only, no listing: node and bash resolve exposed paths through $HOME
	fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %s))\n", sbpl(p.Project))
	for _, e := range p.ExposeWrite {
		fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %s))\n", sbpl(e))
	}
	for _, e := range p.ExposeRead {
		fmt.Fprintf(&b, "(allow file-read* (subpath %s))\n", sbpl(e))
		fmt.Fprintf(&b, "(deny file-write* (subpath %s))\n", sbpl(e))
	}
	for _, r := range p.Readonly {
		fmt.Fprintf(&b, "(deny file-write* (subpath %s))\n", sbpl(r))
	}
	for _, r := range p.ReadonlyFiles { // outside the project, where (allow default) would leave them writable
		fmt.Fprintf(&b, "(deny file-write* (subpath %s))\n", sbpl(r))
	}
	for _, h := range append(append([]string{}, p.HiddenDirs...), p.HiddenFiles...) {
		fmt.Fprintf(&b, "(deny file-read* file-write* process-exec* (subpath %s))\n", sbpl(h)) // file-read* alone still lets exec through
	}
	for _, path := range sortedValues(p.Bins) {
		if strings.HasPrefix(path, p.Home+"/") { // binaries under $HOME, otherwise denied above
			fmt.Fprintf(&b, "(allow file-read* (subpath %s))\n", sbpl(path))
		}
		fmt.Fprintf(&b, "(allow process-exec (literal %s))\n", sbpl(path))
		if path == "/bin/sh" {
			fmt.Fprintf(&b, "(allow process-exec (literal %s))\n", sbpl(shVariant()))
		}
	}
	fmt.Fprintf(&b, "(allow file-read* (subpath %s))\n", sbpl(p.BinDir))
	return b.String()
}

// SeatbeltArgs renders the arguments for /usr/bin/sandbox-exec.
func SeatbeltArgs(p *Plan) []string {
	return append([]string{"-p", SeatbeltProfile(p)}, p.Argv...)
}

// sbpl quotes s as a Seatbelt string literal.
func sbpl(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
