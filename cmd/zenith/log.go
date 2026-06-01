package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var logFlags struct {
	n      int
	follow bool
	filter string
}

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "Show ZENITH activity log",
	Long: `Displays the persistent activity log at ~/.zenith/zenith.log.

Events logged: INDEXED, REMOVED, SEARCH, NERVE, SAVED, LOADED, PDF

Examples:
  zenith log              Show last 50 events
  zenith log -n 100       Show last 100 events
  zenith log -f           Stream new events live (Ctrl-C to stop)
  zenith log --type SEARCH  Filter to search events only`,
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("could not determine home directory: %w", err)
		}
		logPath := filepath.Join(home, ".zenith", "zenith.log")
		return runLog(logPath)
	},
}

func runLog(logPath string) error {
	data, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		fmt.Println("No activity log found. Run zenith index or zenith watch first.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("could not read log: %w", err)
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	lines = filterLines(lines, logFlags.filter)
	lines = tailLines(lines, logFlags.n)

	for _, line := range lines {
		fmt.Println(line)
	}

	if !logFlags.follow {
		return nil
	}

	// Follow mode: poll for new lines every 200ms until Ctrl-C.
	info, _ := os.Stat(logPath)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}

	for {
		time.Sleep(200 * time.Millisecond)

		f, err := os.Open(logPath)
		if err != nil {
			continue
		}

		fi, err := f.Stat()
		if err != nil {
			f.Close()
			continue
		}
		if fi.Size() <= size {
			f.Close()
			continue
		}

		if _, err := f.Seek(size, 0); err != nil {
			f.Close()
			continue
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if logFlags.filter == "" || strings.Contains(strings.ToUpper(line), strings.ToUpper(logFlags.filter)) {
				fmt.Println(line)
			}
		}
		size = fi.Size()
		f.Close()
	}
}

func filterLines(lines []string, filter string) []string {
	if filter == "" {
		return lines
	}
	upper := strings.ToUpper(filter)
	var out []string
	for _, l := range lines {
		if strings.Contains(strings.ToUpper(l), upper) {
			out = append(out, l)
		}
	}
	return out
}

func tailLines(lines []string, n int) []string {
	if n <= 0 || len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

func init() {
	logCmd.Flags().IntVarP(&logFlags.n, "lines", "n", 50, "Number of lines to show")
	logCmd.Flags().BoolVarP(&logFlags.follow, "follow", "f", false, "Stream new events live")
	logCmd.Flags().StringVar(&logFlags.filter, "type", "", "Filter by event type (e.g. SEARCH, INDEXED)")
}
