package index

import "sync"

type InvertedIndex struct {
	mu           sync.RWMutex
	data         map[string][]uint32
	tokenCounts  map[string]int
	vocabulary   map[int][]string
	globalSeen   map[string]bool
	docFragments map[uint32][]string // Fragment tracking for idempotency
}

func NewInvertedIndex() *InvertedIndex {
	return &InvertedIndex{
		data:         make(map[string][]uint32),
		tokenCounts:  make(map[string]int),
		vocabulary:   make(map[int][]string),
		globalSeen:   make(map[string]bool),
		docFragments: make(map[uint32][]string),
	}
}

func (idx *InvertedIndex) RLock()   { idx.mu.RLock() }
func (idx *InvertedIndex) RUnlock() { idx.mu.RUnlock() }
func (idx *InvertedIndex) Lock()    { idx.mu.Lock() }
func (idx *InvertedIndex) Unlock()  { idx.mu.Unlock() }

// Helpers logic extracted from engine
func (idx *InvertedIndex) GetTokenCounts() map[string]int  { return idx.tokenCounts }
func (idx *InvertedIndex) GetGlobalSeen() map[string]bool  { return idx.globalSeen }
func (idx *InvertedIndex) GetDocFragments() map[uint32][]string { return idx.docFragments }
func (idx *InvertedIndex) GetVocabulary() map[int][]string { return idx.vocabulary }
func (idx *InvertedIndex) GetData() map[string][]uint32    { return idx.data }
