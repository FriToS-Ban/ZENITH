package memtable

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"github.com/shramanb113/ZENITH/internal/storage/wal"
)

func TestMemTable_PutAndGet(t *testing.T) {
	m := NewMemTable(DefaultMaxSize)

	if err := m.Put([]byte("key1"), []byte("value1")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	val, ok := m.Get([]byte("key1"))
	if !ok {
		t.Fatal("Get returned false for existing key")
	}
	if string(val) != "value1" {
		t.Errorf("Get = %q, want %q", val, "value1")
	}
}

func TestMemTable_GetMissing(t *testing.T) {
	m := NewMemTable(DefaultMaxSize)
	_, ok := m.Get([]byte("absent"))
	if ok {
		t.Error("Get should return false for absent key")
	}
}

func TestMemTable_PutOverwrite(t *testing.T) {
	m := NewMemTable(DefaultMaxSize)
	_ = m.Put([]byte("k"), []byte("v1"))
	_ = m.Put([]byte("k"), []byte("v2"))

	val, ok := m.Get([]byte("k"))
	if !ok || string(val) != "v2" {
		t.Errorf("expected overwritten value 'v2', got %q (ok=%v)", val, ok)
	}
}

func TestMemTable_Delete(t *testing.T) {
	m := NewMemTable(DefaultMaxSize)
	_ = m.Put([]byte("key"), []byte("val"))
	if err := m.Delete([]byte("key")); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, ok := m.Get([]byte("key"))
	if ok {
		t.Error("Get should return false after Delete")
	}
}

func TestMemTable_EmptyKey(t *testing.T) {
	m := NewMemTable(DefaultMaxSize)
	if err := m.Put([]byte{}, []byte("v")); err != ErrKeyEmpty {
		t.Errorf("expected ErrKeyEmpty for empty key, got %v", err)
	}
	if err := m.Delete([]byte{}); err != ErrKeyEmpty {
		t.Errorf("expected ErrKeyEmpty for empty delete key, got %v", err)
	}
	_, ok := m.Get([]byte{})
	if ok {
		t.Error("Get with empty key should return false")
	}
}

func TestMemTable_FreezeOnSizeExceeded(t *testing.T) {
	// Use a tiny maxSize so a single put triggers the freeze.
	m := NewMemTable(10)
	_ = m.Put([]byte("key"), []byte("this_value_exceeds_ten_bytes"))
	if !m.IsFrozen() {
		t.Error("expected memtable to be frozen after size exceeded")
	}

	err := m.Put([]byte("new"), []byte("entry"))
	if err != ErrFrozen {
		t.Errorf("expected ErrFrozen, got %v", err)
	}
}

func TestMemTable_IteratorSorted(t *testing.T) {
	m := NewMemTable(DefaultMaxSize)
	keys := []string{"zebra", "apple", "mango", "kiwi", "banana"}
	for _, k := range keys {
		_ = m.Put([]byte(k), []byte("v"))
	}

	entries := m.Iterator()
	for i := 1; i < len(entries); i++ {
		if bytes.Compare(entries[i-1].Key, entries[i].Key) > 0 {
			t.Errorf("iterator not sorted: %q > %q at index %d",
				entries[i-1].Key, entries[i].Key, i)
		}
	}
}

func TestMemTable_IteratorIncludesTombstones(t *testing.T) {
	m := NewMemTable(DefaultMaxSize)
	_ = m.Put([]byte("live"), []byte("yes"))
	_ = m.Put([]byte("dead"), []byte("yes"))
	_ = m.Delete([]byte("dead"))

	entries := m.Iterator()
	tombstoneFound := false
	for _, e := range entries {
		if string(e.Key) == "dead" && e.Deleted {
			tombstoneFound = true
		}
	}
	if !tombstoneFound {
		t.Error("iterator should include tombstone for deleted key")
	}
}

func TestMemTable_ConcurrentPut(t *testing.T) {
	m := NewMemTable(DefaultMaxSize)
	var wg sync.WaitGroup
	n := 200
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("key%04d", i)
			_ = m.Put([]byte(key), []byte("v"))
		}(i)
	}
	wg.Wait()

	// Verify all entries were written.
	entries := m.Iterator()
	if len(entries) != n {
		t.Errorf("expected %d entries after concurrent puts, got %d", n, len(entries))
	}
}

func TestMemTable_ApplyRecord_Put(t *testing.T) {
	m := NewMemTable(DefaultMaxSize)
	r := wal.Record{Seq: 1, Op: wal.OpTypePut, Key: []byte("rkey"), Value: []byte("rval")}
	if err := ApplyRecord(m, r); err != nil {
		t.Fatalf("ApplyRecord(Put): %v", err)
	}
	val, ok := m.Get([]byte("rkey"))
	if !ok || string(val) != "rval" {
		t.Errorf("after ApplyRecord(Put): get=%q ok=%v", val, ok)
	}
}

func TestMemTable_ApplyRecord_Delete(t *testing.T) {
	m := NewMemTable(DefaultMaxSize)
	_ = m.Put([]byte("rkey"), []byte("rval"))
	r := wal.Record{Seq: 2, Op: wal.OpTypeDelete, Key: []byte("rkey")}
	if err := ApplyRecord(m, r); err != nil {
		t.Fatalf("ApplyRecord(Delete): %v", err)
	}
	_, ok := m.Get([]byte("rkey"))
	if ok {
		t.Error("key should be absent after ApplyRecord(Delete)")
	}
}

func TestMemTable_SizeTracking(t *testing.T) {
	m := NewMemTable(DefaultMaxSize)
	if m.Size() != 0 {
		t.Errorf("initial size = %d, want 0", m.Size())
	}
	_ = m.Put([]byte("k"), []byte("v"))
	if m.Size() == 0 {
		t.Error("size should be positive after a Put")
	}
}
