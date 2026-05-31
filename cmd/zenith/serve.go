package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/shramanb113/ZENITH/gen/go/zenithproto"
	"github.com/shramanb113/ZENITH/internal/server"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

var serveFlags struct {
	port string
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the gRPC search server",
	Long: `Starts the ZENITH gRPC server and exposes the full search and indexing
API for remote clients (use cmd/client or any gRPC client).

The server loads the existing index from zenith.db on startup and saves it
on graceful shutdown (SIGINT / SIGTERM).`,

	RunE: func(cmd *cobra.Command, args []string) error {
		setupLogger()

		lis, err := net.Listen("tcp", ":"+serveFlags.port)
		if err != nil {
			return fmt.Errorf("listen :%s: %w", serveFlags.port, err)
		}

		engine, teardown, err := buildEngine(true)
		if err != nil {
			return fmt.Errorf("engine init: %w", err)
		}

		grpcServer := grpc.NewServer()
		zenithproto.RegisterSearchServiceServer(grpcServer, &server.ZenithServer{Engine: engine})

		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

		go func() {
			slog.Info("ZENITH gRPC server live", "addr", lis.Addr())
			if err := grpcServer.Serve(lis); err != nil {
				slog.Error("gRPC serve error", "error", err)
			}
		}()

		<-stop
		slog.Info("Graceful shutdown")
		grpcServer.GracefulStop()
		teardown()
		return nil
	},
}

func init() {
	addEngineFlags(serveCmd)
	serveCmd.Flags().StringVarP(&serveFlags.port, "port", "p", "8080", "gRPC listen port")
}
