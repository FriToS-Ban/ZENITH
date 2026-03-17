package index

import (
	"sync"

	"github.com/x448/float16"
)

type VectorEntry struct {
	Vector    []uint16 // Stored as float16 representation
	Magnitude float64
}

// Memory optimization helpers
func FloatsToFloat16(vec []float32) []uint16 {
	out := make([]uint16, len(vec))
	for i, v := range vec {
		out[i] = float16.Fromfloat32(v).Bits()
	}
	return out
}

func Float16ToFloats(vec []uint16) []float32 {
	out := make([]float32, len(vec))
	for i, v := range vec {
		out[i] = float16.Frombits(v).Float32()
	}
	return out
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
