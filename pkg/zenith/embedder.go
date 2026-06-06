package zenith

import "context"

// Embedder computes vector embeddings for documents and queries.
// Implement this interface to plug in a custom embedding model via WithEmbedder.
// All three methods must be safe for concurrent use.
type Embedder interface {
	// Embed returns a vector for a single text string.
	Embed(ctx context.Context, text string) ([]float32, error)
	// EmbedBatch returns one vector per element of texts, in the same order.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	// Dimensions returns the length of each vector produced by this embedder.
	Dimensions() int
}
