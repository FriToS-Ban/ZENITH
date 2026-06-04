package main

import (
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/shramanb113/ZENITH/gen/go/zenithproto"
	"github.com/shramanb113/ZENITH/internal/activitylog"
	"github.com/shramanb113/ZENITH/internal/analysis"
	"github.com/shramanb113/ZENITH/internal/config"
	"github.com/shramanb113/ZENITH/internal/embedding"
	"github.com/shramanb113/ZENITH/internal/index"
	"github.com/shramanb113/ZENITH/internal/localembedder"
	"github.com/shramanb113/ZENITH/internal/pdf"
	"github.com/shramanb113/ZENITH/internal/ranking"
	"github.com/shramanb113/ZENITH/internal/server"
	storage "github.com/shramanb113/ZENITH/internal/storage"
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

	storageEng, err := storage.Open(storage.DefaultEngineConfig())
	if err != nil {
		slog.Error("Failed to open storage engine", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := storageEng.Close(); err != nil {
			slog.Error("Storage engine close failed", "error", err)
		}
	}()

	alog := activitylog.Open()
	defer alog.Close()

	// Embedder: use local ONNX model when CGo is available, deterministic otherwise.
	var emb embedding.Embedder
	if localEmb, err := localembedder.New(); err == nil {
		emb, _ = embedding.NewCachingEmbedder(localEmb, 10_000)
		slog.Info("Local ONNX embedder ready")
	} else {
		slog.Warn("Local embedder unavailable, using deterministic", "error", err)
		emb = embedding.NewDeterministicEmbedder(384)
	}

	tkz := analysis.NewStandardAnalyzer()
	scorer := ranking.NewRRFRanker(0, 0)
	engine := index.NewEngine(appConfig, emb, scorer, tkz)

	engine.SetFSTPath("./data/index.fst")
	engine.SetTermStore(storageEng)

	if err := engine.Load("zenith.db"); err != nil {
		slog.Info("No existing index found, starting fresh.")
	} else {
		slog.Info("Successfully loaded index from disk.")
		alog.Log("LOADED", "zenith.db")
	}

	pdfIndexer := pdf.NewIndexer(engine, alog)

	grpcServer := grpc.NewServer()
	zenithproto.RegisterSearchServiceServer(grpcServer, &server.ZenithServer{
		Engine:     engine,
		PDFIndexer: pdfIndexer,
		Logger:     alog,
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
		alog.Log("SAVED", "zenith.db")
	}
}
