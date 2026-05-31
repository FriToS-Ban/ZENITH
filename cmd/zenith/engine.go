package main

// engine.go — shared engine construction used by index, search, watch, serve.

import (
	"log/slog"
	"os"
	"time"

	"github.com/shramanb113/ZENITH/internal/analysis"
	"github.com/shramanb113/ZENITH/internal/config"
	"github.com/shramanb113/ZENITH/internal/embedding"
	"github.com/shramanb113/ZENITH/internal/index"
	"github.com/shramanb113/ZENITH/internal/ranking"
	storage "github.com/shramanb113/ZENITH/internal/storage"
)

// cliFlags holds the flag values shared across multiple commands.
var cliFlags struct {
	dbPath      string
	fstPath     string
	embedder    string // "ollama" | "openai" | "nerve" | "deterministic"
	ollamaURL   string
	ollamaModel string
	nerveURL    string
	openAIKey   string
}

// buildEngine constructs and loads a ready-to-use index.Engine.
// It also opens the storage engine and wires it as the FST term sink.
// Returns the engine and a teardown function (call on exit).
func buildEngine(load bool) (*index.Engine, func(), error) {
	appConfig := config.DefaultConfig()

	// ── Storage engine ────────────────────────────────────────────────────────
	storageEng, err := storage.Open(storage.DefaultEngineConfig())
	if err != nil {
		return nil, nil, err
	}

	// ── Embedder ──────────────────────────────────────────────────────────────
	var emb embedding.Embedder
	switch cliFlags.embedder {
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
		cached, err := embedding.NewCachingEmbedder(raw, 10000)
		if err != nil {
			storageEng.Close()
			return nil, nil, err
		}
		emb = cached

	case "nerve":
		nerveURL := cliFlags.nerveURL
		if nerveURL == "" {
			nerveURL = appConfig.NerveURL
		}
		raw := embedding.NewNeuralEmbedder(nerveURL, appConfig.NerveTimeout)
		cached, err := embedding.NewCachingEmbedder(raw, 10000)
		if err != nil {
			storageEng.Close()
			return nil, nil, err
		}
		emb = cached

	default: // "deterministic" or anything else
		emb = embedding.NewDeterministicEmbedder(384)
	}

	// ── Index engine ──────────────────────────────────────────────────────────
	tkz := analysis.NewStandardAnalyzer()
	scorer := ranking.NewRRFRanker(0, 0)
	engine := index.NewEngine(appConfig, emb, scorer, tkz)
	engine.SetFSTPath(cliFlags.fstPath)
	engine.SetTermStore(storageEng)

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

func setupLogger() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))
}
