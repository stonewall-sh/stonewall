//go:build linux

package sandbox

import (
	"errors"
	"os/exec"
	"runtime"
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

func (linuxSandbox) Start(cmd *exec.Cmd, plan *Plan) (warning string, err error) {
	locked, warning := restrictExec(plan.Bins, cmd.Path)
	if err = cmd.Start(); err != nil {
		if locked {
			runtime.UnlockOSThread()
		}
		return warning, err
	}
	if locked {
		runtime.UnlockOSThread()
	}
	return warning, nil
}
