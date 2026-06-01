package activitylog_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/shramanb113/ZENITH/internal/activitylog"
)

func TestLogger_WritesFormattedLine(t *testing.T) {
	dir := t.TempDir()
	l := activitylog.OpenAt(filepath.Join(dir, "test.log"))
	defer l.Close()

	l.Log("INDEXED", "/some/file.txt")

	data, err := os.ReadFile(filepath.Join(dir, "test.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	line := string(data)
	if !strings.Contains(line, "INDEXED") {
		t.Errorf("log line missing INDEXED: %q", line)
	}
	if !strings.Contains(line, "/some/file.txt") {
		t.Errorf("log line missing detail: %q", line)
	}
}

func TestLogger_Concurrent(t *testing.T) {
	dir := t.TempDir()
	l := activitylog.OpenAt(filepath.Join(dir, "concurrent.log"))
	defer l.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.Log("SEARCH", "query")
		}()
	}
	wg.Wait()

	data, _ := os.ReadFile(filepath.Join(dir, "concurrent.log"))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 100 {
		t.Errorf("expected 100 log lines, got %d", len(lines))
	}
}

func TestLogger_Rotation(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "zenith.log")
	l := activitylog.OpenAt(logPath)
	defer l.Close()

	bigDetail := strings.Repeat("x", 1024)
	for i := 0; i < 5200; i++ {
		l.Log("INDEXED", bigDetail)
	}

	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Error("expected zenith.log.1 to exist after rotation")
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("zenith.log missing after rotation: %v", err)
	}
	if info.Size() >= 5*1024*1024 {
		t.Errorf("zenith.log should be small after rotation, got %d bytes", info.Size())
	}
}

func TestLogger_Noop(t *testing.T) {
	l := activitylog.Noop()
	l.Log("INDEXED", "/some/file.txt")
	if err := l.Close(); err != nil {
		t.Errorf("Noop Close returned error: %v", err)
	}
}

func TestLogger_OpenCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dirs")
	l := activitylog.OpenAt(filepath.Join(dir, "zenith.log"))
	defer l.Close()
	l.Log("NERVE", "ready")
	if _, err := os.Stat(dir); err != nil {
		t.Error("OpenAt should create parent directories")
	}
}
