package sstable

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

var (
	ErrFooter = errors.New("sstable corrupt : Footer gone")
	ErrBlock  = errors.New("sstable corrupt : block CRC mismatch")
	ErrBloom  = errors.New("sstable: corrupt bloom filter")
	ErrHeader = errors.New("sstable : Header mismatch or corrupt")
)

type BloomReader struct {
	bits     []uint64
	numsBits uint32
	numsHash uint8
}

// [numBits(4)][numHash(1)][bitset bytes...]
func decode(data []byte) (*BloomReader, error) {

	if len(data) < 5 {
		return nil, ErrBloom
	}

	numBits := binary.LittleEndian.Uint32(data[0:4])
	numHash := data[4]
	bitsetBytes := data[5:]

	expectedWords := int((numBits + 63) / 64)
	if len(bitsetBytes) != expectedWords*8 {
		return nil, ErrBloom
	}

	bits := make([]uint64, expectedWords)
	for i := 0; i < expectedWords; i++ {
		bits[i] = binary.LittleEndian.Uint64(bitsetBytes[5+8*i:])
	}

	return &BloomReader{
		bits:     bits,
		numsBits: numBits,
		numsHash: numHash,
	}, nil

}

func (b *BloomReader) mayContain(key []byte) bool {
	h1, h2 := bloomHash(key)
	for i := uint8(0); i < b.numsHash; i++ {
		pos_i := (h1 + uint64(i)*h2) % uint64(b.numsBits)
		if b.bits[pos_i/64]&(1<<(pos_i%64)) == 0 {
			return false
		}
	}
	return true
}

type Reader struct {
	file   *os.File
	footer Footer
	index  []IndexEntry
	bloom  BloomReader
}

func (r *Reader) readBloom() error {

}
func (r *Reader) readIndex() error {

}
func (r *Reader) readFooter() error {

}

func OpenReader(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("sstable: open: %w", err)
	}

	r := &Reader{file: f}

	if err := r.readFooter(); err != nil {
		f.Close()
		return nil, err
	}

	if err := r.readBloom(); err != nil {
		f.Close()
		return nil, err
	}

	if err := r.readIndex(); err != nil {
		f.Close()
		return nil, err
	}
	return r, nil
}

// ─── Get ──────────────────────────────────────────────────────────────────────

// Get returns the value for key and true if found and not a tombstone.
// Returns (nil, false) if the key is not present or has been deleted.

func (r *Reader) Get(key []byte) ([]byte, bool)

func (r *Reader) findBlock(key []byte) int

type blockEntry struct {
	key     []byte
	value   []byte
	deleted bool
}

func (r *Reader) readBlock(entry IndexEntry) ([]blockEntry, error)

func (r *Reader) Close() error {
	return r.file.Close()
}
