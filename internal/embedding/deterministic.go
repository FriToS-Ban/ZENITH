package embedding

import (
	"context"
	"hash/fnv"
	"math"
)

type DeterministicEmbedder struct {
	dims int
}

func NewDeterministicEmbedder(dims int) *DeterministicEmbedder {
	return &DeterministicEmbedder{dims: dims}
}

// Embed produces a stable, text-dependent unit vector via FNV-64a seeded xorshift.
// Different texts produce different vectors so lexical-fallback mode still ranks results.
func (d *DeterministicEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	h := fnv.New64a()
	h.Write([]byte(text))
	seed := h.Sum64()

	result := make([]float32, d.dims)
	var sumSq float64
	for i := range result {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		v := float32(int64(seed)) / float32(1<<63)
		result[i] = v
		sumSq += float64(v) * float64(v)
	}
	if sumSq > 0 {
		norm := float32(math.Sqrt(sumSq))
		for i := range result {
			result[i] /= norm
		}
	}
	return result, nil
}

func (d *DeterministicEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		emb, _ := d.Embed(context.Background(), text)
		results[i] = emb
	}
	return results, nil
}

func (d *DeterministicEmbedder) Dimensions() int {
	return d.dims
}
