package fileindex_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shramanb113/ZENITH/internal/fileindex"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
}

func TestFileIndex_NewFileNotUpToDate(t *testing.T) {
	dir := t.TempDir()
	fi, _ := fileindex.Open(filepath.Join(dir, "hashes.json"))

	f := filepath.Join(dir, "doc.txt")
	writeFile(t, f, "hello world")

	if fi.IsUpToDate(f) {
		t.Error("new file should not be up to date before Mark")
	}
}

func TestFileIndex_UpToDateAfterMark(t *testing.T) {
	dir := t.TempDir()
	fi, _ := fileindex.Open(filepath.Join(dir, "hashes.json"))

	f := filepath.Join(dir, "doc.txt")
	writeFile(t, f, "hello world")

	if err := fi.Mark(f); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	if !fi.IsUpToDate(f) {
		t.Error("file should be up to date after Mark")
	}
}

func TestFileIndex_NotUpToDateAfterChange(t *testing.T) {
	dir := t.TempDir()
	fi, _ := fileindex.Open(filepath.Join(dir, "hashes.json"))

	f := filepath.Join(dir, "doc.txt")
	writeFile(t, f, "original content")
	_ = fi.Mark(f)

	// Modify the file
	writeFile(t, f, "changed content")
	if fi.IsUpToDate(f) {
		t.Error("file should not be up to date after content change")
	}
}

func TestFileIndex_SaveAndReload(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "hashes.json")

	fi, _ := fileindex.Open(indexPath)
	f := filepath.Join(dir, "doc.txt")
	writeFile(t, f, "persistent content")
	_ = fi.Mark(f)

	if err := fi.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reload from disk
	fi2, err := fileindex.Open(indexPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !fi2.IsUpToDate(f) {
		t.Error("file should still be up to date after reload")
	}
}

func TestFileIndex_Remove(t *testing.T) {
	dir := t.TempDir()
	fi, _ := fileindex.Open(filepath.Join(dir, "hashes.json"))

	f := filepath.Join(dir, "doc.txt")
	writeFile(t, f, "content")
	_ = fi.Mark(f)

	fi.Remove(f)
	if fi.IsUpToDate(f) {
		t.Error("file should not be up to date after Remove")
	}
}

func TestFileIndex_MissingFileNotUpToDate(t *testing.T) {
	dir := t.TempDir()
	fi, _ := fileindex.Open(filepath.Join(dir, "hashes.json"))

	nonExistent := filepath.Join(dir, "does_not_exist.txt")
	if fi.IsUpToDate(nonExistent) {
		t.Error("non-existent file should not be up to date")
	}
}

func TestFileIndex_Len(t *testing.T) {
	dir := t.TempDir()
	fi, _ := fileindex.Open(filepath.Join(dir, "hashes.json"))

	if fi.Len() != 0 {
		t.Errorf("empty index: want 0, got %d", fi.Len())
	}
	for i, name := range []string{"a.txt", "b.txt", "c.txt"} {
		f := filepath.Join(dir, name)
		writeFile(t, f, name)
		_ = fi.Mark(f)
		if fi.Len() != i+1 {
			t.Errorf("Len after %d marks: want %d, got %d", i+1, i+1, fi.Len())
		}
	}
}

func TestFileIndex_ConcurrentMark(t *testing.T) {
	dir := t.TempDir()
	fi, _ := fileindex.Open(filepath.Join(dir, "hashes.json"))

	const n = 20
	done := make(chan struct{}, n)
	for i := range n {
		go func(i int) {
			f := filepath.Join(dir, filepath.FromSlash(
				filepath.Join("sub", "file_"+string(rune('a'+i))+".txt")))
			_ = os.MkdirAll(filepath.Dir(f), 0o755)
			writeFile(t, f, "content")
			_ = fi.Mark(f)
			done <- struct{}{}
		}(i)
	}
	for range n {
		<-done
	}
	if fi.Len() != n {
		t.Errorf("expected %d entries after concurrent marks, got %d", n, fi.Len())
	}
}

func TestFileIndex_AtomicSave(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "hashes.json")
	fi, _ := fileindex.Open(indexPath)

	f := filepath.Join(dir, "doc.txt")
	writeFile(t, f, "data")
	_ = fi.Mark(f)
	if err := fi.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Temp file must not linger after successful save
	if _, err := os.Stat(indexPath + ".tmp"); err == nil {
		t.Error("temp file should not exist after successful Save")
	}
	if _, err := os.Stat(indexPath); err != nil {
		t.Error("index file should exist after Save")
	}
}
