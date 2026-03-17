package server

import (
	"context"

	"github.com/google/uuid"
	"log/slog"

	"github.com/shramanb113/ZENITH/gen/go/zenithproto"
	"github.com/shramanb113/ZENITH/internal/index"
)

type ZenithServer struct {
	zenithproto.UnimplementedSearchServiceServer
	Engine *index.Engine
}

func (s *ZenithServer) IndexDocuments(ctx context.Context, req *zenithproto.IndexRequest) (*zenithproto.IndexResponse, error) {

	logger := slog.With("doc_id", req.Id)
	logger.Info("Ingesting new document")

	err := s.Engine.Add(ctx, req.Id, req.Data)

	if err != nil {
		logger.Error("Failed to index document", "error", err)
		return &zenithproto.IndexResponse{
			Status:  false,
			Message: "Failed indexing document",
		}, err
	}

	return &zenithproto.IndexResponse{
		Status:  true,
		Message: "Document Indexed successfully",
	}, nil
}

func (s *ZenithServer) Search(ctx context.Context, req *zenithproto.SearchRequest) (*zenithproto.SearchResponse, error) {

	// AP-3 and AP-5 Fix: Logging search with UUID string
	requestID := uuid.NewString()
	searchCtx := context.WithValue(ctx, "request_id", requestID)

	logger := slog.With(
		slog.String("request_id", requestID),
		slog.String("query", req.Query),
	)

	logger.Info("Search started")

	results, err := s.Engine.Search(searchCtx, req.Query)
	if err != nil {
		logger.Error("Search failed", "error", err)
		return nil, err
	}

	logger.Info("Search completed", slog.Int("results_count", len(results)))

	var protoResults []*zenithproto.SearchResult

	for _, res := range results {
		protoResults = append(protoResults, &zenithproto.SearchResult{
			Id:    res.ID,
			Score: res.Score,
		})
	}

	return &zenithproto.SearchResponse{
		Results: protoResults,
	}, nil
}
