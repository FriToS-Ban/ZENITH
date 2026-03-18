package memtable

import (
	"sync"
	"sync/atomic"

	"github.com/shramanb113/ZENITH/internal/storage/wal"
)

type MemTable struct {
    mu      sync.RWMutex
    data    map[string]entry
    size    int64
    maxSize int64
    frozen  atomic.Bool
}

type entry struct {
    value   []byte
    deleted bool  
}


// memtable.go
func NewMemTable(maxSize int64) *MemTable

func (m *MemTable) Put(key, value []byte) error   // returns ErrFrozen if frozen
func (m *MemTable) Delete(key []byte) error        // stores tombstone
func (m *MemTable) Get(key []byte) ([]byte, bool)  // false if missing or deleted
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