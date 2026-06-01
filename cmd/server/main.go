package main

import (
	"fmt"
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
	"github.com/shramanb113/ZENITH/internal/nerve"
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

	//  Storage engine (LSM)
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

	//  Nerve gRPC sidecar
	nerveClient, err := nerve.NewNerveClient(appConfig.NerveGRPCAddr)
	if err != nil {
		slog.Error("Failed to connect to Nerve sidecar", "addr", appConfig.NerveGRPCAddr, "error", err)
		os.Exit(1)
	}
	alog.Log("NERVE", fmt.Sprintf("ready (%s)", appConfig.NerveGRPCAddr))
	defer func() {
		if err := nerveClient.Close(); err != nil {
			slog.Error("Nerve client close failed", "error", err)
		}
	}()

	//  Index engine
	tkz := analysis.NewStandardAnalyzer()
	rawEmbedder := nerveClient.Embedder()
	embedder, err := embedding.NewCachingEmbedder(rawEmbedder, 10000)
	if err != nil {
		slog.Error("Failed to create embedding cache", "error", err)
		os.Exit(1)
	}

	scorer := ranking.NewRRFRanker(0, 0)
	engine := index.NewEngine(appConfig, embedder, scorer, tkz)

	engine.SetFSTPath("./data/index.fst")
	engine.SetTermStore(storageEng)

	if err := engine.Load("zenith.db"); err != nil {
		slog.Info("No existing index found, starting fresh.")
	} else {
		slog.Info("Successfully loaded index from disk.")
		alog.Log("LOADED", "zenith.db")
	}

	//  PDF indexer
	pdfIndexer := pdf.NewIndexer(nerveClient, engine, alog)

	//  gRPC server
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
