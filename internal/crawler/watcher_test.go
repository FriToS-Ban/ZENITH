package crawler_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/fsnotify/fsnotify"
	"github.com/shramanb113/ZENITH/internal/crawler"
)

type recordingIndexer struct {
	mu      sync.Mutex
	added   []string
	removed []string
}

func (r *recordingIndexer) Add(_ context.Context, id, _ string) error {
	r.mu.Lock()
	r.added = append(r.added, id)
	r.mu.Unlock()
	return nil
}

func (r *recordingIndexer) Remove(_ context.Context, id string) error {
	r.mu.Lock()
	r.removed = append(r.removed, id)
	r.mu.Unlock()
	return nil
}

func writeTempFileAt(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writeTempFileAt: %v", err)
	}
}

func TestWatcher_NewWatcher(t *testing.T) {
	w, err := crawler.NewWatcher(&recordingIndexer{})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()
}

func TestWatcher_IndexDir_AddsFiles(t *testing.T) {
	dir := t.TempDir()
	writeTempFileAt(t, dir, "a.txt", "hello world")
	writeTempFileAt(t, dir, "b.txt", "foo bar")

	idx := &recordingIndexer{}
	w, _ := crawler.NewWatcher(idx)
	defer w.Close()

	if err := w.IndexDir(context.Background(), dir); err != nil {
		t.Fatalf("IndexDir: %v", err)
	}
	if len(idx.added) != 2 {
		t.Errorf("expected 2 files added, got %d", len(idx.added))
	}
}

func TestWatcher_HandleRemoveEvent_CallsRemove(t *testing.T) {
	dir := t.TempDir()
	absPath := filepath.Join(dir, "test.txt")

	idx := &recordingIndexer{}
	w, err := crawler.NewWatcher(idx)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	w.SimulateEvent(context.Background(), fsnotify.Event{
		Name: absPath,
		Op:   fsnotify.Remove,
	})

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if len(idx.removed) != 1 {
		t.Fatalf("expected 1 Remove call, got %d", len(idx.removed))
	}
	if idx.removed[0] != absPath {
		t.Errorf("Remove called with %q, want %q", idx.removed[0], absPath)
	}
}

func TestWatcher_HandleRenameEvent_CallsRemove(t *testing.T) {
	dir := t.TempDir()
	absPath := filepath.Join(dir, "old.txt")

	idx := &recordingIndexer{}
	w, _ := crawler.NewWatcher(idx)
	defer w.Close()

	w.SimulateEvent(context.Background(), fsnotify.Event{
		Name: absPath,
		Op:   fsnotify.Rename,
	})

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if len(idx.removed) != 1 {
		t.Errorf("expected 1 Remove call on Rename, got %d", len(idx.removed))
	}
}

func TestWatchMultipleRegistersAllDirs(t *testing.T) {
	idx := &recordingIndexer{}
	w, err := crawler.NewWatcher(idx)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so WatchMultiple returns right away

	// Should not error even when dirs exist and ctx is already done.
	if err := w.WatchMultiple(ctx, []string{dir1, dir2}); err != nil {
		t.Fatalf("WatchMultiple: %v", err)
	}
}

func TestWatcher_HandleRemoveEvent_UnsupportedExt_NoCall(t *testing.T) {
	dir := t.TempDir()
	absPath := filepath.Join(dir, "video.mp4")

	idx := &recordingIndexer{}
	w, _ := crawler.NewWatcher(idx)
	defer w.Close()

	w.SimulateEvent(context.Background(), fsnotify.Event{
		Name: absPath,
		Op:   fsnotify.Remove,
	})

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if len(idx.removed) != 0 {
		t.Errorf("expected no Remove call for unsupported ext, got %d", len(idx.removed))
	}
}
