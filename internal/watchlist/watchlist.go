package watchlist

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type file struct {
	Version int      `json:"version"`
	Dirs    []string `json:"dirs"`
}

// Path returns the absolute path to ~/.zenith/watchlist.json.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".zenith", "watchlist.json"), nil
}

// Load reads the watchlist. Returns an empty slice if the file doesn't exist.
func Load() ([]string, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("watchlist: corrupt file: %w", err)
	}
	return f.Dirs, nil
}

// Add resolves dir to absolute, validates it exists and is a directory,
// deduplicates, and saves. Returns an error if dir does not exist.
func Add(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("not a directory: %s", abs)
	}
	dirs, err := Load()
	if err != nil {
		return err
	}
	for _, d := range dirs {
		if d == abs {
			return nil
		}
	}
	return save(append(dirs, abs))
}

// Remove removes dir (resolved to absolute) from the watchlist.
// It is a no-op if dir is not in the list.
func Remove(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	dirs, err := Load()
	if err != nil {
		return err
	}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d != abs {
			out = append(out, d)
		}
	}
	return save(out)
}

// save writes dirs atomically: write to a .tmp file then rename.
func save(dirs []string) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if dirs == nil {
		dirs = []string{}
	}
	data, err := json.MarshalIndent(file{Version: 1, Dirs: dirs}, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
