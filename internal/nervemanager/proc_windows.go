//go:build windows

package nervemanager

import (
	"os/exec"
	"syscall"
)

// detach creates a new process group so the nerve sidecar is not killed when
// the parent zenith console window closes.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}
