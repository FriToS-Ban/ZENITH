package main

import (
	"context"
	"fmt"
	"os"

	"github.com/shramanb113/ZENITH/internal/crawler"
	"github.com/spf13/cobra"
)

var indexCmd = &cobra.Command{
	Use:   "index <directory>",
	Short: "Bulk-index all supported files in a directory",
	Long: `Recursively walks <directory>, extracts text from each supported file,
and adds it to the local index (zenith.db). Existing documents are re-indexed
idempotently — running index twice on the same directory is safe.

Supported formats:
  .txt .md .log .csv .json .yaml .yml  — raw text
  .go                                  — AST-extracted identifiers + comments
  .py .ts .js .jsx .tsx .rs .java      — raw source
  .html .htm                           — tag-stripped visible text`,

	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		setupLogger()
		dir := args[0]

		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return fmt.Errorf("not a directory: %s", dir)
		}

		engine, teardown, err := buildEngine(true)
		if err != nil {
			return fmt.Errorf("engine init: %w", err)
		}
		defer teardown()

		w, err := crawler.NewWatcher(engine)
		if err != nil {
			return err
		}
		defer w.Close()

		fmt.Printf("Indexing %s ...\n", dir)
		ctx := context.Background()
		if err := w.IndexDir(ctx, dir); err != nil {
			return fmt.Errorf("index: %w", err)
		}
		fmt.Println("Done.")
		return nil
	},
}

func init() {
	addEngineFlags(indexCmd)
}
