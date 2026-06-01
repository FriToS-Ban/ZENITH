package main

// engine.go — shared engine construction used by index, search, watch, serve.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shramanb113/ZENITH/internal/activitylog"
	"github.com/shramanb113/ZENITH/internal/analysis"
	"github.com/shramanb113/ZENITH/internal/config"
	"github.com/shramanb113/ZENITH/internal/embedding"
	"github.com/shramanb113/ZENITH/internal/index"
	"github.com/shramanb113/ZENITH/internal/nerve"
	"github.com/shramanb113/ZENITH/internal/nervemanager"
	"github.com/shramanb113/ZENITH/internal/ranking"
	storage "github.com/shramanb113/ZENITH/internal/storage"
	"github.com/shramanb113/ZENITH/internal/storage/wal"
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

// defaultStorageConfig returns a storage config rooted at ~/.zenith/ so WAL,
// SSTable, and FST files land in a consistent location regardless of the
// working directory from which zenith is invoked.
func defaultStorageConfig() storage.EngineConfig {
	cfg := storage.DefaultEngineConfig()
	home, err := os.UserHomeDir()
	if err != nil {
		return cfg
	}
	base := filepath.Join(home, ".zenith")
	cfg.WALPath = filepath.Join(base, "data", "wal", "zenith.wal")
	cfg.WALConfig = wal.WALConfig{
		SyncMode: wal.SyncAlways,
		Dir:      filepath.Join(base, "data", "wal"),
	}
	cfg.SSTDir = filepath.Join(base, "data", "sst")
	cfg.FSTPath = filepath.Join(base, "data", "terms.fst")
	return cfg
}

// buildEngine constructs and optionally loads a ready-to-use index.Engine.
// Also opens the LSM storage engine and wires it as the FST term sink.
// Returns the engine, the activity logger, and a teardown function to call on process exit.
func buildEngine(load bool) (*index.Engine, *activitylog.Logger, func(), error) {
	appConfig := config.DefaultConfig()

	// ── Activity logger ───────────────────────────────────────────────────────
	alog := activitylog.Open()

	// ── Improvement 3: open storage and start embedder concurrently ───────────
	// The LSM WAL replay and the nerve sidecar start are completely independent.
	var (
		storageEng   *storage.Engine
		storageErr   error
		emb          embedding.Embedder
		embedderName string
	)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		storageEng, storageErr = storage.Open(defaultStorageConfig())
	}()
	go func() {
		defer wg.Done()
		emb, embedderName = resolveEmbedder(appConfig, alog)
	}()
	wg.Wait()

	if storageErr != nil {
		alog.Close()
		return nil, nil, nil, storageErr
	}

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
		} else {
			alog.Log("LOADED", cliFlags.dbPath)
		}
	}

	teardown := func() {
		if err := engine.Save(cliFlags.dbPath); err != nil {
			slog.Error("Failed to save index", "error", err)
		} else {
			alog.Log("SAVED", cliFlags.dbPath)
		}
		if err := storageEng.Close(); err != nil {
			slog.Error("Storage engine close failed", "error", err)
		}
		alog.Close()
	}

	return engine, alog, teardown, nil
}

// resolveEmbedder picks and constructs the appropriate Embedder based on
// cliFlags.embedder, printing status lines for auto-start operations.
// Returns the embedder and a short label for display.
func resolveEmbedder(appConfig *config.Config, alog *activitylog.Logger) (embedding.Embedder, string) {
	switch cliFlags.embedder {
	case "auto":
		return autoEmbedder(appConfig, alog)

	case "nerve":
		addr := cliFlags.nerveURL
		if addr == "" {
			addr = appConfig.NerveGRPCAddr
		}
		// Strip any http:// or https:// prefix — nerve is now a gRPC service.
		addr = strings.TrimPrefix(strings.TrimPrefix(addr, "https://"), "http://")
		nc, err := nerve.NewNerveClient(addr)
		if err != nil {
			return embedding.NewDeterministicEmbedder(384), "deterministic"
		}
		cached, err := embedding.NewCachingEmbedder(nc.Embedder(), 10_000)
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
func autoEmbedder(_ *config.Config, alog *activitylog.Logger) (embedding.Embedder, string) {
	nm := nervemanager.New()

	fmt.Fprintf(os.Stderr, "\n  %s  starting embedding service...\n", cyan("nerve"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	nerveAddr, status := nm.Start(ctx)

	switch status {
	case nervemanager.StatusReady:
		alog.Log("NERVE", fmt.Sprintf("ready (%s)", nerveAddr))
		printNerveStatus("nerve ready  "+dim("("+nerveAddr+")"), true)
		if nc, err := nerve.NewNerveClient(nerveAddr); err == nil {
			if cached, err := embedding.NewCachingEmbedder(nc.Embedder(), 10_000); err == nil {
				// Improvement 4: fire a background embed to trigger model loading
				// while the caller continues setup. First real request won't stall.
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
					defer cancel()
					if _, err := cached.Embed(ctx, "warmup"); err == nil {
						slog.Info("Nerve models warmed and ready")
					}
				}()
				return cached, "nerve"
			}
		}

	case nervemanager.StatusNoPython:
		alog.Log("NERVE", "failed: python3 not found")
		printNerveStatus("python3 not found — trying Ollama", false)

	case nervemanager.StatusDepsFailed:
		alog.Log("NERVE", "failed: pip install failed — see ~/.zenith/nerve/nerve.log")
		printNerveStatus("pip install failed — check ~/.zenith/nerve/nerve.log", false)

	case nervemanager.StatusLaunchFailed:
		alog.Log("NERVE", "failed: launch failed — see ~/.zenith/nerve/nerve.log")
		printNerveStatus("nerve failed to start — check ~/.zenith/nerve/nerve.log", false)

	case nervemanager.StatusTimeout:
		alog.Log("NERVE", "timeout")
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
