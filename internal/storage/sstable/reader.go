package sstable

import (
	"encoding/binary"
	"errors"
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
