package engines

import "context"

// Engine is the common interface implemented by every search backend under test.
// All runners must be safe for sequential use (the benchmark never calls methods
// concurrently on the same Engine).
type Engine interface {
	// Name returns a short display label for the output table.
	Name() string

	// Index adds a single document. Used for single-write latency measurement.
	Index(ctx context.Context, id, text string) error

	// IndexBatch adds all documents in one call. Used for bulk-indexing benchmarks.
	// The implementation should use the most efficient batch path available.
	IndexBatch(ctx context.Context, docs map[string]string) error

	// Search returns up to topK document IDs ranked by relevance.
	Search(ctx context.Context, query string, topK int) ([]string, error)

	// Close releases all resources held by the engine.
	Close() error
}
