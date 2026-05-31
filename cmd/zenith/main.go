package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev" // overridden by goreleaser via -ldflags

var rootCmd = &cobra.Command{
	Use:   "zenith",
	Short: "Local-first semantic search engine",
	Long: `ZENITH — local-first hybrid search engine written from scratch in Go.

Engine architecture:
  [Crawler] → [Analyzer] → [Embedder] → [LSM Storage]
  (fsnotify)   (FST+BKTree) (Ollama/OpenAI) (WAL→MemTable→SSTable)

Hybrid ranking:
  Lexical  BM25 + TF-IDF + Porter stemming + edge n-grams
  Fuzzy    BK-tree Levenshtein (O(log n))
  Semantic vector cosine via local Ollama embeddings
  Fusion   Reciprocal Rank Fusion (RRF)`,
}

func main() {
	rootCmd.AddCommand(indexCmd, searchCmd, watchCmd, serveCmd, versionCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
