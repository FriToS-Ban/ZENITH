package pdf_test

import (
	"context"
	"net"
	"strings"
	"testing"

	nervepb "github.com/shramanb113/ZENITH/gen/go/nervepb"
	"github.com/shramanb113/ZENITH/internal/analysis"
	"github.com/shramanb113/ZENITH/internal/config"
	"github.com/shramanb113/ZENITH/internal/embedding"
	"github.com/shramanb113/ZENITH/internal/index"
	"github.com/shramanb113/ZENITH/internal/nerve"
	"github.com/shramanb113/ZENITH/internal/pdf"
	"github.com/shramanb113/ZENITH/internal/ranking"
	"google.golang.org/grpc"
)

type mockPDFNerveService struct {
	nervepb.UnimplementedNerveServiceServer
}

func (m *mockPDFNerveService) Embed(_ context.Context, _ *nervepb.EmbedRequest) (*nervepb.EmbedResponse, error) {
	return &nervepb.EmbedResponse{Embedding: make([]float32, 384)}, nil
}

func (m *mockPDFNerveService) EmbedBatch(_ context.Context, req *nervepb.BatchEmbedRequest) (*nervepb.BatchEmbedResponse, error) {
	embeddings := make([]*nervepb.EmbedVector, len(req.Texts))
	for i := range req.Texts {
		embeddings[i] = &nervepb.EmbedVector{Elements: make([]float32, 384)}
	}
	return &nervepb.BatchEmbedResponse{Embeddings: embeddings}, nil
}

func (m *mockPDFNerveService) ExtractPDF(_ context.Context, req *nervepb.ExtractPDFRequest) (*nervepb.ExtractPDFResponse, error) {
	vec := make([]float32, 384)
	return &nervepb.ExtractPDFResponse{
		TotalPages: 1,
		Chunks: []*nervepb.Chunk{
			{
				Text: "quarterly revenue grew", PageNumber: 1, ChunkIndex: 0,
				SourceType: "text", Embedding: vec,
				BboxX: 10, BboxY: 20, BboxW: 400, BboxH: 15,
			},
			{
				Text: "a pie chart showing market share", PageNumber: 1, ChunkIndex: 1,
				SourceType: "image_caption", Embedding: vec,
				BboxX: 50, BboxY: 100, BboxW: 200, BboxH: 150,
			},
		},
	}, nil
}

func startMockForPDF(t *testing.T) (addr string, cleanup func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	nervepb.RegisterNerveServiceServer(srv, &mockPDFNerveService{})
	go func() { _ = srv.Serve(lis) }()
	return lis.Addr().String(), func() { srv.Stop() }
}

func buildTestEngine(t *testing.T, nerveAddr string) (*index.Engine, *nerve.NerveClient) {
	t.Helper()
	nerveClient, err := nerve.NewNerveClient(nerveAddr)
	if err != nil {
		t.Fatalf("NewNerveClient: %v", err)
	}
	emb, err := embedding.NewCachingEmbedder(nerveClient.Embedder(), 1000)
	if err != nil {
		t.Fatalf("NewCachingEmbedder: %v", err)
	}
	cfg := config.DefaultConfig()
	eng := index.NewEngine(cfg, emb, ranking.NewRRFRanker(0, 0), analysis.NewStandardAnalyzer())
	return eng, nerveClient
}

func TestPDFIndexer_IndexReturnsTwoChunks(t *testing.T) {
	addr, cleanup := startMockForPDF(t)
	defer cleanup()

	eng, nerveClient := buildTestEngine(t, addr)
	defer nerveClient.Close()

	indexer := pdf.NewIndexer(nerveClient, eng)

	count, err := indexer.Index(context.Background(), "report2024", "/fake/report.pdf")
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 chunks indexed, got %d", count)
	}
}

func TestPDFIndexer_IndexChunkIDEncodesLocation(t *testing.T) {
	addr, cleanup := startMockForPDF(t)
	defer cleanup()

	eng, nerveClient := buildTestEngine(t, addr)
	defer nerveClient.Close()

	indexer := pdf.NewIndexer(nerveClient, eng)

	_, err := indexer.Index(context.Background(), "report2024", "/fake/report.pdf")
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	results, err := eng.Search(context.Background(), "quarterly revenue")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results after indexing")
	}

	found := false
	for _, r := range results {
		if strings.HasPrefix(r.ID, "report2024||p1||c0||text||") {
			found = true
			break
		}
	}
	if !found {
		ids := make([]string, len(results))
		for i, r := range results {
			ids[i] = r.ID
		}
		t.Errorf("no result with expected ID prefix; got: %v", ids)
	}
}
