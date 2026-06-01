//go:build darwin

package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	darwinLabel     = "com.zenith.watch"
	darwinPlistFile = "com.zenith.watch.plist"
)

type darwinManager struct{}

func newManager() Manager { return &darwinManager{} }

func (m *darwinManager) plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", darwinPlistFile), nil
}

func plistContent(execPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>watch</string>
        <string>start</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <false/>
</dict>
</plist>
`, darwinLabel, execPath)
}

func (m *darwinManager) Install(execPath string) error {
	p, err := m.plistPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(plistContent(execPath)), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("launchctl", "load", "-w", p)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl: %w\n%s", err, out)
	}
	return nil
}

func (m *darwinManager) Uninstall() error {
	p, err := m.plistPath()
	if err != nil {
		return err
	}
	_ = exec.Command("launchctl", "unload", "-w", p).Run()
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *darwinManager) IsInstalled() (bool, error) {
	p, err := m.plistPath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	return err == nil, nil
}
