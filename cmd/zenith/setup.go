package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shramanb113/ZENITH/internal/nerve"
	"github.com/shramanb113/ZENITH/internal/nervemanager"
	"github.com/spf13/cobra"
)

// errSetupRequired is returned by checkSetupDone so main() can distinguish it
// from real command errors and avoid printing a duplicate message.
var errSetupRequired = errors.New("setup required")

// setupSentinelPath is a var so tests can redirect it to a temp directory.
var setupSentinelPath = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "setup_ok"
	}
	return filepath.Join(home, ".zenith", "setup_ok")
}

func isSetupDone() bool {
	want := nervemanager.New().RequirementsHash()
	got, err := os.ReadFile(setupSentinelPath())
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(got)) == want
}

func writeSetupSentinel() error {
	p := setupSentinelPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(nervemanager.New().RequirementsHash()), 0o644)
}

// checkSetupDone prints a hard error and returns errSetupRequired when setup
// has not completed. Wired into rootCmd.PersistentPreRunE.
func checkSetupDone() error {
	if isSetupDone() {
		return nil
	}
	fmt.Fprintf(os.Stderr, "\n  %s  ZENITH is not set up yet. Run:\n\n       zenith setup\n\n  This downloads the embedding models and verifies the search engine.\n  No other commands will work until setup completes successfully.\n\n", bold("✗"))
	return errSetupRequired
}

var setupFlags struct {
	force bool
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "First-run setup: install Python packages and download embedding models",
	Long: `Prepares ZENITH for use by running four steps:

  [1/4] Create the Python virtual environment
  [2/4] Install packages via uv (torch, sentence-transformers, ~2 GB)
  [3/4] Start the nerve gRPC server and verify it responds
  [4/4] Download embedding model weights (~1.6 GB — up to 20 min on first run)

Run this once after 'go install'. All other zenith commands are blocked until
setup completes successfully.

Use --force to wipe the environment and start from scratch.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSetup()
	},
}

func init() {
	setupCmd.Flags().BoolVar(&setupFlags.force, "force", false,
		"Delete the setup sentinel and venv, then redo everything from scratch")
}

func runSetup() error {
	printHeader("setup", "first-run initialisation")

	nm := nervemanager.New()

	if setupFlags.force {
		fmt.Printf("  %s  --force: clearing previous setup state\n\n", yellow("!"))
		_ = os.Remove(setupSentinelPath())
		nm.ResetSetup()
	}

	// ── [1/4] Python environment ──────────────────────────────────────────────
	fmt.Printf("  %s  Python environment\n", dim("[1/4]"))
	if err := nm.Extract(); err != nil {
		fmt.Printf("  %s  failed to extract nerve files: %v\n\n", yellow("!"), err)
		return fmt.Errorf("setup [1/4] failed: %w", err)
	}
	python, err := nm.FindPython()
	if err != nil {
		fmt.Printf("  %s  Python 3 not found\n\n", yellow("!"))
		fmt.Println("  Install Python 3.10+ from https://www.python.org/downloads/")
		fmt.Println()
		return fmt.Errorf("setup [1/4] failed: %w", err)
	}
	fmt.Printf("  %s  %s\n", green("✓"), dim(python))

	// ── [2/4] Installing packages ─────────────────────────────────────────────
	fmt.Printf("\n  %s  Installing packages  %s\n", dim("[2/4]"),
		dim("(torch CPU + dependencies, ~600 MB — may take several minutes)"))
	if err := nm.EnsureDeps(python); err != nil {
		fmt.Printf("  %s  package install failed — see output above\n\n", yellow("!"))
		return fmt.Errorf("setup [2/4] failed: %w", err)
	}
	fmt.Printf("  %s  packages installed\n", green("✓"))

	// ── [3/4] Nerve smoke test ────────────────────────────────────────────────
	fmt.Printf("\n  %s  Starting nerve\n", dim("[3/4]"))

	// If already running (e.g. from a previous partial run), skip launch.
	quickCtx, quickCancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	alreadyUp := nm.WaitReady(quickCtx, 400*time.Millisecond)
	quickCancel()

	if !alreadyUp {
		if err := nm.Launch(); err != nil {
			fmt.Printf("  %s  failed to launch nerve: %v\n\n", yellow("!"), err)
			return fmt.Errorf("setup [3/4] failed: %w", err)
		}
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer waitCancel()
	if !nm.WaitReady(waitCtx, 30*time.Second) {
		fmt.Printf("  %s  nerve did not start within 30 s\n", yellow("!"))
		fmt.Printf("       check %s\n\n", dim("~/.zenith/nerve/nerve.log"))
		return fmt.Errorf("setup [3/4] failed: nerve timeout")
	}
	fmt.Printf("  %s  nerve listening on %s\n", green("✓"), dim(nm.Addr()))

	// ── [4/4] Model warm-up ───────────────────────────────────────────────────
	fmt.Printf("\n  %s  Warming up models  %s\n", dim("[4/4]"),
		dim("(loading torch + downloading weights on first run — up to 20 min)"))

	nc, err := nerve.NewNerveClient(nm.Addr())
	if err != nil {
		fmt.Printf("  %s  could not dial nerve: %v\n\n", yellow("!"), err)
		return fmt.Errorf("setup [4/4] failed: %w", err)
	}
	defer nc.Close()

	warmCtx, warmCancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer warmCancel()
	vec, err := nc.Embedder().Embed(warmCtx, "zenith model warmup")
	if err != nil || len(vec) == 0 {
		fmt.Printf("  %s  model warm-up failed: %v\n", yellow("!"), err)
		fmt.Printf("       check %s\n\n", dim("~/.zenith/nerve/nerve.log"))
		return fmt.Errorf("setup [4/4] failed: %w", err)
	}
	fmt.Printf("  %s  models ready\n", green("✓"))

	// ── Write sentinel ────────────────────────────────────────────────────────
	if err := writeSetupSentinel(); err != nil {
		fmt.Printf("  %s  could not write sentinel: %v\n", yellow("!"), err)
		return fmt.Errorf("setup: could not write sentinel: %w", err)
	}

	// ── Summary ───────────────────────────────────────────────────────────────
	printDivider()
	printFooter("ZENITH is ready", "run 'zenith index <directory>' to get started")
	return nil
}
