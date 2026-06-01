// Package nervemanager embeds the nerve Python sidecar and manages its lifecycle.
// On the first zenith invocation it extracts the Python files to ~/.zenith/nerve/,
// creates an isolated virtualenv, installs dependencies, and starts the gRPC server
// on port 8000. Subsequent invocations reuse the running process if it is still alive
// and running the current embedded code; otherwise it kills and restarts it.
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
	"strconv"
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

// LogPath returns the absolute path to the nerve log file.
func (m *Manager) LogPath() string {
	return filepath.Join(m.dir, "nerve.log")
}

// Start ensures nerve is running with the current embedded code.
// If nerve is already up but running stale code it is killed and restarted.
func (m *Manager) Start(ctx context.Context) (addr string, s Status) {
	addr = m.Addr()

	if m.ping() {
		if m.NerveCodeUpToDate() {
			return addr, StatusReady
		}
		// Running with stale code — kill and restart with current main.py.
		_ = m.Kill()
		time.Sleep(500 * time.Millisecond)
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

	m.markNerveVersionOK()
	return addr, StatusReady
}

// nerveCodeHash returns a combined SHA-256 of the embedded main.py and requirements.txt.
// It changes whenever either file is updated, triggering re-setup and nerve restart.
func (m *Manager) nerveCodeHash() string {
	h := sha256.New()
	h.Write(embeddedMain)
	h.Write(embeddedReqs)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// RequirementsHash returns the combined hash of the embedded nerve code (main.py +
// requirements.txt). Used by the setup sentinel so changes to either file prompt
// users to re-run 'zenith setup'.
func (m *Manager) RequirementsHash() string {
	return m.nerveCodeHash()
}

// NerveCodeUpToDate returns true when the nerve process that is (or was last) running
// was started with the current embedded main.py and requirements.txt.
func (m *Manager) NerveCodeUpToDate() bool {
	got, err := os.ReadFile(m.nerveVersionPath())
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(got)) == m.nerveCodeHash()
}

// MarkNerveVersionOK records that nerve is now running with the current embedded code.
// Called after a successful WaitReady so subsequent Start() calls can skip restart.
func (m *Manager) MarkNerveVersionOK() {
	m.markNerveVersionOK()
}

// Kill terminates a running nerve process using the saved PID file.
// Returns nil if no PID file exists (nerve not managed by this binary).
func (m *Manager) Kill() error {
	data, err := os.ReadFile(m.pidPath())
	if err != nil {
		return nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	_ = proc.Kill()
	_ = os.Remove(m.pidPath())
	return nil
}

// ResetSetup wipes all setup state: venv_ok sentinel, venv directory, and nerve
// version marker. Called by 'zenith setup --force' to guarantee a clean slate.
func (m *Manager) ResetSetup() {
	_ = os.Remove(m.venvOKPath())
	_ = os.RemoveAll(filepath.Join(m.dir, "venv"))
	_ = os.Remove(m.nerveVersionPath())
}

// ─── private ─────────────────────────────────────────────────────────────────

func (m *Manager) ping() bool {
	conn, err := net.DialTimeout("tcp", m.Addr(), 400*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (m *Manager) pidPath() string {
	return filepath.Join(m.dir, "nerve.pid")
}

func (m *Manager) nerveVersionPath() string {
	return filepath.Join(m.dir, "nerve_version")
}

func (m *Manager) markNerveVersionOK() {
	_ = os.WriteFile(m.nerveVersionPath(), []byte(m.nerveCodeHash()), 0o644)
}

func (m *Manager) savePID(pid int) {
	_ = os.WriteFile(m.pidPath(), []byte(strconv.Itoa(pid)), 0o644)
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
// Checks PATH first, then well-known install locations so uv is found even
// when the launching shell did not inherit the updated PATH.
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
func (m *Manager) resolveUV() string {
	if uv := findSystemUV(); uv != "" {
		return uv
	}
	if p := m.venvUVPath(); fileExists(p) {
		return p
	}
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

func (m *Manager) venvOKPath() string {
	return filepath.Join(m.dir, "venv_ok")
}

// venvIsValid returns true when the venv binary exists on disk AND the sentinel hash
// matches the current requirements.txt. The existence check catches venvs that were
// deleted or quarantined by antivirus while the sentinel was still present.
func (m *Manager) venvIsValid() bool {
	if !fileExists(m.venvPython()) {
		return false
	}
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
	if !fileExists(m.venvPython()) {
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
	// pip/uv skips torch in the requirements.txt pass below because it is already satisfied.
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

// Launch starts the nerve gRPC sidecar as a detached background process and saves
// its PID so Kill() can terminate it later.
func (m *Manager) Launch() error {
	logFile, err := os.OpenFile(m.LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logFile, err = os.Open(os.DevNull)
		if err != nil {
			return fmt.Errorf("nerve: could not open log or devnull: %w", err)
		}
	}

	cmd := exec.Command(m.venvPython(), "main.py")
	cmd.Dir = m.dir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	m.savePID(cmd.Process.Pid)
	return nil
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
