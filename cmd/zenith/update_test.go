package main

import (
	"errors"
	"testing"
)

func TestRunUpdate_AlreadyLatest(t *testing.T) {
	origVersion := version
	version = "v1.2.0"
	defer func() { version = origVersion }()

	installCalled := false
	origInstall := goInstallFn
	goInstallFn = func() error { installCalled = true; return nil }
	defer func() { goInstallFn = origInstall }()

	origChecker := versionCheckerFn
	versionCheckerFn = func() (string, error) { return "v1.2.0", nil }
	defer func() { versionCheckerFn = origChecker }()

	if err := runUpdate(); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if installCalled {
		t.Error("go install should not have been called when already on latest")
	}
}

func TestRunUpdate_NewVersionAvailable(t *testing.T) {
	origVersion := version
	version = "v1.1.0"
	defer func() { version = origVersion }()

	installCalled := false
	origInstall := goInstallFn
	goInstallFn = func() error { installCalled = true; return nil }
	defer func() { goInstallFn = origInstall }()

	origChecker := versionCheckerFn
	versionCheckerFn = func() (string, error) { return "v1.2.0", nil }
	defer func() { versionCheckerFn = origChecker }()

	if err := runUpdate(); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if !installCalled {
		t.Error("go install should have been called for a newer version")
	}
}

func TestRunUpdate_DevBuild_InstallsUnconditionally(t *testing.T) {
	origVersion := version
	version = "dev"
	defer func() { version = origVersion }()

	installCalled := false
	origInstall := goInstallFn
	goInstallFn = func() error { installCalled = true; return nil }
	defer func() { goInstallFn = origInstall }()

	origChecker := versionCheckerFn
	versionCheckerFn = func() (string, error) { return "v1.2.0", nil }
	defer func() { versionCheckerFn = origChecker }()

	if err := runUpdate(); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if !installCalled {
		t.Error("go install should run for dev build")
	}
}

func TestRunUpdate_NetworkFailure_StillInstalls(t *testing.T) {
	origVersion := version
	version = "v1.0.0"
	defer func() { version = origVersion }()

	installCalled := false
	origInstall := goInstallFn
	goInstallFn = func() error { installCalled = true; return nil }
	defer func() { goInstallFn = origInstall }()

	origChecker := versionCheckerFn
	versionCheckerFn = func() (string, error) { return "", errors.New("network error") }
	defer func() { versionCheckerFn = origChecker }()

	if err := runUpdate(); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if !installCalled {
		t.Error("go install should still run after network failure")
	}
}

func TestRunUpdate_GoNotInPath(t *testing.T) {
	origVersion := version
	version = "v1.0.0"
	defer func() { version = origVersion }()

	origInstall := goInstallFn
	goInstallFn = func() error { return errors.New("go not found in PATH") }
	defer func() { goInstallFn = origInstall }()

	origChecker := versionCheckerFn
	versionCheckerFn = func() (string, error) { return "v1.1.0", nil }
	defer func() { versionCheckerFn = origChecker }()

	err := runUpdate()
	if err == nil {
		t.Error("expected error when go is not in PATH")
	}
}
