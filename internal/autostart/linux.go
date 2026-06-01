//go:build linux

package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const linuxServiceName = "zenith-watch.service"

type linuxManager struct{}

func newManager() Manager { return &linuxManager{} }

func (m *linuxManager) servicePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user", linuxServiceName), nil
}

func serviceContent(execPath string) string {
	return fmt.Sprintf(`[Unit]
Description=ZENITH watch daemon
After=network.target

[Service]
Type=simple
ExecStart=%s watch start
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, execPath)
}

func (m *linuxManager) Install(execPath string) error {
	p, err := m.servicePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(serviceContent(execPath)), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("systemctl", "--user", "enable", "--now", "zenith-watch")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl: %w\n%s\n\nTo enable manually:\n  systemctl --user enable --now zenith-watch", err, out)
	}
	return nil
}

func (m *linuxManager) Uninstall() error {
	p, err := m.servicePath()
	if err != nil {
		return err
	}
	_ = exec.Command("systemctl", "--user", "disable", "--now", "zenith-watch").Run()
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *linuxManager) IsInstalled() (bool, error) {
	p, err := m.servicePath()
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	return err == nil, nil
}
