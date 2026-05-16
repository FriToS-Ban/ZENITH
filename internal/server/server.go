package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/shramanb113/ZENITH/gen/go/zenithproto"
	"github.com/shramanb113/ZENITH/internal/index"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ZenithServer implements zenithproto.SearchServiceServer.
type ZenithServer struct {
	zenithproto.UnimplementedSearchServiceServer
	Engine *index.Engine
}

func (s *ZenithServer) IndexDocuments(
	ctx context.Context,
	req *zenithproto.IndexRequest,
) (*zenithproto.IndexResponse, error) {

	if req.GetId() == "" {
		return &zenithproto.IndexResponse{
			Status:  false,
			Message: "document id must not be empty",
		}, status.Error(codes.InvalidArgument, "document id must not be empty")
	}
	if req.GetData() == "" {
		return &zenithproto.IndexResponse{
			Status:  false,
			Message: "document data must not be empty",
		}, status.Error(codes.InvalidArgument, "document data must not be empty")
	}

	if err := s.Engine.Add(ctx, req.GetId(), req.GetData()); err != nil {
		slog.Error("Failed to index document", "id", req.GetId(), "error", err)
		msg := fmt.Sprintf("indexing failed: %v", err)
		return &zenithproto.IndexResponse{
			Status:  false,
			Message: msg,
		}, status.Error(codes.Internal, msg)
	}

	slog.Info("Document indexed", "id", req.GetId())
	return &zenithproto.IndexResponse{
		Status:  true,
		Message: fmt.Sprintf("document %s indexed successfully", req.GetId()),
	}, nil
}

func (s *ZenithServer) Search(
	ctx context.Context,
	req *zenithproto.SearchRequest,
) (*zenithproto.SearchResponse, error) {

	if req.GetQuery() == "" {
		return nil, status.Error(codes.InvalidArgument, "query must not be empty")
	}

	results, err := s.Engine.Search(ctx, req.GetQuery())
	if err != nil {
		slog.Error("Search failed", "query", req.GetQuery(), "error", err)
		return nil, status.Errorf(codes.Internal, "search failed: %v", err)
	}

	protoResults := make([]*zenithproto.SearchResult, 0, len(results))
	for _, r := range results {
		protoResults = append(protoResults, &zenithproto.SearchResult{
			Id:    r.ID,
			Score: r.Score,
		})
	}

	slog.Info("Search complete", "query", req.GetQuery(), "hits", len(protoResults))
	return &zenithproto.SearchResponse{Results: protoResults}, nil
}
