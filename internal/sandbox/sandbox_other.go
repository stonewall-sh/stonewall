//go:build !linux && !darwin

package sandbox

import (
	"fmt"
	"runtime"
)

// NewSandbox fails: stonewall only runs on Linux and macOS.
func NewSandbox() (Sandbox, error) {
	return nil, fmt.Errorf("unsupported OS %s: stonewall runs on Linux and macOS", runtime.GOOS)
}
