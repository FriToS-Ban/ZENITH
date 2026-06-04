//go:build windows

package zenith

import (
	"syscall"
)

const processQueryInformation = 0x0400

// processAlive returns true if the process with the given PID is still running.
// On Windows, we attempt to open the process handle; failure means it's gone.
func processAlive(pid int) bool {
	h, err := syscall.OpenProcess(processQueryInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	syscall.CloseHandle(h)
	return true
}
