package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ─── terminal detection ───────────────────────────────────────────────────────

var colorEnabled = func() bool {
	stat, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}()

// ─── ANSI helpers ─────────────────────────────────────────────────────────────

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiCyan   = "\033[36m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
)

func applyCode(s, code string) string {
	if !colorEnabled {
		return s
	}
	return code + s + ansiReset
}

func bold(s string) string   { return applyCode(s, ansiBold) }
func dim(s string) string    { return applyCode(s, ansiDim) }
func cyan(s string) string   { return applyCode(s, ansiCyan) }
func green(s string) string  { return applyCode(s, ansiGreen) }
func yellow(s string) string { return applyCode(s, ansiYellow) }

// ─── layout helpers ───────────────────────────────────────────────────────────

// printHeader prints the command verb and its subject on one line.
//
//	  index  ~/Documents
func printHeader(command, subject string) {
	fmt.Printf("\n  %s  %s\n\n", bold(cyan(command)), subject)
}

// printDivider prints a faint rule.
func printDivider() {
	fmt.Println(dim("  " + strings.Repeat("─", 66)))
}

// printFooter prints a dim summary line with · separators.
//
//	  42 files · nerve · 1.2s
func printFooter(parts ...string) {
	joined := strings.Join(parts, dim(" · "))
	fmt.Printf("\n  %s\n\n", dim(joined))
}

// printResult prints one search result row.
//
//	   1  readme.md            ~/Documents/readme.md    0.953
func printResult(rank int, id string, score float64) {
	name := filepath.Base(id)
	display := shortenPath(id)

	rankStr := bold(fmt.Sprintf("%2d", rank))
	nameStr := cyan(fmt.Sprintf("%-22s", name))
	pathStr := dim(fmt.Sprintf("%-55s", display))
	scoreStr := bold(fmt.Sprintf("%.3f", score))

	fmt.Printf("  %s  %s  %s  %s\n", rankStr, nameStr, pathStr, scoreStr)
}

// printNerveStatus prints a single nerve startup line.
func printNerveStatus(msg string, ok bool) {
	icon := green("✓")
	if !ok {
		icon = yellow("!")
	}
	fmt.Printf("  %s  %s\n", icon, dim(msg))
}

// ─── utilities ────────────────────────────────────────────────────────────────

func shortenPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			p = "~/" + rel
		}
	}
	if len(p) > 55 {
		p = "…" + p[len(p)-54:]
	}
	return p
}
