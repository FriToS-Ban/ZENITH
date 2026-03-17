package embedding

import "context"

type DeterministicEmbedder struct {
	dims int
}

func NewDeterministicEmbedder(dims int) *DeterministicEmbedder {
	return &DeterministicEmbedder{dims: dims}
}

func (d *DeterministicEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	// Fallback implementation logic goes here
	return make([]float32, d.dims), nil
}

func (d *DeterministicEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	var results [][]float32
	for _, text := range texts {
		emb, _ := d.Embed(ctx, text)
		results = append(results, emb)
	}
	return results, nil
}

func (d *DeterministicEmbedder) Dimensions() int {
	return d.dims
}
