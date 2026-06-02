// Package fileindex tracks content-based hashes for indexed files so that
// unchanged files can be skipped on subsequent index runs.
package fileindex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"sync"
)

// Index maps absolute file paths to their SHA-256 content hashes.
// Thread-safe; safe to use from concurrent goroutines.
type Index struct {
	path   string
	hashes map[string]string
	mu     sync.RWMutex
}

// Open loads an existing hash index from path or creates an empty one.
// The file need not exist; a fresh index is returned without error.
func Open(path string) (*Index, error) {
	fi := &Index{path: path, hashes: make(map[string]string)}
	data, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(data, &fi.hashes)
	}
	return fi, nil
}

// IsUpToDate returns true when the file at absPath exists and its current
// SHA-256 hash matches the last recorded hash. Files that are new, modified,
// or unreadable return false.
func (fi *Index) IsUpToDate(absPath string) bool {
	h, err := hashFile(absPath)
	if err != nil {
		return false
	}
	fi.mu.RLock()
	stored, ok := fi.hashes[absPath]
	fi.mu.RUnlock()
	return ok && stored == h
}

// Mark records the current hash of absPath. Call this after a file is
// successfully indexed so the next run can skip it if it has not changed.
func (fi *Index) Mark(absPath string) error {
	h, err := hashFile(absPath)
	if err != nil {
		return err
	}
	fi.mu.Lock()
	fi.hashes[absPath] = h
	fi.mu.Unlock()
	return nil
}

// Remove deletes the hash record for absPath. The next index run will
// re-index the file unconditionally.
func (fi *Index) Remove(absPath string) {
	fi.mu.Lock()
	delete(fi.hashes, absPath)
	fi.mu.Unlock()
}

// Save persists the index atomically (write to temp + rename).
func (fi *Index) Save() error {
	fi.mu.RLock()
	data, err := json.Marshal(fi.hashes)
	fi.mu.RUnlock()
	if err != nil {
		return err
	}
	tmp := fi.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, fi.path)
}

// Len returns the number of tracked file records.
func (fi *Index) Len() int {
	fi.mu.RLock()
	defer fi.mu.RUnlock()
	return len(fi.hashes)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
