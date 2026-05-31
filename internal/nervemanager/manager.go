// Package nervemanager embeds the nerve Python sidecar and manages its lifecycle.
// On the first zenith invocation it extracts main.py to ~/.zenith/nerve/, creates
// an isolated virtualenv, installs dependencies, and starts uvicorn on port 8000.
// Subsequent invocations reuse the running process if it is still alive.
package nervemanager

import (
	_ "embed"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

//go:embed assets/main.py
var embeddedMain []byte

//go:embed assets/requirements.txt
var embeddedReqs []byte

const (
	defaultPort    = 8000
	healthEndpoint = "/health"
)

// Status describes the outcome of a Start call.
type Status int

const (
	StatusReady        Status = iota // nerve responded to a health ping
	StatusNoPython                   // python3 not found in PATH
	StatusDepsFailed                 // pip install returned an error
	StatusLaunchFailed               // uvicorn exec.Start failed
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

// URL returns the nerve HTTP base URL.
func (m *Manager) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", m.port)
}

// Start ensures nerve is running and healthy.
// It is safe to call concurrently — if nerve is already up the function returns
// immediately without launching a second instance.
func (m *Manager) Start(ctx context.Context) (url string, s Status) {
	url = m.URL()

	// Fast path: already running (previous session or explicit start).
	if m.ping() {
		return url, StatusReady
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

	timeout := 20 * time.Second
	if !m.venvExists() {
		// First run: pip already ran above, but model download happens on first
		// embed call inside nerve. Give the server more time to bind.
		timeout = 45 * time.Second
	}

	if !m.waitReady(ctx, timeout) {
		return "", StatusTimeout
	}

	return url, StatusReady
}

// ─── private ─────────────────────────────────────────────────────────────────

func (m *Manager) ping() bool {
	c := &http.Client{Timeout: 400 * time.Millisecond}
	resp, err := c.Get(m.URL() + healthEndpoint)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (m *Manager) extract() error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(m.dir, "main.py"), embeddedMain, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.dir, "requirements.txt"), embeddedReqs, 0o644)
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

func (m *Manager) venvExists() bool {
	_, err := os.Stat(m.venvPython())
	return err == nil
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

func (m *Manager) venvUvicorn() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(m.dir, "venv", "Scripts", "uvicorn.exe")
	}
	return filepath.Join(m.dir, "venv", "bin", "uvicorn")
}

func (m *Manager) ensureDeps(python string) error {
	vp := m.venvPython()

	// Reuse existing venv if all imports succeed.
	if _, err := os.Stat(vp); err == nil {
		chk := exec.Command(vp, "-c", "import fastapi, uvicorn, sentence_transformers")
		if chk.Run() == nil {
			return nil
		}
	}

	// Create isolated virtualenv.
	venvDir := filepath.Join(m.dir, "venv")
	if out, err := exec.Command(python, "-m", "venv", venvDir).CombinedOutput(); err != nil {
		return fmt.Errorf("venv: %w — %s", err, out)
	}

	// Install dependencies into the venv.
	req := filepath.Join(m.dir, "requirements.txt")
	out, err := exec.Command(m.venvPip(), "install", "-q", "-r", req).CombinedOutput()
	if err != nil {
		return fmt.Errorf("pip install: %w — %s", err, out)
	}
	return nil
}

func (m *Manager) launch() error {
	logPath := filepath.Join(m.dir, "nerve.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logFile, _ = os.Open(os.DevNull)
	}

	cmd := exec.Command(
		m.venvUvicorn(),
		"main:app",
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(m.port),
		"--log-level", "warning",
	)
	cmd.Dir = m.dir
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Platform-specific: detach from zenith's process group so nerve outlives
	// the parent process and is reusable by the next zenith invocation.
	detach(cmd)

	return cmd.Start()
}

func (m *Manager) waitReady(ctx context.Context, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m.ping() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
	return false
}
