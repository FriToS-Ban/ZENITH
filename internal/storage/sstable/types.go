package sstable

const (
	BlockSize  = 4 * 1024
	MagicBytes = "ZSST"
	FooterSize = 48
)


// IndexEntry represents an entry in the index.
// It stores the last key of a block, the offset of the block in the file, and the size of the block.
type IndexEntry struct {
	LastKey []byte
	Offset  uint64
	Size    uint32
}

// Footer represents the footer of an SSTable.
// It stores the offset and size of the index and bloom filter, the number of entries, and the magic bytes.
type Footer struct {
	IndexOffset uint64
	IndexSize   uint32
	BloomOffset uint64
	BloomSize   uint32
	EntryCount  uint64
	Magic       [4]byte
}