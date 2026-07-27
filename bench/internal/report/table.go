package report

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EngineResult holds all measured numbers for one engine.
type EngineResult struct {
	Name         string
	Recall10     float64
	P50, P95, P99 time.Duration
	IndexTime    time.Duration // total time to index all docs
	HeapDeltaMB  float64
	Skipped      bool   // true when engine was excluded via --skip
	SkipReason   string
}

// Print writes the comparison table to stdout and saves a markdown copy to
// outDir/YYYY-MM-DD-<scale>.md.
func Print(results []EngineResult, scale, sysInfo, outDir string) {
	var sb strings.Builder
	writeTable(&sb, results, scale, sysInfo)
	fmt.Print(sb.String())

	if err := os.MkdirAll(outDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "report: could not create results dir: %v\n", err)
		return
	}
	fname := filepath.Join(outDir, time.Now().Format("2006-01-02")+"-"+scale+".md")
	if err := os.WriteFile(fname, []byte(sb.String()), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "report: could not save markdown: %v\n", err)
		return
	}
	fmt.Printf("\nResults saved to %s\n", fname)
}

func writeTable(w io.Writer, results []EngineResult, scale, sysInfo string) {
	active := make([]EngineResult, 0, len(results))
	for _, r := range results {
		if !r.Skipped {
			active = append(active, r)
		}
	}

	sep := strings.Repeat("─", 80)
	fmt.Fprintf(w, "\n%s\n", sep)
	fmt.Fprintf(w, "ZENITH Embeddable Benchmark — MS MARCO dev (%s passages, 6,980 queries)\n", scale)
	fmt.Fprintf(w, "Hardware: %s\n", sysInfo)
	fmt.Fprintf(w, "%s\n\n", sep)

	// Header
	fmt.Fprintf(w, "%-22s", "")
	for _, r := range active {
		fmt.Fprintf(w, "%-18s", r.Name)
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%-22s%s\n", "", strings.Repeat("─", 18*len(active)))

	// Rows
	rows := []struct {
		label string
		val   func(EngineResult) string
	}{
		{"Recall@10", func(r EngineResult) string { return fmt.Sprintf("%.3f", r.Recall10) }},
		{"Query p50", func(r EngineResult) string { return fmtDur(r.P50) }},
		{"Query p95", func(r EngineResult) string { return fmtDur(r.P95) }},
		{"Query p99", func(r EngineResult) string { return fmtDur(r.P99) }},
		{"Index all docs", func(r EngineResult) string { return fmtDur(r.IndexTime) }},
		{"Memory (heap)", func(r EngineResult) string { return fmt.Sprintf("%.0f MB", r.HeapDeltaMB) }},
	}

	for _, row := range rows {
		fmt.Fprintf(w, "%-22s", row.label)
		for _, r := range active {
			fmt.Fprintf(w, "%-18s", row.val(r))
		}
		fmt.Fprintln(w)
	}

	// Notes
	fmt.Fprintf(w, "\n%s\n", sep)
	fmt.Fprintln(w, "Notes:")
	fmt.Fprintln(w, "  ZENITH hybrid: BM25 + vector semantic + fuzzy + phonetic (ONNX or deterministic fallback).")
	fmt.Fprintln(w, "  ZENITH BM25: pure lexical — directly comparable to SQLite FTS5 and Bleve.")
	fmt.Fprintln(w, "  SQLite FTS5 via mattn/go-sqlite3 (CGo — same bar as ZENITH hybrid ONNX).")
	fmt.Fprintln(w, "  Bleve: BM25 + Levenshtein fuzzy, pure Go, no CGo required.")
	fmt.Fprintln(w, "  All engines run in-process. No HTTP, no Docker.")
	fmt.Fprintln(w, "  Recall@10: queries whose relevant passage exists in the indexed subset only.")
	fmt.Fprintln(w, "  Memory: heap allocation delta during indexing (runtime.ReadMemStats).")

	for _, r := range results {
		if r.Skipped {
			fmt.Fprintf(w, "  %s skipped: %s\n", r.Name, r.SkipReason)
		}
	}
	fmt.Fprintf(w, "%s\n", sep)
}

func fmtDur(d time.Duration) string {
	switch {
	case d < time.Microsecond:
		return fmt.Sprintf("%dns", d.Nanoseconds())
	case d < time.Millisecond:
		return fmt.Sprintf("%.1fµs", float64(d.Microseconds()))
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d.Milliseconds()))
	default:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
}
