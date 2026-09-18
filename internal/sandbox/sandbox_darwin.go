//go:build darwin

package sandbox

import "os/exec"

// NewSandbox returns the macOS Sandbox.
func NewSandbox() (Sandbox, error) {
	return darwinSandbox{}, nil
}

type darwinSandbox struct{}

func (darwinSandbox) Command(plan *Plan) (*exec.Cmd, string, error) {
	return exec.Command("/usr/bin/sandbox-exec", SeatbeltArgs(plan)...), "sandbox-exec", nil
}

// Start needs nothing beyond cmd.Start(): the Seatbelt profile is already baked into cmd's
// arguments by Command.
func (darwinSandbox) Start(cmd *exec.Cmd, plan *Plan) (warning string, err error) {
	return "", cmd.Start()
}
