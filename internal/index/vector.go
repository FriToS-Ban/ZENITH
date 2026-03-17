package index

import "sync"

type VectorStore struct {
	mu          sync.RWMutex
	vectors     map[uint32][]float32
	wordVectors map[string][]float32
}

func NewVectorStore() *VectorStore {
	return &VectorStore{
		vectors:     make(map[uint32][]float32),
		wordVectors: make(map[string][]float32),
	}
}

func (vs *VectorStore) RLock()   { vs.mu.RLock() }
func (vs *VectorStore) RUnlock() { vs.mu.RUnlock() }
func (vs *VectorStore) Lock()    { vs.mu.Lock() }
func (vs *VectorStore) Unlock()  { vs.mu.Unlock() }

func (vs *VectorStore) GetVectors() map[uint32][]float32       { return vs.vectors }
func (vs *VectorStore) GetWordVectors() map[string][]float32   { return vs.wordVectors }
func (vs *VectorStore) HasWordVector(word string) bool {
	vs.mu.RLock()
	defer vs.mu.RUnlock()
	_, exists := vs.wordVectors[word]
	return exists
}
