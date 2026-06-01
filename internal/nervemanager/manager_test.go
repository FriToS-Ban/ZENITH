package nervemanager

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ─── Addr ─────────────────────────────────────────────────────────────────────

func TestManager_Addr(t *testing.T) {
	m := &Manager{port: 8000}
	want := "127.0.0.1:8000"
	if got := m.Addr(); got != want {
		t.Errorf("Addr() = %q, want %q", got, want)
	}
}

// ─── ping ─────────────────────────────────────────────────────────────────────

func TestManager_ping_NoServer(t *testing.T) {
	m := &Manager{port: 19999}
	if m.ping() {
		t.Error("ping should return false when nothing is listening")
	}
}

func TestManager_ping_LiveServer(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close()
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	port := freePort(t, lis)
	m := &Manager{port: port}
	if !m.ping() {
		t.Error("ping should return true when a TCP server is listening")
	}
}

// ─── extract ──────────────────────────────────────────────────────────────────

func TestManager_extract(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{dir: dir}

	if err := m.extract(); err != nil {
		t.Fatalf("extract: %v", err)
	}

	for _, name := range []string{"main.py", "requirements.txt", "nerve_pb2.py", "nerve_pb2_grpc.py"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected file %q after extract: %v", name, err)
		}
	}

	content, err := os.ReadFile(filepath.Join(dir, "main.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "grpc.aio") {
		t.Error("extracted main.py missing grpc.aio — expected gRPC server")
	}

	reqs, err := os.ReadFile(filepath.Join(dir, "requirements.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range []string{"grpcio", "pdfplumber", "sentence-transformers"} {
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
	paths := []string{m.venvPython(), m.venvPip()}
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
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close()
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	port := freePort(t, lis)
	m := &Manager{dir: t.TempDir(), port: port}
	addr, status := m.Start(context.Background())
	if status != StatusReady {
		t.Errorf("expected StatusReady when nerve already up, got %v", status)
	}
	if addr == "" {
		t.Error("expected non-empty addr on StatusReady")
	}
}

// ─── venv sentinel (venvIsValid / markVenvOK) ────────────────────────────────

func TestManager_VenvSentinel_InvalidBeforeMark(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{dir: dir}
	// Write a fake requirements.txt
	_ = os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("grpcio>=1.60.0\n"), 0o644)

	if m.venvIsValid() {
		t.Error("venvIsValid should return false before markVenvOK is called")
	}
}

func TestManager_VenvSentinel_ValidAfterMark(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{dir: dir}
	_ = os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("grpcio>=1.60.0\n"), 0o644)

	m.markVenvOK()

	if !m.venvIsValid() {
		t.Error("venvIsValid should return true after markVenvOK")
	}
}

func TestManager_VenvSentinel_InvalidatedByRequirementsChange(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{dir: dir}
	_ = os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("grpcio>=1.60.0\n"), 0o644)

	m.markVenvOK()
	if !m.venvIsValid() {
		t.Fatal("expected valid after mark")
	}

	// Simulate a new binary with updated requirements
	_ = os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("grpcio>=1.70.0\n"), 0o644)
	if m.venvIsValid() {
		t.Error("venvIsValid should return false after requirements.txt changes")
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func freePort(t *testing.T, lis net.Listener) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(lis.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil || port == 0 {
		t.Fatalf("could not parse port from %q", portStr)
	}
	return port
}
