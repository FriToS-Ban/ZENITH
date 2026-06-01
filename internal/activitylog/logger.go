package activitylog

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maxLogSize = 5 * 1024 * 1024 // 5MB

// Logger is a mutex-protected, append-only activity log with 5MB rotation.
// All methods are safe for concurrent use. Write errors are silently ignored
// so logging never crashes zenith.
type Logger struct {
	path string
	f    *os.File
	mu   sync.Mutex
	noop bool
}

// Open opens (or creates) ~/.zenith/zenith.log for appending.
// Returns a Noop logger if the home directory cannot be determined.
func Open() *Logger {
	home, err := os.UserHomeDir()
	if err != nil {
		return Noop()
	}
	return OpenAt(filepath.Join(home, ".zenith", "zenith.log"))
}

// OpenAt opens the log at the given path, creating parent directories as needed.
// Returns a Noop logger if the file cannot be opened.
func OpenAt(path string) *Logger {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Noop()
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return Noop()
	}
	return &Logger{path: path, f: f}
}

// Noop returns a logger that discards all writes. Safe to use in tests.
func Noop() *Logger {
	return &Logger{noop: true}
}

// Log appends one event line in the format:
//
//	2006-01-02 15:04:05  EVENT      detail
func (l *Logger) Log(event, detail string) {
	if l.noop {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	line := fmt.Sprintf("%s  %-10s %s\n",
		time.Now().Format("2006-01-02 15:04:05"),
		event,
		detail,
	)

	if info, err := l.f.Stat(); err == nil && info.Size() >= maxLogSize {
		l.rotate()
	}

	_, _ = l.f.WriteString(line)
}

// Close flushes and closes the log file.
func (l *Logger) Close() error {
	if l.noop || l.f == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

// rotate renames current log to .log.1 and opens a fresh file.
// Caller must hold l.mu.
func (l *Logger) rotate() {
	_ = l.f.Close()
	_ = os.Rename(l.path, l.path+".1")
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		l.noop = true
		return
	}
	l.f = f
}
