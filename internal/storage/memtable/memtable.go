// Building Memtable using Maps for now will implement skiplist in future

package memtable

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/shramanb113/ZENITH/internal/storage/wal"
)

var (
	// ErrFrozen is returned when a write is attempted on a frozen MemTable.
	ErrFrozen = errors.New("memtable: is frozen and cannot accept writes")

	// ErrKeyEmpty is returned when a nil or zero-length key is provided.
	ErrKeyEmpty = errors.New("memtable: key cannot be empty")
)

const DefaultMaxSize = 64 * 1024 * 1024 // 64MB (Max Limit for Temp memory)

type MemTable struct {
	mu      sync.RWMutex
	data    map[string]entry
	size    atomic.Int64
	maxSize int64

	// frozen is set to true when size >= maxSize.
	// Once frozen, no further writes are accepted.
	// The engine must create a new active MemTable and schedule this
	// one for flushing to an SSTable.
	frozen atomic.Bool
}

type entry struct {
	value   []byte
	deleted bool
}

func NewMemTable(maxSize int64) *MemTable {

	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}

	return &MemTable{
		maxSize: maxSize,
		data:    make(map[string]entry),
	}
}

func (m *MemTable) Put(key, value []byte) error { // returns ErrFrozen if frozen

	if len(key) == 0 {
		return ErrKeyEmpty
	}

	if m.frozen.Load() {
		return ErrFrozen
	}

	k := string(key)
	v := make([]byte, len(value))
	copy(v, value)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.frozen.Load() {
		return ErrFrozen
	}

	prev, exists := m.data[k]
	m.data[k] = entry{value: v, deleted: false}

	if !exists {
		m.size.Add(int64(len(k)) + int64(len(v)))
	} else {
		m.size.Add(int64(len(v)) - int64(len(prev.value)))
	}

	if m.Size() >= m.maxSize {
		m.frozen.Store(true)
	}

	return nil

}

func (m *MemTable) Delete(key []byte) error { // stores tombstone

	if len(key) == 0 {
		return ErrKeyEmpty
	}

	if m.IsFrozen() {
		return ErrFrozen
	}

	k := string(key)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.IsFrozen() {
		return ErrFrozen
	}

	value, exists := m.data[k]

	m.data[k] = entry{deleted: true}

	if !exists {
		// wastage value
		m.size.Add(int64(len(k)))
	} else {
		m.size.Add(-int64(len(value.value)))
	}

	if m.maxSize <= m.Size() {
		m.frozen.Store(true)
	}

	return nil
}

func (m *MemTable) Get(key []byte) ([]byte, bool) { // false if missing or deleted

	if len(key) == 0 {
		return nil, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	e, exists := m.data[string(key)]

	if !exists || e.deleted {
		return nil, false
	}

	result := make([]byte, len(e.value))

	copy(result, e.value)

	return result, true
}

func (m *MemTable) IsFrozen() bool {
	return m.frozen.Load()
}

func (m *MemTable) Size() int64 {
	return m.size.Load()
}

// SSTable data structure
type Entry struct {
	Key     []byte
	Value   []byte // nil if Deleted is true
	Deleted bool   // true = tombstone, must be written to SSTable
	Seq     uint64 // sequence number, set from WAL — used to resolve conflicts during compaction
}

func (m *MemTable) Iterator() []Entry {
	m.mu.RLock()
	snapshot := make(map[string]entry, len(m.data))

	for k, v := range m.data {
		snapshot[k] = v
	}

	m.mu.RUnlock()

	// Sort keys for SSTable — SSTables require keys in lexicographic order.
	keys := make([]string, 0, len(snapshot))
	for k := range snapshot {
		keys = append(keys, k)
	}
	slices.SortFunc(keys,
		func(a, b string) int {
			return cmp.Compare(a, b)
		},
	)

	entries := make([]Entry, 0, len(keys))
	for _, k := range keys {
		e := snapshot[k]
		entries = append(entries, Entry{
			Key:     []byte(k),
			Value:   e.value,
			Deleted: e.deleted,
		})
	}
	return entries
}

// called during WAL recovery and in your engine's Write method
func ApplyRecord(m *MemTable, r wal.Record) error {
	switch r.Op {
	case wal.OpTypePut:
		return m.Put(r.Key, r.Value)
	case wal.OpTypeDelete:
		return m.Delete(r.Key)
	default:
		return fmt.Errorf("memtable: unknown op type %d at seq %d", r.Op, r.Seq)
	}
}
