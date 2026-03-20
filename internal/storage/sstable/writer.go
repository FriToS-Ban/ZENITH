package sstable

import (
	"bufio"
	"encoding/binary"
	"hash/fnv"
	"math"
	"os"

	"github.com/shramanb113/ZENITH/internal/storage/memtable"
)

// Reader

// Bloom Filter
type bloomFilter struct {
	bit     []uint64
	numBits uint32
	numHash uint8
}

// Bloom filter dataset initialization
func newBloomFilter(expectedKeys int) *bloomFilter {

	if expectedKeys == 0 {
		expectedKeys = 1
	}

	//expectedKeys := number of items i want to store in the db
	//numBits := total size of the filter
	// p = 0.01 that is the probability we want for false positive
	//Target 1% false positive rate: −n·ln(p) / ln(2)²

	numBits := uint32(math.Ceil(-float64(expectedKeys) * math.Log(0.01) / (math.Ln2 * math.Ln2)))

	if numBits < 64 {
		numBits = 64
	}

	// number of Hash function
	//  numhash := expectedKeys * ln2 / numbits
	numHash := uint8(float64(expectedKeys) * math.Ln2 / float64(numBits))

	if numHash < 1 {
		numHash = 1
	}

	return &bloomFilter{
		bit:     make([]uint64, (numBits+63)/64),
		numHash: numHash,
		numBits: numBits,
	}

}

// now converting the keys into hash using hash functions
// very simple hash function is not very complete but just for bulding phase (will update it later)
func bloomHash(key []byte) (uint64, uint64) {
	h := fnv.New64a()
	h.Sum(key)
	h1 := h.Sum64()

	h2 := h1*31 + 17

	if h2 == 0 {
		h2 = 1
	}

	return h1, h2

}

func (b *bloomFilter) add(key []byte) {
	h1, h2 := bloomHash(key)
	for i := uint8(0); i < b.numHash; i++ {
		pos := (h1 + uint64(i)*h2) % uint64(b.numBits)
		b.bit[pos/64] |= 1 << (pos % 64)
	}
}

// encoding for file sotrage in bufio writer
func (b *bloomFilter) encode() []byte {

	// 1 -> numHash
	// 4 -> numBits
	// len(b.bit)*8 -> since 8bytes and length is the capacity needed

	buf := make([]byte, 1+4+len(b.bit)*8)
	binary.LittleEndian.PutUint32(buf[0:4], b.numBits)
	buf[5] = b.numHash

	for i := 0; i < len(b.bit); i++ {
		binary.LittleEndian.PutUint64(buf[5+8*i:], b.bit[i])
	}

	return buf

}

// Writer 

type Writer struct {
	file *os.File
	buf  *bufio.Writer
	
	// offset of the current block
	offset uint64
	
	// index of the blocks
	index []IndexEntry

	// keys of the blocks
	keys [][]byte

	// buffer for the current block (memtable entries)
	blockbuf []memtable.Entry

	// size of the current block (memtable size)
	blocksize int

}

func NewWriter(filename string) (*Writer, error) {
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, err
	}
	return &Writer{
		file:      file,
		buf:       bufio.NewWriterSize(file,64*1024),
		blockbuf: make([]memtable.Entry,0),
		index:     make([]IndexEntry,0),
		keys:      make([][]byte,0),
	}, nil
}


