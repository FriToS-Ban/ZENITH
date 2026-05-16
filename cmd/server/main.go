package main

import (
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/shramanb113/ZENITH/gen/go/zenithproto"
	"github.com/shramanb113/ZENITH/internal/analysis"
	"github.com/shramanb113/ZENITH/internal/config"
	"github.com/shramanb113/ZENITH/internal/embedding"
	"github.com/shramanb113/ZENITH/internal/index"
	"github.com/shramanb113/ZENITH/internal/ranking"
	"github.com/shramanb113/ZENITH/internal/server"
	"google.golang.org/grpc"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	lis, err := net.Listen("tcp", ":8080")
	if err != nil {
		slog.Error("Failed to listen on tcp socket", "error", err)
		os.Exit(1)
	}

	appConfig := config.DefaultConfig()

	tkz := analysis.NewStandardAnalyzer()
	rawEmbedder := embedding.NewNeuralEmbedder(appConfig.NerveURL, appConfig.NerveTimeout)
	embedder, err := embedding.NewCachingEmbedder(rawEmbedder, 10000)
	if err != nil {
		slog.Error("Failed to create embedding cache", "error", err)
		os.Exit(1)
	}

	scorer := ranking.NewRRFRanker(0, 0)
	engine := index.NewEngine(appConfig, embedder, scorer, tkz)

	if err := engine.Load("zenith.db"); err != nil {
		slog.Info("No existing index found, starting fresh.")
	} else {
		slog.Info("Successfully loaded index from disk.")
	}

	grpcServer := grpc.NewServer()
	zenithproto.RegisterSearchServiceServer(grpcServer, &server.ZenithServer{
		Engine: engine,
	})

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		slog.Info("ZENITH engine is live", "address", lis.Addr().String())
		if err := grpcServer.Serve(lis); err != nil {
			slog.Error("gRPC serve failed", "error", err)
		}
	}()

	<-stop
	slog.Info("Graceful shutdown initiated")
	grpcServer.GracefulStop()

	if err := engine.Save("zenith.db"); err != nil {
		slog.Error("Failed to save index", "error", err)
	} else {
		slog.Info("Index saved. Goodbye.")
	}
}
