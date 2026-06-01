package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
)

const (
	githubReleasesURL = "https://api.github.com/repos/shramanb113/ZENITH/releases/latest"
	installTarget     = "github.com/shramanb113/ZENITH/cmd/zenith@latest"
)

// versionCheckerFn fetches the latest release tag from GitHub.
// Replaced in tests to avoid network calls.
var versionCheckerFn = fetchLatestVersion

// goInstallFn runs go install. Replaced in tests to avoid spawning a process.
var goInstallFn = runGoInstall

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update ZENITH to the latest release",
	Long: `Checks GitHub for a newer release and runs:
  go install github.com/shramanb113/ZENITH/cmd/zenith@latest

Requires go in PATH. Index data and the nerve sidecar are unaffected.
The nerve sidecar is refreshed automatically on next run.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUpdate()
	},
}

func runUpdate() error {
	fmt.Println()
	fmt.Println("  Checking for updates...")

	latest, err := versionCheckerFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ! Could not check for updates: %v\n", err)
		fmt.Println("  Proceeding with install anyway...")
	} else {
		fmt.Printf("  Current:  %s\n", version)
		fmt.Printf("  Latest:   %s\n", latest)

		if version != "dev" && version == latest {
			fmt.Printf("\n  ✓ Already up to date (%s).\n\n", version)
			return nil
		}
		if version == "dev" {
			fmt.Println("  Development build detected — skipping version comparison.")
		}
	}

	fmt.Printf("\n  Running: go install %s\n", installTarget)
	if err := goInstallFn(); err != nil {
		return err
	}

	if latest != "" && version != latest {
		fmt.Printf("\n  ✓ Updated to %s. Restart zenith to use the new version.\n\n", latest)
	} else {
		fmt.Println("\n  ✓ Updated to latest release.")
		fmt.Println()
	}
	return nil
}

func fetchLatestVersion() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, githubReleasesURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GitHub API unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("could not parse GitHub response: %w", err)
	}
	return payload.TagName, nil
}

func runGoInstall() error {
	goPath, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("go not found in PATH.\nInstall Go from https://go.dev/dl/ then re-run")
	}

	cmd := exec.Command(goPath, "install", installTarget)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Bypass the Go module proxy so the install always fetches the latest
	// commit directly from GitHub instead of a potentially stale cached version.
	cmd.Env = append(os.Environ(), "GOPROXY=direct", "GONOSUMDB=*")
	return cmd.Run()
}
