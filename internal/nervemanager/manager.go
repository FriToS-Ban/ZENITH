// Package nervemanager embeds the nerve Python sidecar and manages its lifecycle.
// On the first zenith invocation it extracts the Python files to ~/.zenith/nerve/,
// creates an isolated virtualenv, installs dependencies, and starts the gRPC server
// on port 8000. Subsequent invocations reuse the running process if it is still alive.
package nervemanager

import (
	_ "embed"
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed assets/main.py
var embeddedMain []byte

//go:embed assets/requirements.txt
var embeddedReqs []byte

//go:embed assets/nerve_pb2.py
var embeddedPb2 []byte

//go:embed assets/nerve_pb2_grpc.py
var embeddedPb2Grpc []byte

const defaultPort = 8000

// Status describes the outcome of a Start call.
type Status int

const (
	StatusReady        Status = iota // nerve responded to a health ping
	StatusNoPython                   // python3 not found in PATH
	StatusDepsFailed                 // pip install returned an error
	StatusLaunchFailed               // exec.Start failed
	StatusTimeout                    // launched but did not respond within deadline
)

// Manager owns the nerve sidecar lifecycle for one zenith process.
type Manager struct {
	dir  string // ~/.zenith/nerve
	port int
}

// New returns a Manager rooted at ~/.zenith/nerve on port 8000.
func New() *Manager {
	home, _ := os.UserHomeDir()
	return &Manager{
		dir:  filepath.Join(home, ".zenith", "nerve"),
		port: defaultPort,
	}
}

// Addr returns the gRPC address of the nerve sidecar (e.g. "127.0.0.1:8000").
func (m *Manager) Addr() string {
	return fmt.Sprintf("127.0.0.1:%d", m.port)
}

// Start ensures nerve is running and healthy.
// It is safe to call concurrently — if nerve is already up it returns immediately.
func (m *Manager) Start(ctx context.Context) (addr string, s Status) {
	addr = m.Addr()

	// Fast path: already running.
	if m.ping() {
		return addr, StatusReady
	}

	if err := m.extract(); err != nil {
		return "", StatusLaunchFailed
	}

	python, err := m.findPython()
	if err != nil {
		return "", StatusNoPython
	}

	if err := m.ensureDeps(python); err != nil {
		return "", StatusDepsFailed
	}

	if err := m.launch(); err != nil {
		return "", StatusLaunchFailed
	}

	// The gRPC server binds to its port within a second — models load lazily
	// on first request, so 20s is ample even on a fresh install.
	if !m.waitReady(ctx, 20*time.Second) {
		return "", StatusTimeout
	}

	return addr, StatusReady
}

// ─── private ─────────────────────────────────────────────────────────────────

// ping checks whether the nerve gRPC port is accepting TCP connections.
func (m *Manager) ping() bool {
	conn, err := net.DialTimeout("tcp", m.Addr(), 400*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (m *Manager) extract() error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	files := map[string][]byte{
		"main.py":           embeddedMain,
		"requirements.txt":  embeddedReqs,
		"nerve_pb2.py":      embeddedPb2,
		"nerve_pb2_grpc.py": embeddedPb2Grpc,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(m.dir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) findPython() (string, error) {
	for _, c := range []string{"python3", "python"} {
		path, err := exec.LookPath(c)
		if err != nil {
			continue
		}
		out, err := exec.Command(path, "--version").Output()
		if err == nil && strings.HasPrefix(string(out), "Python 3") {
			return path, nil
		}
	}
	return "", fmt.Errorf("python3 not found in PATH")
}

func (m *Manager) venvPython() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(m.dir, "venv", "Scripts", "python.exe")
	}
	return filepath.Join(m.dir, "venv", "bin", "python3")
}

func (m *Manager) venvPip() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(m.dir, "venv", "Scripts", "pip.exe")
	}
	return filepath.Join(m.dir, "venv", "bin", "pip")
}

// venvOKPath is the sentinel file that caches successful dep installs.
func (m *Manager) venvOKPath() string {
	return filepath.Join(m.dir, "venv_ok")
}

// venvIsValid returns true if the sentinel hash matches the current requirements.txt,
// meaning deps are already installed and up-to-date. Skips the slow Python subprocess.
func (m *Manager) venvIsValid() bool {
	reqData, err := os.ReadFile(filepath.Join(m.dir, "requirements.txt"))
	if err != nil {
		return false
	}
	want := fmt.Sprintf("%x", sha256.Sum256(reqData))
	got, err := os.ReadFile(m.venvOKPath())
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(got)) == want
}

// markVenvOK writes the SHA-256 of the current requirements.txt into the sentinel.
// Called after a successful pip install so subsequent starts skip re-validation.
func (m *Manager) markVenvOK() {
	reqData, err := os.ReadFile(filepath.Join(m.dir, "requirements.txt"))
	if err != nil {
		return
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(reqData))
	_ = os.WriteFile(m.venvOKPath(), []byte(hash), 0o644)
}

func (m *Manager) ensureDeps(python string) error {
	// Improvement 2: fast path — skip Python subprocess if deps are cached.
	if m.venvIsValid() {
		return nil
	}

	venvDir := filepath.Join(m.dir, "venv")
	if _, err := os.Stat(m.venvPython()); err != nil {
		// Venv missing — create it.
		if out, err := exec.Command(python, "-m", "venv", venvDir).CombinedOutput(); err != nil {
			return fmt.Errorf("venv: %w — %s", err, out)
		}
	}

	// Improvement 1: stream pip output to stderr so users see progress.
	req := filepath.Join(m.dir, "requirements.txt")
	cmd := exec.Command(m.venvPip(), "install", "-r", req)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pip install failed — see output above")
	}

	m.markVenvOK()
	return nil
}

func (m *Manager) launch() error {
	logPath := filepath.Join(m.dir, "nerve.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logFile, _ = os.Open(os.DevNull)
	}

	cmd := exec.Command(m.venvPython(), "main.py")
	cmd.Dir = m.dir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detach(cmd)
	return cmd.Start()
}

// waitReady polls until the nerve gRPC port accepts connections or the deadline passes.
// Improvement 5: exponential backoff (10ms → 500ms) reduces latency on fast machines
// where nerve is already running or starts quickly.
func (m *Manager) waitReady(ctx context.Context, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	delay := 10 * time.Millisecond
	const maxDelay = 500 * time.Millisecond
	for time.Now().Before(deadline) {
		if m.ping() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(delay):
		}
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
	return false
}
