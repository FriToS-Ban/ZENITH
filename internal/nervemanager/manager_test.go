package nervemanager

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ─── URL ──────────────────────────────────────────────────────────────────────

func TestManager_URL(t *testing.T) {
	m := &Manager{port: 8000}
	want := "http://127.0.0.1:8000"
	if got := m.URL(); got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
}

// ─── ping ─────────────────────────────────────────────────────────────────────

func TestManager_ping_NoServer(t *testing.T) {
	m := &Manager{port: 19999} // nothing listening here
	if m.ping() {
		t.Error("ping should return false when no server is listening")
	}
}

func TestManager_ping_LiveServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == healthEndpoint {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	addr := srv.Listener.Addr().String()
	var port int
	if _, err := fmt.Sscanf(addr[strings.LastIndex(addr, ":")+1:], "%d", &port); err != nil || port == 0 {
		t.Skipf("could not parse port from %q", addr)
	}

	m := &Manager{port: port}
	if !m.ping() {
		t.Error("ping should return true when server is healthy")
	}
}

// ─── extract ──────────────────────────────────────────────────────────────────

func TestManager_extract(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{dir: dir}

	if err := m.extract(); err != nil {
		t.Fatalf("extract: %v", err)
	}

	for _, name := range []string{"main.py", "requirements.txt"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %q after extract: %v", path, err)
		}
	}

	content, err := os.ReadFile(filepath.Join(dir, "main.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "/health") {
		t.Error("extracted main.py missing /health endpoint")
	}

	reqs, err := os.ReadFile(filepath.Join(dir, "requirements.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range []string{"fastapi", "uvicorn", "sentence-transformers"} {
		if !strings.Contains(string(reqs), dep) {
			t.Errorf("requirements.txt missing dependency %q", dep)
		}
	}
}

func TestManager_extract_Idempotent(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{dir: dir}
	if err := m.extract(); err != nil {
		t.Fatalf("first extract: %v", err)
	}
	if err := m.extract(); err != nil {
		t.Fatalf("second extract: %v", err)
	}
}

// ─── venv paths ───────────────────────────────────────────────────────────────

func TestManager_VenvPaths_Consistent(t *testing.T) {
	m := &Manager{dir: "/some/dir"}
	paths := []string{m.venvPython(), m.venvPip(), m.venvUvicorn()}

	for _, p := range paths {
		if !strings.Contains(p, "venv") {
			t.Errorf("venv path %q missing 'venv'", p)
		}
	}
	if runtime.GOOS == "windows" {
		for _, p := range paths {
			if !strings.Contains(p, "Scripts") {
				t.Errorf("Windows path %q missing 'Scripts'", p)
			}
		}
	} else {
		if !strings.Contains(m.venvPython(), "bin") {
			t.Errorf("Unix python path missing 'bin': %q", m.venvPython())
		}
	}
}

// ─── findPython ───────────────────────────────────────────────────────────────

func TestManager_findPython(t *testing.T) {
	m := &Manager{}
	path, err := m.findPython()
	if err != nil {
		t.Skipf("python3 not available on this machine: %v", err)
	}
	if path == "" {
		t.Error("findPython returned empty path without error")
	}
}

// ─── Start — already running ──────────────────────────────────────────────────

func TestManager_Start_AlreadyRunning(t *testing.T) {
	// Serve a fake /health endpoint, then verify Start returns StatusReady
	// without attempting to launch anything.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == healthEndpoint {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	addr := srv.Listener.Addr().String()
	var port int
	if _, err := fmt.Sscanf(addr[strings.LastIndex(addr, ":")+1:], "%d", &port); err != nil || port == 0 {
		t.Skipf("could not parse port from %q", addr)
	}

	m := &Manager{dir: t.TempDir(), port: port}
	url, status := m.Start(context.Background())
	if status != StatusReady {
		t.Errorf("expected StatusReady when nerve already up, got %v", status)
	}
	if url == "" {
		t.Error("expected non-empty URL on StatusReady")
	}
}
