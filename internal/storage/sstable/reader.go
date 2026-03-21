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
	ErrIndex  = errors.New("sstable : corrupt Index")
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
	bloom  *BloomReader
}

// referring from the function from writeBloom in writer.go
func (r *Reader) readBloom() error {

	if r.footer.BloomSize == 0 {
		return ErrBloom
	}

	buf := make([]byte, r.footer.BloomSize)
	if _, err := r.file.ReadAt(buf, int64(r.footer.BloomOffset)); err != nil {
		return fmt.Errorf("sstable: read bloom: %w", err)
	}

	br, err := decode(buf)

	if err != nil {
		return err
	}

	r.bloom = br

	return nil

}

// referring from the function writeIndex in writer.go
func (r *Reader) readIndex() error {

	if r.footer.IndexSize == 0 {
		return ErrIndex
	}

	buf := make([]byte, r.footer.IndexSize)
	if _, err := r.file.ReadAt(buf, int64(r.footer.IndexOffset)); err != nil {
		return fmt.Errorf("sstable: read index: %w", err)
	}

	r.index = make([]IndexEntry, 0)
	pos := 0

	for pos < len(buf) {

		if pos+2 > len(buf) {
			return fmt.Errorf("Keylen corrupted in index")
		}

		keyLen := binary.LittleEndian.Uint16(buf)
		pos += 2

		// keylen(2) , key array(keylen which we extracted) , offset(8) + size (4)
		if pos+int(keyLen)+12 > len(buf) {
			return ErrIndex
		}

		lastKey := make([]byte, keyLen)
		copy(lastKey, buf[pos:pos+int(keyLen)])
		pos += int(keyLen)

		offset := binary.LittleEndian.Uint64(buf[pos : pos+8])
		pos += 8

		size := binary.LittleEndian.Uint32(buf[pos : pos+4])
		pos += 4

		r.index = append(r.index, IndexEntry{
			LastKey: lastKey,
			Offset:  offset,
			Size:    size,
		})

	}

	return nil

}

// similarly referring from writeFooter in writer.go
func (r *Reader) readFooter() error {

	info, err := r.file.Stat()

	if err != nil {
		return fmt.Errorf("sstable: stat: %w", err)
	}

	if info.Size() < FooterSize {
		return ErrFooter
	}

	buf := make([]byte, FooterSize)
	if _, err := r.file.ReadAt(buf, info.Size()-int64(FooterSize)); err != nil {
		return fmt.Errorf("sstable: read footer: %w", err)
	}

	if string(buf[32:36]) != MagicBytes {
		return ErrFooter
	}

	r.footer = Footer{
		IndexOffset: binary.LittleEndian.Uint64(buf[0:8]),
		IndexSize:   binary.LittleEndian.Uint32(buf[8:12]),
		BloomOffset: binary.LittleEndian.Uint64(buf[12:20]),
		BloomSize:   binary.LittleEndian.Uint32(buf[20:24]),
		EntryCount:  binary.LittleEndian.Uint64(buf[24:32]),
	}
	copy(r.footer.Magic[:], buf[32:36])

	return nil

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
