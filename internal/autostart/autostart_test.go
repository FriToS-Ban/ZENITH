package autostart_test

import (
	"testing"

	"github.com/shramanb113/ZENITH/internal/autostart"
)

func TestNewReturnsNonNil(t *testing.T) {
	mgr := autostart.New()
	if mgr == nil {
		t.Fatal("New() returned nil")
	}
}

func TestIsInstalledReturnsFalseBeforeInstall(t *testing.T) {
	mgr := autostart.New()
	_, err := mgr.IsInstalled()
	if err != nil {
		t.Fatalf("IsInstalled: %v", err)
	}
}
