//go:build !windows

package zenith

import "syscall"

// processAlive returns true if the process with the given PID is still running.
// On Unix, sending signal 0 probes the process without disturbing it.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}
