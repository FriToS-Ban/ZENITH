package sstable

import (
	"fmt"
	"sync"
	"time"

	"github.com/shramanb113/ZENITH/internal/storage/memtable"
)

// GroupCommitter batches concurrent MemTable flush requests so that
// multiple goroutines waiting to write an SSTable share a single fsync.
//
// Problem: without batching, N concurrent flushes = N sequential fsyncs.
// Each fsync costs ~5–10ms on SSD. With the group committer, all flushes
// arriving within the commit window share one fsync.
//
// Design: first goroutine to arrive opens a group (leader). Subsequent
// arrivals join the same group. When the commit window timer fires, the
// leader merges all entries and writes one SSTable. All waiters unblock
// simultaneously via close(group.done).
type GroupCommitter struct {
	mu           sync.Mutex
	pending      *commitGroup
	commitWindow time.Duration
	pathGen      func() string
}

type commitGroup struct {
	requests []*flushRequest
	done     chan struct{}
	timer    *time.Timer
	result   commitResult
}

type flushRequest struct {
	entries []memtable.Entry
}

type commitResult struct {
	path string
	err  error
}

// NewGroupCommitter creates a GroupCommitter.
//
//   - commitWindow: how long to wait for more requests before flushing (default 4ms).
//   - pathGen: returns a unique file path per SSTable. Must be goroutine-safe.
func NewGroupCommitter(commitWindow time.Duration, pathGen func() string) *GroupCommitter {
	if commitWindow <= 0 {
		commitWindow = 4 * time.Millisecond
	}
	return &GroupCommitter{
		commitWindow: commitWindow,
		pathGen:      pathGen,
	}
}

// Submit adds entries to the current commit group and blocks until flushed.
// Returns (path, nil) on success or ("", err) on failure.
// Entries must be in lexicographic key order — MemTable.Iterator() guarantees this.
func (gc *GroupCommitter) Submit(entries []memtable.Entry) (string, error) {
	if len(entries) == 0 {
		return "", ErrEmptyTable
	}

	req := &flushRequest{entries: entries}

	gc.mu.Lock()
	if gc.pending == nil {
		gc.pending = &commitGroup{done: make(chan struct{})}
		group := gc.pending
		group.timer = time.AfterFunc(gc.commitWindow, func() {
			gc.commit(group)
		})
	}
	group := gc.pending
	group.requests = append(group.requests, req)
	gc.mu.Unlock()

	<-group.done
	return group.result.path, group.result.err
}

// commit seals the group, merges all entries, writes one SSTable, signals waiters.
func (gc *GroupCommitter) commit(group *commitGroup) {
	gc.mu.Lock()
	if group != gc.pending {
		gc.mu.Unlock()
		return
	}
	gc.pending = nil
	gc.mu.Unlock()

	if group.timer != nil {
		group.timer.Stop()
	}

	merged := mergeRequests(group.requests)

	var result commitResult
	if len(merged) == 0 {
		result.err = ErrEmptyTable
	} else {
		path := gc.pathGen()
		writer, err := NewWriter(path)
		if err != nil {
			result.err = fmt.Errorf("group committer: open writer: %w", err)
		} else if err := writer.WriteAll(merged); err != nil {
			writer.Close()
			result.err = fmt.Errorf("group committer: write: %w", err)
		} else if err := writer.Close(); err != nil {
			result.err = fmt.Errorf("group committer: close/fsync: %w", err)
		} else {
			result.path = path
		}
	}

	group.result = result
	close(group.done)
}

// mergeRequests merges sorted entry slices from all requests into one sorted slice.
// Last-writer-wins on duplicate keys — b is newer than a.
func mergeRequests(requests []*flushRequest) []memtable.Entry {
	if len(requests) == 0 {
		return nil
	}
	if len(requests) == 1 {
		return requests[0].entries
	}
	result := requests[0].entries
	for i := 1; i < len(requests); i++ {
		result = mergeTwoSorted(result, requests[i].entries)
	}
	return result
}

func mergeTwoSorted(a, b []memtable.Entry) []memtable.Entry {
	out := make([]memtable.Entry, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		cmp := compareBytes(a[i].Key, b[j].Key)
		switch {
		case cmp < 0:
			out = append(out, a[i])
			i++
		case cmp > 0:
			out = append(out, b[j])
			j++
		default:
			out = append(out, b[j]) // b wins — newer writer
			i++
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

func compareBytes(a, b []byte) int {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for k := 0; k < minLen; k++ {
		if a[k] < b[k] {
			return -1
		}
		if a[k] > b[k] {
			return 1
		}
	}
	return len(a) - len(b)
}
