package nerve

import (
	"context"
	"fmt"

	nervepb "github.com/shramanb113/ZENITH/gen/go/nervepb"
)

type grpcEmbedder struct {
	stub nervepb.NerveServiceClient
}

func (e *grpcEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := e.stub.Embed(ctx, &nervepb.EmbedRequest{Text: text})
	if err != nil {
		return nil, fmt.Errorf("nerve: Embed: %w", err)
	}
	return resp.Embedding, nil
}

func (e *grpcEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	resp, err := e.stub.EmbedBatch(ctx, &nervepb.BatchEmbedRequest{Texts: texts})
	if err != nil {
		return nil, fmt.Errorf("nerve: EmbedBatch: %w", err)
	}
	result := make([][]float32, len(resp.Embeddings))
	for i, v := range resp.Embeddings {
		result[i] = v.Elements
	}
	return result, nil
}

func (e *grpcEmbedder) Dimensions() int {
	return 384
}
