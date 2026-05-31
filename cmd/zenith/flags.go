package main

import "github.com/spf13/cobra"

// addEngineFlags attaches the common engine-configuration flags to cmd.
// All commands that create a zenith engine (index, search, watch, serve)
// call this in their init().
func addEngineFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cliFlags.dbPath, "db", "zenith.db", "Path to the index database file")
	cmd.Flags().StringVar(&cliFlags.fstPath, "fst", "./data/index.fst", "Path for the on-disk FST file (memory-mapped, not in RAM)")
	cmd.Flags().StringVar(&cliFlags.embedder, "embedder", "deterministic",
		`Embedder to use: ollama | nerve | deterministic
  ollama        Local Ollama (recommended, needs: ollama serve)
  nerve         Custom HTTP embedding service (--nerve-url)
  deterministic Hash-based fallback, no external service needed`)
	cmd.Flags().StringVar(&cliFlags.ollamaURL, "ollama-url", "http://localhost:11434", "Ollama server URL")
	cmd.Flags().StringVar(&cliFlags.ollamaModel, "ollama-model", "nomic-embed-text", "Ollama embedding model")
	cmd.Flags().StringVar(&cliFlags.nerveURL, "nerve-url", "http://localhost:8000", "Nerve embedding service URL")
}
