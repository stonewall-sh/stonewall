//go:build !linux

package sandbox

// restrictSelfExec is a no-op here: there is no confirmed equivalent to Landlock's kernel-level
// exec restriction outside Linux. See restrict_linux.go.
func restrictSelfExec(paths []string) (locked bool, warning string) {
	return false, ""
}
