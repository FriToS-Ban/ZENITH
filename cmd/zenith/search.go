package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

var searchFlags struct {
	maxResults int
}

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Query the local index",
	Long: `Loads the local index from zenith.db and runs a hybrid search query.
No server needed — the engine runs in-process.

The query goes through the full pipeline:
  1. Lexical scoring  (BM25 + edge n-grams + phonetic)
  2. Fuzzy matching   (BK-tree Levenshtein)
  3. Semantic scoring (vector cosine via embedder)
  4. RRF fusion

Results are printed to stdout, ranked by score.`,

	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		setupLogger()

		// Join all args as the query so `zenith search hello world` works.
		query := ""
		for i, a := range args {
			if i > 0 {
				query += " "
			}
			query += a
		}

		engine, teardown, err := buildEngine(true)
		if err != nil {
			return fmt.Errorf("engine init: %w", err)
		}
		// Don't save on search — read-only operation.
		_ = teardown

		results, err := engine.Search(context.Background(), query)
		if err != nil {
			return fmt.Errorf("search: %w", err)
		}

		if len(results) == 0 {
			fmt.Println("No results.")
			return nil
		}

		max := searchFlags.maxResults
		if max <= 0 || max > len(results) {
			max = len(results)
		}

		fmt.Printf("Results for %q:\n\n", query)
		for i, r := range results[:max] {
			fmt.Printf("  %2d. %-60s  score=%.5f\n", i+1, r.ID, r.Score)
		}
		return nil
	},
}

func init() {
	addEngineFlags(searchCmd)
	searchCmd.Flags().IntVarP(&searchFlags.maxResults, "max", "n", 10, "Maximum results to display")
}
