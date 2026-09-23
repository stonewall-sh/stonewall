//go:build linux

package sandbox

import (
	"errors"
	"os/exec"
)

// NewSandbox returns the Linux Sandbox.
func NewSandbox() (Sandbox, error) {
	return linuxSandbox{}, nil
}

type linuxSandbox struct{}

func (linuxSandbox) Command(plan *Plan) (*exec.Cmd, string, error) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, "", errors.New("bubblewrap is required on Linux and was not found on PATH. Install it with your package manager: apt install bubblewrap, dnf install bubblewrap, or pacman -S bubblewrap")
	}
	return exec.Command(bwrapPath, BwrapArgs(plan)...), "bwrap", nil
}

// Start needs nothing beyond cmd.Start(): Landlock isn't applied here, at bwrap's own fork, but
// later, inside the sandbox by ExecShim (see shim.go) — bwrap needs to finish its own unprivileged
// namespace setup first, which breaks if the process forking it already carries the no_new_privs
// bit Landlock requires.
func (linuxSandbox) Start(cmd *exec.Cmd, plan *Plan) (warning string, err error) {
	return "", cmd.Start()
}
