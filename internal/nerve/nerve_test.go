package nerve_test

import (
	"context"
	"net"
	"testing"
	"time"

	nervepb "github.com/shramanb113/ZENITH/gen/go/nervepb"
	"github.com/shramanb113/ZENITH/internal/nerve"
	"google.golang.org/grpc"
)

type mockNerveService struct {
	nervepb.UnimplementedNerveServiceServer
}

func (m *mockNerveService) Embed(_ context.Context, req *nervepb.EmbedRequest) (*nervepb.EmbedResponse, error) {
	vec := make([]float32, 384)
	for i := range vec {
		vec[i] = 0.1
	}
	return &nervepb.EmbedResponse{Embedding: vec}, nil
}

func (m *mockNerveService) EmbedBatch(_ context.Context, req *nervepb.BatchEmbedRequest) (*nervepb.BatchEmbedResponse, error) {
	embeddings := make([]*nervepb.EmbedVector, len(req.Texts))
	for i := range req.Texts {
		vec := make([]float32, 384)
		for j := range vec {
			vec[j] = 0.1
		}
		embeddings[i] = &nervepb.EmbedVector{Elements: vec}
	}
	return &nervepb.BatchEmbedResponse{Embeddings: embeddings}, nil
}

func (m *mockNerveService) ExtractPDF(_ context.Context, req *nervepb.ExtractPDFRequest) (*nervepb.ExtractPDFResponse, error) {
	vec := make([]float32, 384)
	return &nervepb.ExtractPDFResponse{
		TotalPages: 2,
		Chunks: []*nervepb.Chunk{
			{
				Text: "introduction text", PageNumber: 1, ChunkIndex: 0,
				SourceType: "text", Embedding: vec,
				BboxX: 10, BboxY: 20, BboxW: 400, BboxH: 15,
			},
			{
				Text: "a bar chart", PageNumber: 1, ChunkIndex: 1,
				SourceType: "image_caption", Embedding: vec,
				BboxX: 50, BboxY: 100, BboxW: 200, BboxH: 150,
			},
		},
	}, nil
}

func startMockNerve(t *testing.T) (addr string, cleanup func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	nervepb.RegisterNerveServiceServer(srv, &mockNerveService{})
	go func() { _ = srv.Serve(lis) }()
	return lis.Addr().String(), func() { srv.Stop() }
}

func TestNerveClient_EmbedReturns384Dims(t *testing.T) {
	addr, cleanup := startMockNerve(t)
	defer cleanup()

	client, err := nerve.NewNerveClient(addr)
	if err != nil {
		t.Fatalf("NewNerveClient: %v", err)
	}
	defer client.Close()

	emb := client.Embedder()
	vec, err := emb.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 384 {
		t.Errorf("expected 384 dimensions, got %d", len(vec))
	}
	if emb.Dimensions() != 384 {
		t.Errorf("Dimensions() = %d, want 384", emb.Dimensions())
	}
}

func TestNerveClient_EmbedBatchReturnsCorrectCount(t *testing.T) {
	addr, cleanup := startMockNerve(t)
	defer cleanup()

	client, err := nerve.NewNerveClient(addr)
	if err != nil {
		t.Fatalf("NewNerveClient: %v", err)
	}
	defer client.Close()

	vecs, err := client.Embedder().EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 3 {
		t.Errorf("expected 3 vectors, got %d", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != 384 {
			t.Errorf("vector[%d]: expected 384 dims, got %d", i, len(v))
		}
	}
}

func TestNerveClient_EmbedBatchEmptyInput(t *testing.T) {
	addr, cleanup := startMockNerve(t)
	defer cleanup()

	client, err := nerve.NewNerveClient(addr)
	if err != nil {
		t.Fatalf("NewNerveClient: %v", err)
	}
	defer client.Close()

	vecs, err := client.Embedder().EmbedBatch(context.Background(), []string{})
	if err != nil {
		t.Fatalf("EmbedBatch(empty): %v", err)
	}
	if len(vecs) != 0 {
		t.Errorf("expected empty result, got %d", len(vecs))
	}
}

func TestNerveClient_ExtractPDFReturnsChunks(t *testing.T) {
	addr, cleanup := startMockNerve(t)
	defer cleanup()

	client, err := nerve.NewNerveClient(addr)
	if err != nil {
		t.Fatalf("NewNerveClient: %v", err)
	}
	defer client.Close()

	chunks, err := client.ExtractPDF(context.Background(), "doc1", "/fake/path.pdf")
	if err != nil {
		t.Fatalf("ExtractPDF: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0].Page != 1 {
		t.Errorf("chunk[0].Page = %d, want 1", chunks[0].Page)
	}
	if chunks[0].SourceType != "text" {
		t.Errorf("chunk[0].SourceType = %q, want %q", chunks[0].SourceType, "text")
	}
	if chunks[1].SourceType != "image_caption" {
		t.Errorf("chunk[1].SourceType = %q, want %q", chunks[1].SourceType, "image_caption")
	}
	if len(chunks[0].Embedding) != 384 {
		t.Errorf("chunk[0].Embedding len = %d, want 384", len(chunks[0].Embedding))
	}
	if chunks[0].BboxX != 10 || chunks[0].BboxY != 20 {
		t.Errorf("unexpected bbox on chunk[0]: x=%.1f y=%.1f", chunks[0].BboxX, chunks[0].BboxY)
	}
}

func TestNerveClient_PingFailsOnBadAddr(t *testing.T) {
	client, err := nerve.NewNerveClient("localhost:1")
	if err != nil {
		return // eager dial failure is acceptable
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if pingErr := client.Ping(ctx); pingErr == nil {
		t.Error("expected Ping to fail on unreachable address")
	}
}
