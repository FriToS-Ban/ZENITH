package watchlist_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shramanb113/ZENITH/internal/watchlist"
)

// pointHome redirects UserHomeDir to a temp dir for the duration of the test.
func pointHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	return tmp
}

func TestLoadEmpty(t *testing.T) {
	pointHome(t)
	dirs, err := watchlist.Load()
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("expected empty, got %v", dirs)
	}
}

func TestAddRemove(t *testing.T) {
	home := pointHome(t)
	dir1 := filepath.Join(home, "docs")
	dir2 := filepath.Join(home, "notes")
	os.MkdirAll(dir1, 0o755)
	os.MkdirAll(dir2, 0o755)

	if err := watchlist.Add(dir1); err != nil {
		t.Fatalf("Add dir1: %v", err)
	}
	dirs, _ := watchlist.Load()
	if len(dirs) != 1 || dirs[0] != dir1 {
		t.Fatalf("after first add want [%s], got %v", dir1, dirs)
	}

	// Duplicate add is a no-op.
	if err := watchlist.Add(dir1); err != nil {
		t.Fatalf("duplicate Add: %v", err)
	}
	dirs, _ = watchlist.Load()
	if len(dirs) != 1 {
		t.Fatalf("dedup failed, got %v", dirs)
	}

	if err := watchlist.Add(dir2); err != nil {
		t.Fatalf("Add dir2: %v", err)
	}
	dirs, _ = watchlist.Load()
	if len(dirs) != 2 {
		t.Fatalf("want 2 dirs, got %v", dirs)
	}

	if err := watchlist.Remove(dir1); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	dirs, _ = watchlist.Load()
	if len(dirs) != 1 || dirs[0] != dir2 {
		t.Fatalf("after remove want [%s], got %v", dir2, dirs)
	}
}

func TestAddNonExistent(t *testing.T) {
	home := pointHome(t)
	err := watchlist.Add(filepath.Join(home, "ghost"))
	if err == nil {
		t.Fatal("expected error for non-existent directory")
	}
}

func TestRemoveNonexistentIsNoop(t *testing.T) {
	home := pointHome(t)
	dir := filepath.Join(home, "something")
	os.MkdirAll(dir, 0o755)
	watchlist.Add(dir)
	// Remove twice — second is a no-op, no error.
	watchlist.Remove(dir)
	if err := watchlist.Remove(dir); err != nil {
		t.Fatalf("second Remove should be no-op: %v", err)
	}
}
