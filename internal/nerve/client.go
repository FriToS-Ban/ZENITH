package nerve

import (
	"context"
	"fmt"
	"io"

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

// ExtractImage calls the sidecar to caption and embed a standalone image file.
func (c *NerveClient) ExtractImage(ctx context.Context, docID, filePath string) (string, []float32, error) {
	resp, err := c.stub.ExtractImage(ctx, &nervepb.ExtractImageRequest{
		DocumentId: docID,
		FilePath:   filePath,
	})
	if err != nil {
		return "", nil, fmt.Errorf("nerve: ExtractImage %s: %w", filePath, err)
	}
	return resp.Caption, resp.Embedding, nil
}

// ExtractPDF streams chunks from the sidecar as Python processes each page batch.
// Chunks arrive progressively — Go buffers them and calls AddBatch once the
// stream is exhausted, giving a single FST rebuild for the whole document.
func (c *NerveClient) ExtractPDF(ctx context.Context, docID, filePath string) ([]Chunk, error) {
	stream, err := c.stub.ExtractPDF(ctx, &nervepb.ExtractPDFRequest{
		DocumentId: docID,
		FilePath:   filePath,
	})
	if err != nil {
		return nil, fmt.Errorf("nerve: ExtractPDF %s: %w", filePath, err)
	}

	var chunks []Chunk
	for {
		ch, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("nerve: ExtractPDF stream %s: %w", filePath, err)
		}
		chunks = append(chunks, Chunk{
			Text:       ch.Text,
			Page:       int(ch.PageNumber),
			ChunkIndex: int(ch.ChunkIndex),
			SourceType: ch.SourceType,
			Embedding:  ch.Embedding,
			BboxX:      ch.BboxX,
			BboxY:      ch.BboxY,
			BboxW:      ch.BboxW,
			BboxH:      ch.BboxH,
		})
	}
	return chunks, nil
}
