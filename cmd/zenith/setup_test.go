package main

import (
	"os"
	"path/filepath"
	"testing"
)

func withTempSentinel(t *testing.T) (restore func()) {
	t.Helper()
	dir := t.TempDir()
	orig := setupSentinelPath
	setupSentinelPath = func() string { return filepath.Join(dir, "setup_ok") }
	return func() { setupSentinelPath = orig }
}

func TestIsSetupDone_FalseWhenMissing(t *testing.T) {
	defer withTempSentinel(t)()
	if isSetupDone() {
		t.Error("isSetupDone should be false when sentinel file does not exist")
	}
}

func TestWriteAndReadSentinel_RoundTrip(t *testing.T) {
	defer withTempSentinel(t)()
	if err := writeSetupSentinel(); err != nil {
		t.Fatalf("writeSetupSentinel: %v", err)
	}
	if !isSetupDone() {
		t.Error("isSetupDone should be true after writeSetupSentinel")
	}
}

func TestIsSetupDone_FalseAfterSentinelTampered(t *testing.T) {
	defer withTempSentinel(t)()
	if err := writeSetupSentinel(); err != nil {
		t.Fatal(err)
	}
	// Tamper — simulate requirements.txt changing after an update
	_ = os.WriteFile(setupSentinelPath(), []byte("deadbeef"), 0o644)
	if isSetupDone() {
		t.Error("isSetupDone should be false when sentinel hash does not match")
	}
}
