package crawler

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Indexer is the interface the Watcher calls when a file needs to be indexed
// or removed. index.Engine satisfies this interface.
type Indexer interface {
	Add(ctx context.Context, id string, text string) error
}

// Watcher walks a directory tree, indexes every supported file, then keeps
// the index current by watching for fsnotify events (create, write, rename,
// remove). All file reads go through ExtractText so every format gets proper
// text extraction.
type Watcher struct {
	indexer  Indexer
	watcher  *fsnotify.Watcher
	mu       sync.Mutex
	watching map[string]struct{}
}

// NewWatcher creates a Watcher backed by indexer.
func NewWatcher(indexer Indexer) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		indexer:  indexer,
		watcher:  fw,
		watching: make(map[string]struct{}),
	}, nil
}

// IndexDir performs a one-time, recursive walk of dir, indexing every
// supported file. It does NOT start watching — use Watch for live updates.
func (w *Watcher) IndexDir(ctx context.Context, dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			slog.Warn("crawler: walk error", "path", path, "error", err)
			return nil
		}
		if d.IsDir() || !SupportedExt(filepath.Ext(path)) {
			return nil
		}
		return w.indexFile(ctx, path)
	})
}

// Watch starts watching dir (and all subdirectories discovered during the
// initial walk) for file-system changes. It blocks until ctx is cancelled.
// Call IndexDir first if you want an initial bulk index, or call Watch
// directly if you only want incremental updates.
func (w *Watcher) Watch(ctx context.Context, dir string) error {
	// Register the root dir and every subdirectory.
	if err := w.addDir(dir); err != nil {
		return err
	}
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		return w.addDir(path)
	}); err != nil {
		return err
	}

	slog.Info("crawler: watching directory", "dir", dir)

	for {
		select {
		case <-ctx.Done():
			return w.watcher.Close()

		case event, ok := <-w.watcher.Events:
			if !ok {
				return nil
			}
			w.handleEvent(ctx, event)

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return nil
			}
			slog.Warn("crawler: fsnotify error", "error", err)
		}
	}
}

// Close releases the underlying fsnotify watcher.
func (w *Watcher) Close() error {
	return w.watcher.Close()
}

// ─── Internal ──────────────────────────────────────────────────────────────────

func (w *Watcher) addDir(dir string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.watching[dir]; ok {
		return nil
	}
	if err := w.watcher.Add(dir); err != nil {
		return err
	}
	w.watching[dir] = struct{}{}
	return nil
}

func (w *Watcher) handleEvent(ctx context.Context, event fsnotify.Event) {
	path := event.Name
	ext := strings.ToLower(filepath.Ext(path))

	switch {
	case event.Has(fsnotify.Create):
		info, err := os.Stat(path)
		if err != nil {
			return
		}
		if info.IsDir() {
			_ = w.addDir(path)
			return
		}
		if !SupportedExt(ext) {
			return
		}
		slog.Info("crawler: indexing new file", "path", path)
		if err := w.indexFile(ctx, path); err != nil {
			slog.Warn("crawler: index failed", "path", path, "error", err)
		}

	case event.Has(fsnotify.Write):
		if !SupportedExt(ext) {
			return
		}
		slog.Info("crawler: re-indexing modified file", "path", path)
		if err := w.indexFile(ctx, path); err != nil {
			slog.Warn("crawler: re-index failed", "path", path, "error", err)
		}

	case event.Has(fsnotify.Remove), event.Has(fsnotify.Rename):
		slog.Info("crawler: file removed", "path", path)
		// index.Engine.Add is idempotent — no remove API yet; log only.
	}
}

func (w *Watcher) indexFile(ctx context.Context, path string) error {
	text, err := ExtractText(path)
	if err != nil {
		return err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	return w.indexer.Add(ctx, absPath, text)
}
