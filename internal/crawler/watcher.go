package crawler

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/shramanb113/ZENITH/internal/activitylog"
)

// Indexer is the interface the Watcher calls when a file needs to be indexed
// or removed. index.Engine satisfies this interface.
type Indexer interface {
	Add(ctx context.Context, id string, text string) error
	Remove(ctx context.Context, id string) error
}

// FileIndexer handles files that require special extraction (PDFs, images)
// rather than plain text via ExtractText. Both arguments are the same absolute path.
type FileIndexer interface {
	Index(ctx context.Context, docID, filePath string) (int, error)
}

// Watcher walks a directory tree, indexes every supported file, then keeps
// the index current by watching for fsnotify events (create, write, rename,
// remove). All file reads go through ExtractText so every format gets proper
// text extraction. Rich formats (PDF, images) are dispatched to registered
// FileIndexers rather than the plain-text path.
type Watcher struct {
	indexer       Indexer
	fileIndexers  map[string]FileIndexer
	onFileIndexed func(path string)
	watcher       *fsnotify.Watcher
	logger        *activitylog.Logger
	mu            sync.Mutex
	watching      map[string]struct{}
}

// NewWatcher creates a Watcher backed by indexer.
// An optional logger may be supplied; if omitted a no-op logger is used.
func NewWatcher(indexer Indexer, logger ...*activitylog.Logger) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	var l *activitylog.Logger
	if len(logger) > 0 && logger[0] != nil {
		l = logger[0]
	} else {
		l = activitylog.Noop()
	}
	return &Watcher{
		indexer:      indexer,
		fileIndexers: make(map[string]FileIndexer),
		watcher:      fw,
		logger:       l,
		watching:     make(map[string]struct{}),
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
func (w *Watcher) Watch(ctx context.Context, dir string) error {
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

// WatchMultiple registers all dirs with the fsnotify watcher, then runs a
// single shared event loop until ctx is cancelled. Use this instead of
// calling Watch concurrently when you want to watch several directories.
func (w *Watcher) WatchMultiple(ctx context.Context, dirs []string) error {
	for _, dir := range dirs {
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
	}

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

// RegisterFileIndexer registers fi as the handler for files with the given extension.
// ext must include the leading dot (e.g. ".pdf"). Overwrites any prior registration.
func (w *Watcher) RegisterFileIndexer(ext string, fi FileIndexer) {
	w.fileIndexers[strings.ToLower(ext)] = fi
}

// SetOnFileIndexed registers a callback invoked after a rich-format file (PDF,
// image) is successfully indexed. Use this to count such files separately from
// the text-file path that goes through Indexer.Add.
func (w *Watcher) SetOnFileIndexed(fn func(path string)) {
	w.onFileIndexed = fn
}

// SimulateEvent injects a synthetic fsnotify event for testing.
func (w *Watcher) SimulateEvent(ctx context.Context, event fsnotify.Event) {
	w.handleEvent(ctx, event)
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
		if !SupportedExt(ext) {
			return
		}
		absPath, _ := filepath.Abs(path)
		slog.Info("crawler: removing deleted file from index", "path", path)
		if err := w.indexer.Remove(ctx, absPath); err != nil {
			slog.Warn("crawler: remove failed", "path", path, "error", err)
		} else {
			w.logger.Log("REMOVED", absPath)
		}
	}
}

func (w *Watcher) indexFile(ctx context.Context, path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	ext := strings.ToLower(filepath.Ext(path))
	if fi, ok := w.fileIndexers[ext]; ok {
		_, ferr := fi.Index(ctx, absPath, absPath)
		if ferr != nil {
			slog.Warn("crawler: rich-format index failed, skipping", "path", path, "error", ferr)
			return nil
		}
		if w.onFileIndexed != nil {
			w.onFileIndexed(absPath)
		}
		w.logger.Log("INDEXED", absPath)
		return nil
	}

	text, err := ExtractText(path)
	if err != nil {
		return err
	}
	if err := w.indexer.Add(ctx, absPath, text); err != nil {
		return err
	}
	w.logger.Log("INDEXED", absPath)
	return nil
}
