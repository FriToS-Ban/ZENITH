package main

// engine.go — shared engine construction used by index, search, watch, serve.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/shramanb113/ZENITH/internal/analysis"
	"github.com/shramanb113/ZENITH/internal/config"
	"github.com/shramanb113/ZENITH/internal/embedding"
	"github.com/shramanb113/ZENITH/internal/index"
	"github.com/shramanb113/ZENITH/internal/nervemanager"
	"github.com/shramanb113/ZENITH/internal/ranking"
	storage "github.com/shramanb113/ZENITH/internal/storage"
)

// cliFlags holds the flag values shared across all commands.
var cliFlags struct {
	dbPath      string
	fstPath     string
	embedder    string // "auto" | "nerve" | "ollama" | "deterministic"
	ollamaURL   string
	ollamaModel string
	nerveURL    string
}

// buildEngine constructs and optionally loads a ready-to-use index.Engine.
// Also opens the LSM storage engine and wires it as the FST term sink.
// Returns the engine and a teardown function to call on process exit.
func buildEngine(load bool) (*index.Engine, func(), error) {
	appConfig := config.DefaultConfig()

	// ── Storage engine ────────────────────────────────────────────────────────
	storageEng, err := storage.Open(storage.DefaultEngineConfig())
	if err != nil {
		return nil, nil, err
	}

	// ── Embedder ──────────────────────────────────────────────────────────────
	emb, embedderName := resolveEmbedder(appConfig)

	// ── Index engine ──────────────────────────────────────────────────────────
	tkz := analysis.NewStandardAnalyzer()
	scorer := ranking.NewRRFRanker(0, 0)
	engine := index.NewEngine(appConfig, emb, scorer, tkz)
	engine.SetFSTPath(cliFlags.fstPath)
	engine.SetTermStore(storageEng)

	_ = embedderName // surfaced by commands via printFooter

	if load {
		if err := engine.Load(cliFlags.dbPath); err != nil {
			slog.Info("No existing index, starting fresh.")
		}
	}

	teardown := func() {
		if err := engine.Save(cliFlags.dbPath); err != nil {
			slog.Error("Failed to save index", "error", err)
		}
		if err := storageEng.Close(); err != nil {
			slog.Error("Storage engine close failed", "error", err)
		}
	}

	return engine, teardown, nil
}

// resolveEmbedder picks and constructs the appropriate Embedder based on
// cliFlags.embedder, printing status lines for auto-start operations.
// Returns the embedder and a short label for display.
func resolveEmbedder(appConfig *config.Config) (embedding.Embedder, string) {
	switch cliFlags.embedder {
	case "auto":
		return autoEmbedder(appConfig)

	case "nerve":
		nerveURL := cliFlags.nerveURL
		if nerveURL == "" {
			nerveURL = appConfig.NerveURL
		}
		raw := embedding.NewNeuralEmbedder(nerveURL, 30*time.Second)
		cached, err := embedding.NewCachingEmbedder(raw, 10_000)
		if err != nil {
			return embedding.NewDeterministicEmbedder(384), "deterministic"
		}
		return cached, "nerve"

	case "ollama":
		base := cliFlags.ollamaURL
		if base == "" {
			base = "http://localhost:11434"
		}
		model := cliFlags.ollamaModel
		if model == "" {
			model = "nomic-embed-text"
		}
		raw := embedding.NewOllamaEmbedder(base, model, 30*time.Second)
		cached, err := embedding.NewCachingEmbedder(raw, 10_000)
		if err != nil {
			return embedding.NewDeterministicEmbedder(384), "deterministic"
		}
		return cached, "ollama"

	default: // "deterministic"
		return embedding.NewDeterministicEmbedder(384), "deterministic"
	}
}

// autoEmbedder implements the cascade: nerve → Ollama → deterministic.
// It starts nerve in the background and waits up to 20 s for it to be ready.
func autoEmbedder(_ *config.Config) (embedding.Embedder, string) {
	nm := nervemanager.New()

	fmt.Fprintf(os.Stderr, "\n  %s  starting embedding service...\n", cyan("nerve"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	nerveURL, status := nm.Start(ctx)

	switch status {
	case nervemanager.StatusReady:
		printNerveStatus("nerve ready  "+dim("("+nerveURL+")"), true)
		raw := embedding.NewNeuralEmbedder(nerveURL, 30*time.Second)
		if cached, err := embedding.NewCachingEmbedder(raw, 10_000); err == nil {
			return cached, "nerve"
		}

	case nervemanager.StatusNoPython:
		printNerveStatus("python3 not found — trying Ollama", false)

	case nervemanager.StatusDepsFailed:
		printNerveStatus("pip install failed — check ~/.zenith/nerve/nerve.log", false)

	case nervemanager.StatusLaunchFailed:
		printNerveStatus("nerve failed to start — check ~/.zenith/nerve/nerve.log", false)

	case nervemanager.StatusTimeout:
		printNerveStatus("nerve timed out — falling back", false)
	}

	// Try Ollama as second choice.
	if ollamaUp() {
		base := cliFlags.ollamaURL
		if base == "" {
			base = "http://localhost:11434"
		}
		model := cliFlags.ollamaModel
		if model == "" {
			model = "nomic-embed-text"
		}
		printNerveStatus("Ollama ready  "+dim("("+base+")"), true)
		raw := embedding.NewOllamaEmbedder(base, model, 30*time.Second)
		if cached, err := embedding.NewCachingEmbedder(raw, 10_000); err == nil {
			return cached, "ollama"
		}
	}

	// Last resort.
	printNerveStatus("using deterministic embeddings  "+dim("(no semantic search)"), false)
	return embedding.NewDeterministicEmbedder(384), "deterministic"
}

// ollamaUp returns true if a local Ollama instance is responding.
func ollamaUp() bool {
	base := cliFlags.ollamaURL
	if base == "" {
		base = "http://localhost:11434"
	}
	raw := embedding.NewOllamaEmbedder(base, "nomic-embed-text", 2*time.Second)
	_, err := raw.Embed(context.Background(), "ping")
	return err == nil
}

func setupLogger() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	})))
}
