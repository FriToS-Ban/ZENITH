package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// executableFn is a var so tests can replace os.Executable.
var executableFn = os.Executable

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove ZENITH binary and nerve sidecar (index data is preserved)",
	Long: `Removes the zenith binary and the nerve Python sidecar venv at ~/.zenith/.

Your index data is NOT affected:
  ./data/wal/, ./data/sst/, ./data/index.fst, zenith.db — all kept.

You will be asked to confirm before anything is deleted.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUninstall(os.Stdin)
	},
}

func runUninstall(input io.Reader) error {
	binPath, err := resolveBinaryPath()
	if err != nil {
		binPath = "(could not determine binary path)"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not determine home directory: %w", err)
	}
	nerveDir := filepath.Join(home, ".zenith")

	fmt.Println()
	fmt.Println("  This will remove:")
	fmt.Printf("    ✗  %s  (binary)\n", binPath)
	fmt.Printf("    ✗  %s  (nerve sidecar + venv)\n", nerveDir)
	fmt.Println()
	fmt.Println("  Your index data is NOT affected:")
	fmt.Println("    ✓  ./data/wal/   (kept)")
	fmt.Println("    ✓  ./data/sst/   (kept)")
	fmt.Println("    ✓  zenith.db     (kept)")
	fmt.Println()
	fmt.Print("  Type \"yes\" to continue: ")

	scanner := bufio.NewScanner(input)
	scanner.Scan()
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if answer != "yes" {
		fmt.Println("\n  Uninstall cancelled.")
		return nil
	}
	fmt.Println()

	// Remove nerve dir first (safe on all platforms).
	if _, err := os.Stat(nerveDir); err == nil {
		if err := os.RemoveAll(nerveDir); err != nil {
			fmt.Fprintf(os.Stderr, "  ! Failed to remove %s: %v\n", nerveDir, err)
		} else {
			fmt.Printf("  ✓  %s removed\n", nerveDir)
		}
	}

	// Remove binary — Windows may deny deletion of a running exe.
	if binPath != "(could not determine binary path)" {
		if runtime.GOOS == "windows" {
			fmt.Println("  !  Binary cannot be removed while running on Windows.")
			fmt.Printf("     Delete manually: %s\n", binPath)
		} else {
			if err := os.Remove(binPath); err != nil {
				fmt.Fprintf(os.Stderr, "  ! Failed to remove binary: %v\n", err)
			} else {
				fmt.Printf("  ✓  %s removed\n", binPath)
			}
		}
	}

	fmt.Println()
	fmt.Println("  ✓  Uninstall complete.")
	fmt.Println()
	return nil
}

func resolveBinaryPath() (string, error) {
	p, err := executableFn()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p, nil
	}
	return resolved, nil
}
