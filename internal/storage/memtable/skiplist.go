package memtable

import (
	"bytes"
	"math/rand/v2"
	"sync"
	"sync/atomic"
)

// skipList is a concurrent probabilistic skip-list ordered by byte key.
// It is used as the sorted in-memory store inside MemTable, replacing the
// previous hashmap implementation.
//
// Properties:
//   - O(log n) average insert, lookup, and delete
//   - Keys are kept in lexicographic order at all times — Iterator() requires
//     no sort step, unlike the hashmap which had to sort on every call
//   - Lock granularity: one RWMutex over the full list. Fine for a MemTable
//     where the bottleneck is WAL fsync, not skip-list contention.
//
// MaxLevel and probability are tuned for a 64MB MemTable holding ~1M keys.
const (
	slMaxLevel = 20   // supports 2^20 ≈ 1M keys at p=0.25
	slP        = 0.25 // promotion probability
)

type slNode struct {
	key     []byte
	value   []byte
	deleted bool
	next    []*slNode // next[i] = forward pointer at level i
}

type skipList struct {
	mu    sync.RWMutex
	head  *slNode // sentinel head — never holds real data
	level int     // current highest level in use (1-indexed)
	count atomic.Int64
}

func newSkipList() *skipList {
	head := &slNode{next: make([]*slNode, slMaxLevel)}
	return &skipList{head: head, level: 1}
}

// randomLevel generates a level for a new node using geometric distribution.
func randomLevel() int {
	level := 1
	for level < slMaxLevel && rand.Float64() < slP {
		level++
	}
	return level
}

// findPredecessors returns the predecessor nodes at each level for key.
// prev[i] is the last node at level i whose key < key.
// Must be called with at least a read lock held.
func (s *skipList) findPredecessors(key []byte) [slMaxLevel]*slNode {
	var prev [slMaxLevel]*slNode
	cur := s.head
	for i := s.level - 1; i >= 0; i-- {
		for cur.next[i] != nil && bytes.Compare(cur.next[i].key, key) < 0 {
			cur = cur.next[i]
		}
		prev[i] = cur
	}
	return prev
}

// put inserts or updates key→value. deleted=false marks a live entry.
// deleted=true inserts a tombstone (for Delete operations).
// Returns the net byte delta (positive = grew, negative = shrank).
func (s *skipList) put(key, value []byte, deleted bool) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	prev := s.findPredecessors(key)

	// Check if key already exists at level 0.
	existing := prev[0].next[0]
	if existing != nil && bytes.Equal(existing.key, key) {
		delta := int64(len(value)) - int64(len(existing.value))
		existing.value = value
		existing.deleted = deleted
		return delta
	}

	// New node.
	level := randomLevel()
	node := &slNode{
		key:     key,
		value:   value,
		deleted: deleted,
		next:    make([]*slNode, level),
	}

	// Extend head's forward pointers if new node exceeds current max level.
	if level > s.level {
		for i := s.level; i < level; i++ {
			prev[i] = s.head
		}
		s.level = level
	}

	for i := 0; i < level; i++ {
		node.next[i] = prev[i].next[i]
		prev[i].next[i] = node
	}

	s.count.Add(1)
	return int64(len(key)) + int64(len(value))
}

// get returns (value, exists, deleted).
// exists=false means the key was never inserted.
// deleted=true means a tombstone is present.
func (s *skipList) get(key []byte) (value []byte, exists bool, deleted bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cur := s.head
	for i := s.level - 1; i >= 0; i-- {
		for cur.next[i] != nil && bytes.Compare(cur.next[i].key, key) < 0 {
			cur = cur.next[i]
		}
	}

	node := cur.next[0]
	if node == nil || !bytes.Equal(node.key, key) {
		return nil, false, false
	}

	if node.deleted {
		return nil, true, true
	}

	val := make([]byte, len(node.value))
	copy(val, node.value)
	return val, true, false
}

// iterate returns all entries in lexicographic key order.
// Takes a read lock for the snapshot then releases it.
func (s *skipList) iterate() []Entry {
	s.mu.RLock()
	count := int(s.count.Load())
	entries := make([]Entry, 0, count)

	cur := s.head.next[0]
	for cur != nil {
		key := make([]byte, len(cur.key))
		copy(key, cur.key)
		var val []byte
		if !cur.deleted && len(cur.value) > 0 {
			val = make([]byte, len(cur.value))
			copy(val, cur.value)
		}
		entries = append(entries, Entry{
			Key:     key,
			Value:   val,
			Deleted: cur.deleted,
		})
		cur = cur.next[0]
	}
	s.mu.RUnlock()
	return entries
}

// len returns the number of nodes (including tombstones).
func (s *skipList) len() int {
	return int(s.count.Load())
}
