package index

import "sync"

type VectorEntry struct {
	Vector    []float32
	Magnitude float64
}

type VectorStore struct {
	mu          sync.RWMutex
	vectors     map[uint32]VectorEntry
	wordVectors map[string]VectorEntry
}

func NewVectorStore() *VectorStore {
	return &VectorStore{
		vectors:     make(map[uint32]VectorEntry),
		wordVectors: make(map[string]VectorEntry),
	}
}

func (vs *VectorStore) RLock()   { vs.mu.RLock() }
func (vs *VectorStore) RUnlock() { vs.mu.RUnlock() }
func (vs *VectorStore) Lock()    { vs.mu.Lock() }
func (vs *VectorStore) Unlock()  { vs.mu.Unlock() }

func (vs *VectorStore) GetVectors() map[uint32]VectorEntry       { return vs.vectors }
func (vs *VectorStore) GetWordVectors() map[string]VectorEntry   { return vs.wordVectors }
func (vs *VectorStore) HasWordVector(word string) bool {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	_, exists := vs.wordVectors[word]
	return exists
}
