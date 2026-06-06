package zenith

import "time"

type options struct {
	embedder           Embedder
	bm25Only           bool
	cacheSize          int
	fuzzyDistance      int
	limit              int
	checkpointInterval time.Duration
}

func defaultOptions() *options {
	return &options{
		cacheSize:     10_000,
		fuzzyDistance: 2,
		limit:         10,
	}
}

// Option configures a DB at Open time.
type Option func(*options) error

// SearchOption configures a single Search call without changing DB state.
type SearchOption func(*searchOptions)

type searchOptions struct {
	limit int
}

// WithEmbedder replaces the default embedded ONNX embedder with a custom one.
func WithEmbedder(e Embedder) Option {
	return func(o *options) error {
		if e == nil {
			return ErrInvalidOption
		}
		o.embedder = e
		return nil
	}
}

// WithBM25Only disables vector search, eliminating the CGo build dependency.
func WithBM25Only() Option {
	return func(o *options) error {
		o.bm25Only = true
		return nil
	}
}

// WithCacheSize sets the LRU embedding cache size. Default: 10,000.
// Pass 0 to disable caching.
func WithCacheSize(n int) Option {
	return func(o *options) error {
		if n < 0 {
			return ErrInvalidOption
		}
		o.cacheSize = n
		return nil
	}
}

// WithFuzzyDistance sets the BK-tree edit distance threshold. Default: 2.
// Clamped to [0, 5] — values above 5 produce O(n) BKTree scans.
func WithFuzzyDistance(n int) Option {
	return func(o *options) error {
		if n < 0 {
			return ErrInvalidOption
		}
		if n > 5 {
			n = 5
		}
		o.fuzzyDistance = n
		return nil
	}
}

// WithLimit sets the default maximum results returned by Search. Default: 10.
func WithLimit(n int) Option {
	return func(o *options) error {
		if n <= 0 {
			return ErrInvalidOption
		}
		o.limit = n
		return nil
	}
}

// Limit overrides the result limit for a single Search call.
func Limit(n int) SearchOption {
	return func(o *searchOptions) {
		if n > 0 {
			o.limit = n
		}
	}
}

// WithCheckpointInterval sets how often the DB automatically saves a gob
// snapshot and resets the WAL in a background goroutine. Default: 0 (disabled).
// This bounds WAL growth so crash recovery only needs to replay a small delta.
// Minimum enforced: 10 seconds. Has no effect on :memory: databases.
func WithCheckpointInterval(d time.Duration) Option {
	return func(o *options) error {
		const minInterval = 10 * time.Second
		if d > 0 && d < minInterval {
			d = minInterval
		}
		o.checkpointInterval = d
		return nil
	}
}
