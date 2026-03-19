// Building Memtable using Maps for now will implement skiplist in future

package memtable

import (
	"errors"
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
	size    int64
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

func NewMemTable(maxSize int64) *MemTable

func (m *MemTable) Put(key, value []byte) error // returns ErrFrozen if frozen
func (m *MemTable) Delete(key []byte) error     // stores tombstone
func (m *MemTable) Get(key []byte) ([]byte, bool) { // false if missing or deleted

	if len(key) == 0 {
		return nil, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	e, exists := m.data[string(key)]

	if !exists || e.deleted {
		return nil, false
	}

	result := make([]byte, len(e.value))

	copy(result, e.value)

	return result, true
}
func (m *MemTable) IsFrozen() bool
func (m *MemTable) Size() int64

// called during WAL recovery and in your engine's Write method
func ApplyRecord(m *MemTable, r wal.Record) error {
	switch r.Op {
	case wal.OpTypePut:
		return m.Put(r.Key, r.Value)
	case wal.OpTypeDelete:
		return m.Delete(r.Key)
	}
	return nil
}
