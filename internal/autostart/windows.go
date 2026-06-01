//go:build windows

package autostart

import (
	"fmt"
	"os/exec"
	"strings"
)

const taskName = "ZenithWatch"

type windowsManager struct{}

func newManager() Manager { return &windowsManager{} }

func (m *windowsManager) Install(execPath string) error {
	cmd := exec.Command("schtasks",
		"/Create",
		"/TN", taskName,
		"/SC", "ONLOGON",
		"/DELAY", "0000:30",
		"/TR", `"`+execPath+`" watch start`,
		"/F",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks create: %w\n%s", err, out)
	}
	return nil
}

func (m *windowsManager) Uninstall() error {
	cmd := exec.Command("schtasks", "/Delete", "/TN", taskName, "/F")
	out, err := cmd.CombinedOutput()
	if err != nil {
		lower := strings.ToLower(string(out))
		if strings.Contains(lower, "cannot find") || strings.Contains(lower, "does not exist") {
			return nil
		}
		return fmt.Errorf("schtasks delete: %w\n%s", err, out)
	}
	return nil
}

func (m *windowsManager) IsInstalled() (bool, error) {
	err := exec.Command("schtasks", "/Query", "/TN", taskName).Run()
	return err == nil, nil
}
