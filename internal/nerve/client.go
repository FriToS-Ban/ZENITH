package nerve

import (
	"context"
	"fmt"

	nervepb "github.com/shramanb113/ZENITH/gen/go/nervepb"
	"github.com/shramanb113/ZENITH/internal/embedding"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// NerveClient is the single entry point for all Nerve sidecar interaction.
type NerveClient struct {
	conn *grpc.ClientConn
	stub nervepb.NerveServiceClient
}

// NewNerveClient dials the Nerve gRPC sidecar at addr (e.g. "localhost:8000").
func NewNerveClient(addr string) (*NerveClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("nerve: dial %s: %w", addr, err)
	}
	return &NerveClient{conn: conn, stub: nervepb.NewNerveServiceClient(conn)}, nil
}

// Close releases the underlying gRPC connection.
func (c *NerveClient) Close() error {
	return c.conn.Close()
}

// Ping checks reachability by sending a minimal Embed request.
func (c *NerveClient) Ping(ctx context.Context) error {
	_, err := c.stub.Embed(ctx, &nervepb.EmbedRequest{Text: "ping"})
	return err
}

// Embedder returns an embedding.Embedder backed by the Nerve gRPC service.
func (c *NerveClient) Embedder() embedding.Embedder {
	return &grpcEmbedder{stub: c.stub}
}

// ExtractPDF calls the sidecar to extract and embed all chunks from the PDF at filePath.
func (c *NerveClient) ExtractPDF(ctx context.Context, docID, filePath string) ([]Chunk, error) {
	resp, err := c.stub.ExtractPDF(ctx, &nervepb.ExtractPDFRequest{
		DocumentId: docID,
		FilePath:   filePath,
	})
	if err != nil {
		return nil, fmt.Errorf("nerve: ExtractPDF %s: %w", filePath, err)
	}
	chunks := make([]Chunk, len(resp.Chunks))
	for i, ch := range resp.Chunks {
		chunks[i] = Chunk{
			Text:       ch.Text,
			Page:       int(ch.PageNumber),
			ChunkIndex: int(ch.ChunkIndex),
			SourceType: ch.SourceType,
			Embedding:  ch.Embedding,
			BboxX:      ch.BboxX,
			BboxY:      ch.BboxY,
			BboxW:      ch.BboxW,
			BboxH:      ch.BboxH,
		}
	}
	return chunks, nil
}
