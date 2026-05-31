package main

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/shramanb113/ZENITH/internal/crawler"
	"github.com/spf13/cobra"
)

var indexCmd = &cobra.Command{
	Use:   "index <directory>",
	Short: "Bulk-index all supported files in a directory",
	Long: `Recursively walks <directory>, extracts text from each supported file,
and adds it to the local index (zenith.db). Running index twice on the same
directory is safe — documents are re-indexed idempotently.

Supported formats:
  .txt .md .log .csv .json .yaml .yml  — raw text
  .go                                  — AST-extracted identifiers + comments
  .py .ts .js .jsx .tsx .rs .java .c   — raw source
  .html .htm                           — tag-stripped visible text`,

	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		setupLogger()
		dir := args[0]

		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return fmt.Errorf("not a directory: %s", dir)
		}

		printHeader("index", dir)

		engine, teardown, err := buildEngine(true)
		if err != nil {
			return fmt.Errorf("engine init: %w", err)
		}
		defer teardown()

		// Wrap the indexer to count files as they are processed.
		var count atomic.Int64
		counted := &countingIndexer{inner: engine, n: &count}

		w, err := crawler.NewWatcher(counted)
		if err != nil {
			return err
		}
		defer w.Close()

		start := time.Now()
		ctx := context.Background()
		if err := w.IndexDir(ctx, dir); err != nil {
			return fmt.Errorf("index: %w", err)
		}

		elapsed := time.Since(start)
		n := count.Load()

		printDivider()
		printFooter(
			fmt.Sprintf("%d file%s indexed", n, plural(n)),
			elapsed.Round(time.Millisecond).String(),
		)
		return nil
	},
}

func init() {
	addEngineFlags(indexCmd)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

type countingIndexer struct {
	inner interface {
		Add(ctx context.Context, id, text string) error
	}
	n *atomic.Int64
}

func (c *countingIndexer) Add(ctx context.Context, id, text string) error {
	err := c.inner.Add(ctx, id, text)
	if err == nil {
		c.n.Add(1)
	}
	return err
}

func plural(n int64) string {
	if n == 1 {
		return ""
	}
	return "s"
}
