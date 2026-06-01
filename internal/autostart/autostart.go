package autostart

// Manager registers and removes an OS-level boot auto-start entry
// that runs "zenith watch start" at user login.
type Manager interface {
	// Install registers the auto-start entry using execPath as the binary.
	Install(execPath string) error
	// Uninstall removes the auto-start entry. No-op if not installed.
	Uninstall() error
	// IsInstalled reports whether the auto-start entry currently exists.
	IsInstalled() (bool, error)
}

// New returns the Manager for the current operating system.
func New() Manager {
	return newManager()
}
