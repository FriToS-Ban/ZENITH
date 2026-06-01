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

	if m.ping() {
		return addr, StatusReady
	}

	if err := m.Extract(); err != nil {
		return "", StatusLaunchFailed
	}

	python, err := m.FindPython()
	if err != nil {
		return "", StatusNoPython
	}

	if err := m.EnsureDeps(python); err != nil {
		return "", StatusDepsFailed
	}

	if err := m.Launch(); err != nil {
		return "", StatusLaunchFailed
	}

	if !m.WaitReady(context.Background(), 90*time.Second) {
		return "", StatusTimeout
	}

	return addr, StatusReady
}

// RequirementsHash returns the SHA-256 hex of the embedded requirements.txt.
// Used by the setup sentinel to detect when deps have changed after an update.
func (m *Manager) RequirementsHash() string {
	return fmt.Sprintf("%x", sha256.Sum256(embeddedReqs))
}

// ResetSetup deletes the venv_ok sentinel so EnsureDeps will reinstall packages
// on the next call. Called by 'zenith setup --force'.
func (m *Manager) ResetSetup() {
	_ = os.Remove(m.venvOKPath())
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

// Extract writes the embedded nerve Python assets to the manager's working directory.
func (m *Manager) Extract() error {
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

// FindPython locates a Python 3 interpreter in PATH and returns its absolute path.
func (m *Manager) FindPython() (string, error) {
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

func (m *Manager) venvUVPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(m.dir, "venv", "Scripts", "uv.exe")
	}
	return filepath.Join(m.dir, "venv", "bin", "uv")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// findSystemUV returns the path to a system-installed uv binary.
// It checks PATH first, then well-known installation locations on each OS
// (e.g. ~/.local/bin/uv on Linux/macOS, %LOCALAPPDATA%\uv\bin\uv.exe on Windows)
// so that uv is found even when the launching shell did not inherit the updated PATH.
func findSystemUV() string {
	if path, err := exec.LookPath("uv"); err == nil {
		return path
	}
	home, _ := os.UserHomeDir()
	var candidates []string
	if runtime.GOOS == "windows" {
		localAppData := os.Getenv("LOCALAPPDATA")
		candidates = []string{
			filepath.Join(localAppData, "uv", "bin", "uv.exe"),
			filepath.Join(home, ".local", "bin", "uv.exe"),
			filepath.Join(home, ".cargo", "bin", "uv.exe"),
		}
	} else {
		candidates = []string{
			filepath.Join(home, ".local", "bin", "uv"),
			filepath.Join(home, ".cargo", "bin", "uv"),
			"/usr/local/bin/uv",
		}
	}
	for _, p := range candidates {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

// resolveUV returns the path to a uv binary or "" if uv cannot be obtained.
// Priority: system install (PATH + known locations) → cached venv uv → bootstrap via venv pip.
// Bootstrap failure is non-fatal: caller falls back to plain pip.
func (m *Manager) resolveUV() string {
	if uv := findSystemUV(); uv != "" {
		return uv
	}
	if p := m.venvUVPath(); fileExists(p) {
		return p
	}
	// Bootstrap: install uv (~1 MB) into the venv so it can pull the heavy
	// packages (torch, sentence-transformers) in parallel.
	fmt.Fprintln(os.Stderr, "  nerve  bootstrapping uv package manager...")
	boot := exec.Command(m.venvPip(), "install", "--quiet", "uv")
	boot.Stdout = os.Stderr
	boot.Stderr = os.Stderr
	if err := boot.Run(); err != nil {
		return ""
	}
	if p := m.venvUVPath(); fileExists(p) {
		return p
	}
	return ""
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

// EnsureDeps creates the Python venv if absent and installs requirements.txt into it.
func (m *Manager) EnsureDeps(python string) error {
	if m.venvIsValid() {
		return nil
	}

	venvDir := filepath.Join(m.dir, "venv")

	// Create venv if missing. Prefer uv venv (faster) when uv is available.
	if _, err := os.Stat(m.venvPython()); err != nil {
		var createCmd *exec.Cmd
		if sysUV := findSystemUV(); sysUV != "" {
			createCmd = exec.Command(sysUV, "venv", "--python", python, venvDir)
		} else {
			createCmd = exec.Command(python, "-m", "venv", venvDir)
		}
		if out, err := createCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("venv: %w — %s", err, out)
		}
	}

	req := filepath.Join(m.dir, "requirements.txt")
	uv := m.resolveUV()

	// Install torch CPU-only first. The default PyPI torch is the CUDA build (~2.6 GB);
	// the CPU build from the PyTorch wheel index is ~260 MB and sufficient for inference.
	// pip/uv will skip torch in the requirements.txt pass below because it is already satisfied.
	const torchCPUIndex = "https://download.pytorch.org/whl/cpu"
	var torchCmd *exec.Cmd
	if uv != "" {
		torchCmd = exec.Command(uv, "pip", "install", "--python", m.venvPython(),
			"--index-url", torchCPUIndex, "torch>=2.3.0")
	} else {
		torchCmd = exec.Command(m.venvPip(), "install",
			"--prefer-binary", "--index-url", torchCPUIndex, "torch>=2.3.0")
	}
	torchCmd.Stdout = os.Stderr
	torchCmd.Stderr = os.Stderr
	if err := torchCmd.Run(); err != nil {
		return fmt.Errorf("torch install failed — see output above")
	}

	var installCmd *exec.Cmd
	if uv != "" {
		installCmd = exec.Command(uv, "pip", "install", "--python", m.venvPython(), "-r", req)
	} else {
		installCmd = exec.Command(m.venvPip(), "install", "--prefer-binary", "-r", req)
	}
	installCmd.Stdout = os.Stderr
	installCmd.Stderr = os.Stderr
	if err := installCmd.Run(); err != nil {
		return fmt.Errorf("pip install failed — see output above")
	}

	m.markVenvOK()
	return nil
}

// Launch starts the nerve gRPC sidecar as a detached background process.
func (m *Manager) Launch() error {
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

// WaitReady polls until the nerve gRPC port accepts connections or timeout elapses.
// Uses exponential backoff (10ms → 500ms cap). ctx cancellation exits early.
func (m *Manager) WaitReady(ctx context.Context, timeout time.Duration) bool {
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
