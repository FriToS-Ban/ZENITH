//go:build windows

package nervemanager

import (
	"os/exec"
	"syscall"
)

// detach creates a new process group and detaches from the parent console so
// the nerve sidecar is not killed when the parent zenith terminal closes.
func detach(cmd *exec.Cmd) {
	const detachedProcess = 0x00000008 // Win32 DETACHED_PROCESS
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess,
	}
}
