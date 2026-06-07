package engines

import (
	"context"
	"fmt"

	"github.com/shramanb113/ZENITH/pkg/zenith"
)

// ZenithEngine wraps pkg/zenith. mode is "hybrid" or "bm25only".
//
// hybrid    — full BM25 + fuzzy + phonetic + vector search. Uses the local
//             ONNX embedder when available; falls back to the deterministic
//             hash-based embedder with a printed warning when it is not.
// bm25only  — pure lexical (BM25 + fuzzy + phonetic, no vectors). No CGo
//             required. Directly comparable to SQLite FTS5 and Bleve on the
//             lexical dimension.
type ZenithEngine struct {
	db   *zenith.DB
	mode string
}

// NewZenithHybrid opens an in-memory ZENITH index in full hybrid mode.
func NewZenithHybrid() (*ZenithEngine, error) {
	db, err := zenith.Open(":memory:")
	if err != nil {
		return nil, fmt.Errorf("zenith hybrid open: %w", err)
	}
	return &ZenithEngine{db: db, mode: "hybrid"}, nil
}

// NewZenithBM25Only opens an in-memory ZENITH index in lexical-only mode.
func NewZenithBM25Only() (*ZenithEngine, error) {
	db, err := zenith.Open(":memory:", zenith.WithBM25Only())
	if err != nil {
		return nil, fmt.Errorf("zenith bm25only open: %w", err)
	}
	return &ZenithEngine{db: db, mode: "bm25only"}, nil
}

func (e *ZenithEngine) Name() string {
	if e.mode == "hybrid" {
		return "ZENITH hybrid"
	}
	return "ZENITH BM25"
}

func (e *ZenithEngine) Index(ctx context.Context, id, text string) error {
	return e.db.Add(ctx, id, text)
}

func (e *ZenithEngine) IndexBatch(ctx context.Context, docs map[string]string) error {
	return e.db.AddBatch(ctx, docs)
}

func (e *ZenithEngine) Search(ctx context.Context, query string, topK int) ([]string, error) {
	results, err := e.db.Search(ctx, query, zenith.Limit(topK))
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.ID
	}
	return ids, nil
}

func (e *ZenithEngine) Close() error {
	return e.db.Close()
}
