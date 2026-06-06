package storage_test

import (
	"context"
	"path/filepath"
	"testing"

	storage "github.com/shramanb113/ZENITH/internal/storage"
	"github.com/shramanb113/ZENITH/internal/storage/wal"
)


func TestStorageEngine_RecordsAfterOpen(t *testing.T) {
	dir := t.TempDir()
	cfg := storage.DefaultEngineConfig()
	cfg.WALPath = filepath.Join(dir, "zenith.wal")
	cfg.WALConfig.Dir = dir
	cfg.SSTDir = filepath.Join(dir, "sst")
	cfg.FSTPath = ""

	ctx := context.Background()

	// Phase 1: write two documents via Put.
	eng1, err := storage.Open(cfg)
	if err != nil {
		t.Fatalf("Open phase1: %v", err)
	}
	if err := eng1.Put(ctx, []byte("doc1"), []byte("hello world")); err != nil {
		t.Fatalf("Put doc1: %v", err)
	}
	if err := eng1.Put(ctx, []byte("doc2"), []byte("foo bar")); err != nil {
		t.Fatalf("Put doc2: %v", err)
	}
	// Close without checkpoint — WAL retains the records.
	if err := eng1.Close(); err != nil {
		t.Fatalf("Close phase1: %v", err)
	}

	// Phase 2: reopen — Records() must return the two documents.
	eng2, err := storage.Open(cfg)
	if err != nil {
		t.Fatalf("Open phase2: %v", err)
	}
	defer eng2.Close()

	records := eng2.Records()
	if len(records) != 2 {
		t.Fatalf("expected 2 records after reopen, got %d", len(records))
	}
	ids := map[string]string{}
	for _, r := range records {
		if r.Op == wal.OpTypePut {
			ids[string(r.Key)] = string(r.Value)
		}
	}
	if ids["doc1"] != "hello world" {
		t.Errorf("doc1 value mismatch: %q", ids["doc1"])
	}
	if ids["doc2"] != "foo bar" {
		t.Errorf("doc2 value mismatch: %q", ids["doc2"])
	}
}

func TestStorageEngine_CheckpointClearsJournal(t *testing.T) {
	dir := t.TempDir()
	cfg := storage.DefaultEngineConfig()
	cfg.WALPath = filepath.Join(dir, "zenith.wal")
	cfg.WALConfig.Dir = dir
	cfg.SSTDir = filepath.Join(dir, "sst")
	cfg.FSTPath = ""

	ctx := context.Background()

	eng1, err := storage.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := eng1.Put(ctx, []byte("doc1"), []byte("content")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Checkpoint — WAL must be reset.
	if err := eng1.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if err := eng1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen — Records() must be empty (WAL was reset).
	eng2, err := storage.Open(cfg)
	if err != nil {
		t.Fatalf("reopen after checkpoint: %v", err)
	}
	defer eng2.Close()

	if got := eng2.Records(); len(got) != 0 {
		t.Fatalf("expected 0 records after Checkpoint, got %d", len(got))
	}
}
