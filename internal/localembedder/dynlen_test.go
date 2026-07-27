//go:build cgo

package localembedder

import (
	"context"
	"testing"
)

// TestEmbed_PaddingInvariance verifies that the same text produces the same
// vector regardless of how much padding the batch forces onto it. A short
// word embedded alone (seqLen 8) must match the same word embedded inside a
// batch padded to a long passage's length.
func TestEmbed_PaddingInvariance(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	single, err := e.Embed(ctx, "dog")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	batch, err := e.EmbedBatch(ctx, []string{"dog", passage})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}

	var dot float64
	for i := range single {
		dot += float64(single[i]) * float64(batch[0][i])
	}
	// The int8 dynamically-quantized model computes activation scales over
	// whole tensors including padded positions, so outputs vary ~1% with
	// padding length. A genuine attention-mask bug would drop similarity to
	// 0.3–0.7; quantization noise stays above 0.98.
	if dot < 0.98 {
		t.Fatalf("padding changed the embedding: cosine similarity %.6f, want >= 0.98", dot)
	}
}

// TestEmbed_SemanticSanity checks the model still produces meaningful
// similarities after the dynamic-length change.
func TestEmbed_SemanticSanity(t *testing.T) {
	e, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	cos := func(a, b []float32) float64 {
		var dot float64
		for i := range a {
			dot += float64(a[i]) * float64(b[i])
		}
		return dot
	}

	dog, _ := e.Embed(ctx, "a happy dog playing fetch")
	puppy, _ := e.Embed(ctx, "a joyful puppy chasing a ball")
	tax, _ := e.Embed(ctx, "quarterly corporate tax filing deadlines")

	if cos(dog, puppy) <= cos(dog, tax) {
		t.Fatalf("semantic ordering broken: sim(dog,puppy)=%.4f <= sim(dog,tax)=%.4f",
			cos(dog, puppy), cos(dog, tax))
	}
}
