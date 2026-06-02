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

	printHeader("uninstall", "remove binary and nerve sidecar")

	fmt.Printf("  %s  will be removed:\n", yellow("!"))
	fmt.Printf("       %s  %s\n", cc("✗", cErrCol), muted(binPath+"  (binary)"))
	fmt.Printf("       %s  %s\n\n", cc("✗", cErrCol), muted(nerveDir+"  (nerve sidecar + venv)"))

	fmt.Printf("  %s  index data is kept:\n", green("✓"))
	fmt.Printf("       %s\n", muted("./data/wal/   ./data/sst/   zenith.db"))
	fmt.Println()
	fmt.Printf("  Type %s to continue: ", cc(`"yes"`, ansiBold+cBrand))

	scanner := bufio.NewScanner(input)
	scanner.Scan()
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	if answer != "yes" {
		fmt.Printf("\n  %s  uninstall cancelled\n\n", muted("·"))
		return nil
	}
	fmt.Println()

	// Remove nerve dir first (safe on all platforms).
	if _, err := os.Stat(nerveDir); err == nil {
		if err := os.RemoveAll(nerveDir); err != nil {
			fmt.Fprintf(os.Stderr, "  %s  failed to remove %s: %v\n", yellow("!"), nerveDir, err)
		} else {
			fmt.Printf("  %s  %s\n", green("✓"), muted(nerveDir+" removed"))
		}
	}

	// Remove binary — Windows may deny deletion of a running exe.
	if binPath != "(could not determine binary path)" {
		if runtime.GOOS == "windows" {
			fmt.Printf("  %s  binary cannot be removed while running on Windows\n", yellow("!"))
			fmt.Printf("       delete manually: %s\n", muted(binPath))
		} else {
			if err := os.Remove(binPath); err != nil {
				fmt.Fprintf(os.Stderr, "  %s  failed to remove binary: %v\n", yellow("!"), err)
			} else {
				fmt.Printf("  %s  %s\n", green("✓"), muted(binPath+" removed"))
			}
		}
	}

	printDivider()
	printFooter("uninstall complete")
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
