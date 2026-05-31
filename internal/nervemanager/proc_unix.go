//go:build !windows

package nervemanager

import (
	"os/exec"
	"syscall"
)

// detach puts the child process into its own session so it survives after
// the parent zenith process exits (no SIGHUP on terminal close).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
