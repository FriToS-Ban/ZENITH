package engines

import (
	"context"
	"fmt"

	"github.com/blevesearch/bleve/v2"
)

// BleveEngine runs queries against an in-memory Bleve index.
// Bleve provides BM25 + Levenshtein fuzzy search, pure Go, no CGo required.
type BleveEngine struct {
	idx bleve.Index
}

func NewBleveEngine() (*BleveEngine, error) {
	mapping := bleve.NewIndexMapping()
	idx, err := bleve.NewMemOnly(mapping)
	if err != nil {
		return nil, fmt.Errorf("bleve: new index: %w", err)
	}
	return &BleveEngine{idx: idx}, nil
}

func (e *BleveEngine) Name() string { return "Bleve" }

func (e *BleveEngine) Index(_ context.Context, id, text string) error {
	return e.idx.Index(id, struct{ Body string }{Body: text})
}

func (e *BleveEngine) IndexBatch(_ context.Context, docs map[string]string) error {
	batch := e.idx.NewBatch()
	for id, text := range docs {
		if err := batch.Index(id, struct{ Body string }{Body: text}); err != nil {
			return fmt.Errorf("bleve: batch index %q: %w", id, err)
		}
	}
	return e.idx.Batch(batch)
}

func (e *BleveEngine) Search(_ context.Context, query string, topK int) ([]string, error) {
	req := bleve.NewSearchRequest(bleve.NewMatchQuery(query))
	req.Size = topK
	req.Fields = []string{}

	res, err := e.idx.Search(req)
	if err != nil {
		return nil, fmt.Errorf("bleve: search: %w", err)
	}

	ids := make([]string, len(res.Hits))
	for i, hit := range res.Hits {
		ids[i] = hit.ID
	}
	return ids, nil
}

func (e *BleveEngine) Close() error { return e.idx.Close() }
