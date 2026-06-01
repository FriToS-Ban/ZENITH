package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunUninstall_CancelledOnNo(t *testing.T) {
	tmpBin := filepath.Join(t.TempDir(), "zenith-test-binary")
	if err := os.WriteFile(tmpBin, []byte("fake binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	origExec := executableFn
	executableFn = func() (string, error) { return tmpBin, nil }
	defer func() { executableFn = origExec }()

	input := strings.NewReader("no\n")
	if err := runUninstall(input); err != nil {
		t.Fatalf("runUninstall: %v", err)
	}

	if _, err := os.Stat(tmpBin); err != nil {
		t.Error("binary was deleted despite 'no' answer")
	}
}

func TestRunUninstall_CancelledOnEmpty(t *testing.T) {
	tmpBin := filepath.Join(t.TempDir(), "zenith-test-binary")
	_ = os.WriteFile(tmpBin, []byte("fake"), 0o755)

	origExec := executableFn
	executableFn = func() (string, error) { return tmpBin, nil }
	defer func() { executableFn = origExec }()

	input := strings.NewReader("\n")
	if err := runUninstall(input); err != nil {
		t.Fatalf("runUninstall: %v", err)
	}

	if _, err := os.Stat(tmpBin); err != nil {
		t.Error("binary was deleted despite empty answer")
	}
}

func TestResolveBinaryPath(t *testing.T) {
	origExec := executableFn
	executableFn = func() (string, error) { return "/tmp/zenith", nil }
	defer func() { executableFn = origExec }()

	p, err := resolveBinaryPath()
	if err != nil {
		t.Fatalf("resolveBinaryPath: %v", err)
	}
	if p == "" {
		t.Error("expected non-empty path")
	}
}
