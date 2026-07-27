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

// ─── Parallel IndexDir ────────────────────────────────────────────────────────

func TestWatcher_IndexDir_ParallelIndexesAllFiles(t *testing.T) {
	dir := t.TempDir()
	const n = 12
	for i := range n {
		writeTempFileAt(t, dir, filepath.FromSlash("file_"+string(rune('a'+i))+".txt"), "content")
	}

	idx := &recordingIndexer{}
	w, err := crawler.NewWatcher(idx)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	if err := w.IndexDir(context.Background(), dir); err != nil {
		t.Fatalf("IndexDir: %v", err)
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if len(idx.added) != n {
		t.Errorf("expected %d files indexed, got %d", n, len(idx.added))
	}
}

func TestWatcher_IndexDir_ParallelNoDuplicates(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alpha.txt", "beta.md", "gamma.go"} {
		writeTempFileAt(t, dir, name, name)
	}

	idx := &recordingIndexer{}
	w, _ := crawler.NewWatcher(idx)
	defer w.Close()

	if err := w.IndexDir(context.Background(), dir); err != nil {
		t.Fatalf("IndexDir: %v", err)
	}

	idx.mu.Lock()
	seen := make(map[string]int)
	for _, id := range idx.added {
		seen[id]++
	}
	idx.mu.Unlock()
	for path, count := range seen {
		if count > 1 {
			t.Errorf("file %q indexed %d times (want 1)", path, count)
		}
	}
}

// ─── Skip / After hooks (deduplication) ──────────────────────────────────────

func TestWatcher_SkipFile_SkipsMatchingFiles(t *testing.T) {
	dir := t.TempDir()
	writeTempFileAt(t, dir, "skip.txt", "content")
	writeTempFileAt(t, dir, "keep.txt", "content")

	idx := &recordingIndexer{}
	w, _ := crawler.NewWatcher(idx)
	defer w.Close()

	skipped := filepath.Join(dir, "skip.txt")
	w.SetSkipFile(func(absPath string) bool {
		return absPath == skipped
	})

	if err := w.IndexDir(context.Background(), dir); err != nil {
		t.Fatalf("IndexDir: %v", err)
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, id := range idx.added {
		if id == skipped {
			t.Errorf("skip.txt was indexed despite being skipped")
		}
	}
	if len(idx.added) != 1 {
		t.Errorf("expected 1 file indexed (keep.txt), got %d", len(idx.added))
	}
}

func TestWatcher_AfterFile_CalledAfterIndex(t *testing.T) {
	dir := t.TempDir()
	writeTempFileAt(t, dir, "doc.txt", "hello")

	idx := &recordingIndexer{}
	w, _ := crawler.NewWatcher(idx)
	defer w.Close()

	var mu sync.Mutex
	var afterCalled []string
	w.SetAfterFile(func(path string) {
		mu.Lock()
		afterCalled = append(afterCalled, path)
		mu.Unlock()
	})

	if err := w.IndexDir(context.Background(), dir); err != nil {
		t.Fatalf("IndexDir: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(afterCalled) != 1 {
		t.Errorf("expected after-file callback 1 time, got %d", len(afterCalled))
	}
}

func TestWatcher_SkipAll_IndexesNothing(t *testing.T) {
	dir := t.TempDir()
	writeTempFileAt(t, dir, "a.txt", "a")
	writeTempFileAt(t, dir, "b.txt", "b")

	idx := &recordingIndexer{}
	w, _ := crawler.NewWatcher(idx)
	defer w.Close()
	w.SetSkipFile(func(string) bool { return true }) // skip everything

	if err := w.IndexDir(context.Background(), dir); err != nil {
		t.Fatalf("IndexDir: %v", err)
	}

	idx.mu.Lock()
	defer idx.mu.Unlock()
	if len(idx.added) != 0 {
		t.Errorf("expected 0 files indexed when all skipped, got %d", len(idx.added))
	}
}

