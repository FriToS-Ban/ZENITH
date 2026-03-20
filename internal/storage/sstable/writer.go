package sstable

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"hash/fnv"
	"math"
	"os"

	"github.com/shramanb113/ZENITH/internal/storage/memtable"
)

var ErrEmptyTable = errors.New("sstable: cannot write an empty table")

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
	numHash := uint8(math.Round((float64(numBits) / float64(expectedKeys)) * math.Ln2))

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
	h.Write(key)
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
	buf[4] = b.numHash

	for i := 0; i < len(b.bit); i++ {
		binary.LittleEndian.PutUint64(buf[5+i*8:], b.bit[i])
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
		file:     file,
		buf:      bufio.NewWriterSize(file, 64*1024),
		blockbuf: make([]memtable.Entry, 0),
		index:    make([]IndexEntry, 0),
		keys:     make([][]byte, 0),
	}, nil
}

//  WriteALL -> writes all the entries which we get from the memtable iterator to the file
//  WriteBlock -> writes all the entries in the block buffer to the file ✅
//  WriteBloom -> writes the bloom filter to the file ✅
//  WriteIndex -> writes the index of the blocks to the file ✅
//  WriteFooter -> writes the footer of the file ✅
//  Close -> closes the file ✅

func (w *Writer) WriteAll(entries []memtable.Entry) error {
	if len(entries) == 0 {
		return ErrEmptyTable
	}

	// ── Phase 1: Data Blocks ─────────────────────────────────────────────────
	for _, e := range entries {
		// Collect key for bloom filter — defensive copy
		key := make([]byte, len(e.Key))
		copy(key, e.Key)
		w.keys = append(w.keys, key)

		// Accumulate into current block
		w.blockbuf = append(w.blockbuf, e)

		// Estimated encoded size of this entry:
		// keyLen(2) + valLen(4) + deleted(1) + key + value
		w.blocksize += 2 + 4 + 1 + len(e.Key) + len(e.Value)

		// Flush when block is full
		if w.blocksize >= BlockSize {
			if err := w.writeBlock(); err != nil {
				return fmt.Errorf("sstable: write block: %w", err)
			}
		}
	}

	// Flush any remaining entries that didn't fill a complete block.
	if len(w.blockbuf) > 0 {
		if err := w.writeBlock(); err != nil {
			return fmt.Errorf("sstable: write final block: %w", err)
		}
	}

	// ── Phase 2: Bloom Filter ────────────────────────────────────────────────
	bloomOffset := w.offset
	bloomSize, err := w.writeBloom()
	if err != nil {
		return fmt.Errorf("sstable: write bloom: %w", err)
	}

	// ── Phase 3: Index Block ─────────────────────────────────────────────────
	indexOffset := w.offset
	indexSize, err := w.writeIndex()
	if err != nil {
		return fmt.Errorf("sstable: write index: %w", err)
	}

	// ── Phase 4: Footer ──────────────────────────────────────────────────────
	if err := w.writeFooter(Footer{
		IndexOffset: indexOffset,
		IndexSize:   uint32(indexSize),
		BloomOffset: bloomOffset,
		BloomSize:   uint32(bloomSize),
		EntryCount:  uint64(len(entries)),
		Magic:       [4]byte{'Z', 'S', 'S', 'T'},
	}); err != nil {
		return fmt.Errorf("sstable: write footer: %w", err)
	}

	return nil
}

// Each entry
// keylen(2) | valueLen(4) | deleted(0 or 1) | key byte array | value byte array
// after that for header it will have crc check and the body encoded body in bytes
// and for sparse indexing the last key is stored in the writer as lastkey will point to that block
func (w *Writer) writeBlock() error {
	var encoded []byte

	for _, e := range w.blockbuf {
		buf := make([]byte, 2+4+1+len(e.Key)+len(e.Value))

		binary.LittleEndian.PutUint16(buf[0:2], uint16(len(e.Key)))
		binary.LittleEndian.PutUint32(buf[2:6], uint32(len(e.Value)))
		if e.Deleted {
			buf[6] = 1
		} else {
			buf[6] = 0
		}
		copy(buf[7:], e.Key)
		copy(buf[7+len(e.Key):], e.Value)
		encoded = append(encoded, buf...)
	}

	// Block header: CRC covers the encoded entries only
	header := make([]byte, 6)
	binary.LittleEndian.PutUint32(header[0:4], crc32.ChecksumIEEE(encoded))
	binary.LittleEndian.PutUint16(header[4:6], uint16(len(w.blockbuf)))

	// Record where this block starts before writing
	blockStart := w.offset

	if _, err := w.buf.Write(header); err != nil {
		return err
	}
	if _, err := w.buf.Write(encoded); err != nil {
		return err
	}

	// Sparse index entry — last key in this block is the boundary.
	// Reader does: find first IndexEntry.LastKey >= target → that block.
	lastKey := make([]byte, len(w.blockbuf[len(w.blockbuf)-1].Key))
	copy(lastKey, w.blockbuf[len(w.blockbuf)-1].Key)

	blockBytes := uint32(len(header) + len(encoded))
	w.offset += uint64(blockBytes)

	w.index = append(w.index, IndexEntry{
		LastKey: lastKey,
		Offset:  blockStart,
		Size:    blockBytes,
	})

	// Reset accumulation buffer
	w.blockbuf = w.blockbuf[:0]
	w.blocksize = 0

	return nil

}

func (w *Writer) writeIndex() (int, error) {
	var total int

	for _, indexentry := range w.index {
		// keylen(2) , key(len) , offset(8), size(4)
		buf := make([]byte, 2+len(indexentry.LastKey)+8+4)

		binary.LittleEndian.PutUint16(buf[0:2], uint16(len(indexentry.LastKey)))
		copy(buf[2:], indexentry.LastKey)
		baselength := len(indexentry.LastKey)
		binary.LittleEndian.PutUint64(buf[2+baselength:10+baselength], indexentry.Offset)
		binary.LittleEndian.PutUint32(buf[10+baselength:], indexentry.Size)

		if _, err := w.buf.Write(buf); err != nil {
			return total, err
		}
		total += len(buf)

	}

	w.offset += uint64(total)
	return total, nil
}

func (w *Writer) writeBloom() (int, error) {
	bf := newBloomFilter(len(w.keys))

	for _, k := range w.keys {
		bf.add(k)
	}

	encoded := bf.encode()

	if _, err := w.buf.Write(encoded); err != nil {
		return 0, nil
	}

	w.offset += uint64(len(encoded))

	return len(encoded), nil
}

func (w *Writer) writeFooter(f Footer) error {
	buf := make([]byte, FooterSize)

	binary.LittleEndian.PutUint64(buf[0:8], f.IndexOffset)
	binary.LittleEndian.PutUint32(buf[8:12], f.IndexSize)
	binary.LittleEndian.PutUint64(buf[12:20], f.BloomOffset)
	binary.LittleEndian.PutUint32(buf[20:24], f.BloomSize)
	binary.LittleEndian.PutUint64(buf[24:32], f.EntryCount)
	copy(buf[32:36], f.Magic[:])

	// padding from 36 to 48 for future usecase

	if _, err := w.buf.Write(buf); err != nil {
		return err
	}
	w.offset += FooterSize
	return nil
}

func (w *Writer) Close() error {

	// FLush from app cache to os cache
	if err := w.buf.Flush(); err != nil {
		return fmt.Errorf("sstable: flush: %w", err)
	}

	// now from os cache into file
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("sstable: fsync: %w", err)
	}

	return w.file.Close()
}
