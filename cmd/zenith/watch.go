package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/shramanb113/ZENITH/internal/crawler"
	"github.com/spf13/cobra"
)

var watchFlags struct {
	indexFirst bool
}

var watchCmd = &cobra.Command{
	Use:   "watch <directory>",
	Short: "Watch a directory and incrementally re-index on changes",
	Long: `Watches <directory> for file-system events (create, modify, rename,
delete) via fsnotify and incrementally updates the index.

Use --index-first to perform a full bulk index before starting the watcher.
Without that flag, only changes made after watch starts are indexed.

Press Ctrl-C to stop. The index is saved to disk on exit.`,

	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		setupLogger()
		dir := args[0]

		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return fmt.Errorf("not a directory: %s", dir)
		}

		engine, teardown, err := buildEngine(true)
		if err != nil {
			return fmt.Errorf("engine init: %w", err)
		}
		defer teardown()

		w, err := crawler.NewWatcher(engine)
		if err != nil {
			return err
		}
		defer w.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Graceful shutdown on Ctrl-C.
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigCh
			fmt.Println("\nShutting down watcher...")
			cancel()
		}()

		if watchFlags.indexFirst {
			fmt.Printf("Bulk-indexing %s ...\n", dir)
			if err := w.IndexDir(ctx, dir); err != nil {
				return fmt.Errorf("initial index: %w", err)
			}
		}

		fmt.Printf("Watching %s (Ctrl-C to stop)\n", dir)
		return w.Watch(ctx, dir)
	},
}

func init() {
	addEngineFlags(watchCmd)
	watchCmd.Flags().BoolVar(&watchFlags.indexFirst, "index-first", false, "Bulk-index directory before starting the watcher")
}
