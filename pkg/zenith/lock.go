package zenith

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// inProcReg prevents two Open calls in the same process from targeting the
// same database file. Keyed by absolute path.
var inProcReg sync.Map // map[string]struct{}

type fileLock struct {
	absPath  string
	lockPath string
}

// acquireLock grabs both the in-process registry slot and a cross-process
// lock file at absPath+".lock". Returns ErrLocked immediately if either is
// already held. On success the caller must call release() when done.
//
// Stale lock files (left by a crashed process) are detected by reading the
// PID stored inside and checking whether that process is still alive.
func acquireLock(absPath string) (*fileLock, error) {
	// In-process check first — fast path.
	if _, loaded := inProcReg.LoadOrStore(absPath, struct{}{}); loaded {
		return nil, ErrLocked
	}

	lockPath := absPath + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		if !os.IsExist(err) {
			inProcReg.Delete(absPath)
			return nil, fmt.Errorf("zenith: lock file: %w", err)
		}

		// Lock file exists — check for stale lock (dead process).
		if stale := isStale(lockPath); !stale {
			inProcReg.Delete(absPath)
			return nil, ErrLocked
		}

		// Stale lock — remove and retry once.
		os.Remove(lockPath)
		f, err = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			inProcReg.Delete(absPath)
			return nil, ErrLocked
		}
	}

	fmt.Fprintf(f, "%d\n", os.Getpid())
	f.Close()

	return &fileLock{absPath: absPath, lockPath: lockPath}, nil
}

// release removes the lock file and frees the in-process registry slot.
func (l *fileLock) release() {
	os.Remove(l.lockPath)
	inProcReg.Delete(l.absPath)
}

// isStale reads the PID from the lock file and returns true if that process
// is no longer alive. Returns false (not stale) on any read error so we err
// on the side of caution.
func isStale(lockPath string) bool {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	return !processAlive(pid)
}
