package image

import (
	"context"
	"fmt"

	"github.com/shramanb113/ZENITH/internal/activitylog"
	"github.com/shramanb113/ZENITH/internal/index"
	"github.com/shramanb113/ZENITH/internal/nerve"
)

// Indexer captions and indexes a standalone image file through the Nerve sidecar.
type Indexer struct {
	nerve  *nerve.NerveClient
	engine *index.Engine
	logger *activitylog.Logger
}

// NewIndexer creates an Indexer wired to the given sidecar client and engine.
func NewIndexer(n *nerve.NerveClient, e *index.Engine, logger ...*activitylog.Logger) *Indexer {
	var l *activitylog.Logger
	if len(logger) > 0 && logger[0] != nil {
		l = logger[0]
	} else {
		l = activitylog.Noop()
	}
	return &Indexer{nerve: n, engine: e, logger: l}
}

// Index generates a BLIP caption, embeds it, and stores the result.
// Returns 1 on success. An empty caption from the sidecar is treated as a no-op.
func (idx *Indexer) Index(ctx context.Context, docID, filePath string) (int, error) {
	caption, vec, err := idx.nerve.ExtractImage(ctx, docID, filePath)
	if err != nil {
		return 0, fmt.Errorf("image: extract %s: %w", filePath, err)
	}
	if caption == "" {
		return 0, nil
	}
	if err := idx.engine.AddWithVector(ctx, docID, caption, vec); err != nil {
		return 0, fmt.Errorf("image: index %s: %w", docID, err)
	}
	idx.logger.Log("IMAGE", fmt.Sprintf("%s → caption indexed", docID))
	return 1, nil
}
