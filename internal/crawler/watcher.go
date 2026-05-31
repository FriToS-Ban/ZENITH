package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "zenith",
	Short: "ZENITH: Local-first semantic & lexical search engine",
	Long: `ZENITH is a high-performance local search engine built from scratch in Go.
It features a hybrid scoring engine (BM25 + Cosine Similarity fused via RRF) 
backed by a native LSM-tree storage engine (WAL, MemTable, SSTables).`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(`
 ███████╗███████╗███╗   ██╗██╗████████╗██╗  ██╗
 ╚══███╔╝██╔════╝████╗  ██║██║╚══██╔══╝██║  ██║
   ███╔╝ █████╗  ██╔██╗ ██║██║   ██║   ███████║
  ███╔╝  ██╔══╝  ██║╚██╗██║██║   ██║   ██╔══██║
 ███████╗███████╗██║ ╚████║██║   ██║   ██║  ██║
 ╚══════╝╚══════╝╚═╝  ╚═══╝╚═╝   ╚═╝   ╚═╝  ╚═╝
 
A local-first hybrid search engine written from scratch in Go.
USAGE:
  zenith [command] [arguments]

CORE COMMANDS:
  watch <dir>   Watch a local directory for real-time changes & index files
  index <dir>   Perform a bulk, one-time scan and index of a directory
  search <q>    Query the engine (Supports natural language & --fuzzy flags)
  serve         Start the production gRPC server & Prometheus metrics endpoint

ENGINE ARCHITECTURE:
  [Crawler] ──> [Analyzer] ──> [Embedder] ──> [LSM Storage Engine]
  (fsnotify)    (Tokenizer)    (Local Ollama)  (WAL -> MemTable -> SSTable)

HYBRID RANKING PIPELINE:
  1. Lexical:  Inverted Index with BM25 / TF-IDF scoring & Porter Stemming
  2. Fuzzy:    BK-Tree over Levenshtein distance for O(log n) typo tolerance
  3. Semantic: Local Vector embeddings (nomic-embed-text) via Ollama
  4. Fusion:   Reciprocal Rank Fusion (RRF) score normalization

Use "zenith [command] --help" for more information about a specific command.`)
	},
}

var watchCmd = &cobra.Command{
	Use:   "watch [directory_path]",
	Short: "Watch a directory for real-time file changes",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Help()
			return
		}

		targetDir := args[0]

		info, err := os.Stat(targetDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error accessing path: %v\n", err)
			os.Exit(1)
		}
		if !info.IsDir() {
			fmt.Fprintf(os.Stderr, "Path is a file, not a directory: %s\n", targetDir)
			os.Exit(1)
		}

		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize fsnotify: %v\n", err)
			os.Exit(1)
		}
		defer watcher.Close()

		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

		go func() {
			for {
				select {
				case event, ok := <-watcher.Events:
					if !ok {
						return
					}
					if event.Has(fsnotify.Write) {
						fmt.Printf("[WRITE] File modified: %s\n", event.Name)
					} else if event.Has(fsnotify.Create) {
						fmt.Printf("[CREATE] File created: %s\n", event.Name)
					} else if event.Has(fsnotify.Remove) {
						fmt.Printf("[REMOVE] File deleted: %s\n", event.Name)
					} else if event.Has(fsnotify.Rename) {
						fmt.Printf("[RENAME] File moved/renamed: %s\n", event.Name)
					}
				case err, ok := <-watcher.Errors:
					if !ok {
						return
					}
					fmt.Fprintf(os.Stderr, "Watcher error: %v\n", err)
				}
			}
		}()

		err = watcher.Add(targetDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to add path to watcher: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("⚡ ZENITH: Active watcher streaming from -> %s\n", targetDir)

		<-sigChan
		fmt.Println("\n🛑 Gracefully shutting down ZENITH Watcher registry...")
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Oops. An error while executing ZENITH '%s'\n", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(watchCmd)
}
