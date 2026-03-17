package embedding

import (
	"context"

	lru "github.com/hashicorp/golang-lru/v2"
)

type CachingEmbedder struct {
	base  Embedder
	cache *lru.Cache[string, []float32]
}

func NewCachingEmbedder(base Embedder, maxSize int) (*CachingEmbedder, error) {
	c, err := lru.New[string, []float32](maxSize)
	if err != nil {
		return nil, err
	}
	return &CachingEmbedder{
		base:  base,
		cache: c,
	}, nil
}

func (c *CachingEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if val, ok := c.cache.Get(text); ok {
		return val, nil
	}

	vec, err := c.base.Embed(ctx, text)
	if err == nil {
		c.cache.Add(text, vec)
	}
	return vec, err
}

func (c *CachingEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	var missingIdx []int
	var missingTexts []string

	for i, txt := range texts {
		if val, ok := c.cache.Get(txt); ok {
			results[i] = val
		} else {
			missingIdx = append(missingIdx, i)
			missingTexts = append(missingTexts, txt)
		}
	}

	if len(missingTexts) > 0 {
		missingVecs, err := c.base.EmbedBatch(ctx, missingTexts)
		if err != nil {
			return nil, err
		}
		for i, vec := range missingVecs {
			ogIdx := missingIdx[i]
			results[ogIdx] = vec
			c.cache.Add(missingTexts[i], vec)
		}
	}

	return results, nil
}

func (c *CachingEmbedder) Dimensions() int {
	return c.base.Dimensions()
}
