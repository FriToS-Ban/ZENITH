//go:build cgo

package localembedder

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// passage simulates a typical MS MARCO passage (~70 words).
const passage = "The presence of communication amid scientific minds was equally important to " +
	"the success of the Manhattan Project as scientific intellect was. The only cloud " +
	"hanging over the impressive achievement of the atomic researchers and engineers " +
	"is what their success truly meant; hundreds of thousands of innocent lives " +
	"obliterated. The project was a research and development undertaking during World " +
	"War II that produced the first nuclear weapons."

var words = func() []string {
	out := make([]string, 0, 512)
	for i := 0; i < 512; i++ {
		out = append(out, fmt.Sprintf("vocabword%d", i))
	}
	return out
}()

func newBenchEmbedder(b *testing.B) *Embedder {
	b.Helper()
	e, err := New()
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	return e
}

func BenchmarkEmbedSinglePassage(b *testing.B) {
	e := newBenchEmbedder(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Embed(ctx, passage); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEmbedBatch512Words(b *testing.B) {
	e := newBenchEmbedder(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.EmbedBatch(ctx, words); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEmbedBatch128Passages(b *testing.B) {
	e := newBenchEmbedder(b)
	ctx := context.Background()
	docs := make([]string, 128)
	for i := range docs {
		docs[i] = passage + " " + strings.Repeat("extra context words ", i%5)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.EmbedBatch(ctx, docs); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEmbedConcurrent4x64 measures whether concurrent ONNX inference
// scales: 4 goroutines, each with its own session, each embedding a
// 64-passage batch (256 passages total per iteration).
func BenchmarkEmbedConcurrent4x64(b *testing.B) {
	embedders := make([]*Embedder, 4)
	for i := range embedders {
		embedders[i] = newBenchEmbedder(b)
	}
	ctx := context.Background()
	docs := make([]string, 64)
	for i := range docs {
		docs[i] = passage + " " + strings.Repeat("extra context words ", i%5)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		for _, e := range embedders {
			wg.Add(1)
			go func(e *Embedder) {
				defer wg.Done()
				if _, err := e.EmbedBatch(ctx, docs); err != nil {
					b.Error(err)
				}
			}(e)
		}
		wg.Wait()
	}
}

func BenchmarkEmbedBatch64Passages(b *testing.B) {
	e := newBenchEmbedder(b)
	ctx := context.Background()
	docs := make([]string, 64)
	for i := range docs {
		docs[i] = passage + " " + strings.Repeat("extra context words ", i%5)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.EmbedBatch(ctx, docs); err != nil {
			b.Fatal(err)
		}
	}
}
