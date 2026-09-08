package sandbox

import (
	"fmt"
	"strings"
)

// SeatbeltProfile renders the plan as a macOS sandbox profile. Later rules override earlier ones.
func SeatbeltProfile(p *Plan) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")
	fmt.Fprintf(&b, "(deny file-read* file-write* (subpath %s))\n", sbpl(p.Home))
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
