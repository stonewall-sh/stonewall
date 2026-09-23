package sandbox

import "os/exec"

// Sandbox runs an agent under one platform's kernel-enforced restrictions. NewSandbox returns the
// implementation for the OS this binary was built for.
type Sandbox interface {
	// Command returns the process that runs plan's agent inside the sandbox, and a short name for it.
	Command(plan *Plan) (cmd *exec.Cmd, name string, err error)
	// Start starts cmd, applying whatever extra restriction this platform still needs at fork time —
	// on Linux that's nothing; real enforcement happens later, inside the sandbox (see shim.go).
	// warning is advisory and non-fatal; err is cmd.Start()'s own error.
	Start(cmd *exec.Cmd, plan *Plan) (warning string, err error)
}
