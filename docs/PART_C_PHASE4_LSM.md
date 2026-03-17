# Part C: Phase 4 Architectural Blueprint — LSM-Tree Storage (#25-30)

---

## Architecture Overview

```
Write Path:                          Read Path:
                                     
Client → WAL (append) ──┐            Client
                         ↓              ↓
                     MemTable      Query Coordinator
                    (sorted)         ↓       ↓        ↓
                         ↓       MemTable  L0-SST   L1-SST ...
                    Flush to disk    ↓       ↓        ↓
                         ↓        (merge results, newest wins)
                    SSTable (L0)
                         ↓
                    Compactor
                    (background)
                    L0 → L1 → L2 ...
```

---

## 25. Write-Ahead Log (WAL)

### File Format Design

```
┌─────────────────────────────────────────────┐
│ WAL File Header (32 bytes)                  │
├──────────┬──────────┬───────────┬───────────┤
│ Magic    │ Version  │ Created   │ Seq Start │
│ "ZWAL"   │ uint32   │ int64     │ uint64    │
│ 4 bytes  │ 4 bytes  │ 8 bytes   │ 8 bytes   │
└──────────┴──────────┴───────────┴───────────┘

Each Record:
┌──────────┬──────────┬──────────┬───────────┬──────────┐
│ CRC32    │ Seq#     │ Type     │ KeyLen    │ ValLen   │
│ uint32   │ uint64   │ uint8    │ uint16    │ uint32   │
│ 4 bytes  │ 8 bytes  │ 1 byte   │ 2 bytes   │ 4 bytes  │
├──────────┴──────────┴──────────┴───────────┴──────────┤
│ Key (KeyLen bytes) │ Value (ValLen bytes)              │
└─────────────────────────────────────────────────────────┘

Record Types: PUT=1, DELETE=2, BATCH_BEGIN=3, BATCH_END=4
```

### Core Implementation

```go
type WAL struct {
    mu       sync.Mutex
    file     *os.File
    buf      *bufio.Writer
    seq      atomic.Uint64
    bytesW   int64            // Bytes written since last fsync
    syncCh   chan struct{}     // Trigger background sync
    
    // Config
    MaxSegmentSize int64      // 64MB default, then rotate
    SyncInterval   time.Duration // 100ms batched fsync
    SyncOnWrite    bool       // true = every write fsyncs (slow but safe)
}

func (w *WAL) Append(op OpType, key, value []byte) (uint64, error) {
    seq := w.seq.Add(1)
    
    // Build record
    rec := encodeRecord(seq, op, key, value)
    crc := crc32.ChecksumIEEE(rec)
    
    w.mu.Lock()
    // Write: [CRC32][record bytes]
    binary.Write(w.buf, binary.LittleEndian, crc)
    w.buf.Write(rec)
    w.bytesW += int64(4 + len(rec))
    w.mu.Unlock()
    
    if w.SyncOnWrite {
        return seq, w.sync()
    }
    return seq, nil
}

// Background fsync goroutine — batches fsyncs for throughput
func (w *WAL) syncLoop() {
    ticker := time.NewTicker(w.SyncInterval)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            w.sync()
        case <-w.syncCh:
            return
        }
    }
}
```

### Recovery Protocol

```go
func (w *WAL) Recover() ([]WALRecord, error) {
    reader := bufio.NewReader(w.file)
    // 1. Validate header magic + version
    // 2. Read records sequentially
    var records []WALRecord
    for {
        var crc uint32
        if err := binary.Read(reader, binary.LittleEndian, &crc); err != nil {
            if err == io.EOF { break }
            // Corruption: truncate WAL at this point
            log.Printf("WAL corruption at offset %d, truncating", offset)
            break
        }
        rec := readRecord(reader)
        if crc32.ChecksumIEEE(rec.Bytes()) != crc {
            // CRC mismatch: corruption detected
            log.Printf("CRC mismatch at seq %d, stopping recovery", rec.Seq)
            break
        }
        records = append(records, rec)
    }
    // 3. Sort by sequence number (should already be sorted)
    // 4. Apply to fresh memtable
    return records, nil
}
```

### Concurrency: Multiple writers use the mutex. Background sync batches fsyncs. Readers during recovery get exclusive access (engine not yet serving).

---

## 26. MemTables

### Data Structure Decision

| Structure | Insert O() | Lookup O() | Range O() | Go Friendliness | Lock-Free? |
|-----------|-----------|-----------|----------|-----------------|------------|
| **Skip List** | O(log n) | O(log n) | O(log n + k) | ✅ Easy | Possible |
| B-Tree | O(log n) | O(log n) | O(log n + k) | ⚠️ Complex | Hard |
| ART (Radix) | O(k) | O(k) | O(k + n) | ⚠️ Complex | Hard |

**Recommendation: Skip List** — simplest to implement correctly in Go, good cache behavior for typical search workloads, and concurrent skip lists are well-studied.

### Implementation

```go
const MaxLevel = 16

type SkipNode struct {
    Key     []byte
    Value   []byte
    Forward [MaxLevel]*SkipNode  // Fixed array, no allocation per node
    Level   int
}

type MemTable struct {
    mu       sync.RWMutex
    head     *SkipNode
    level    int
    size     int64         // Current memory usage in bytes
    maxSize  int64         // Flush threshold (default: 64MB)
    frozen   atomic.Bool   // True = read-only, pending flush
}

func (m *MemTable) Put(key, value []byte) error {
    if m.frozen.Load() {
        return ErrMemTableFrozen
    }
    m.mu.Lock()
    defer m.mu.Unlock()
    
    level := m.randomLevel()
    // ... standard skip list insert ...
    m.size += int64(len(key) + len(value) + 8*level)
    
    // Check flush threshold
    if m.size >= m.maxSize {
        m.frozen.Store(true)
        // Signal flush coordinator
    }
    return nil
}

func (m *MemTable) Get(key []byte) ([]byte, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    // Standard skip list search...
}
```

### Memory Budget
- **Active MemTable**: 64MB (configurable)
- **Frozen MemTable** (being flushed): 64MB  
- **Total MemTable memory**: 128MB worst case (2× active size)
- **Flush trigger**: When `size >= maxSize`, freeze current and create new active

### Serialization for SSTable Flush
```go
// In-order traversal of skip list → sorted key-value pairs
func (m *MemTable) Iterator() *MemTableIterator {
    return &MemTableIterator{current: m.head.Forward[0]}
}
```

---

## 27. SSTables

### On-Disk Format

```
┌──────────────────────────────────────────────────┐
│ SSTable File                                      │
├──────────────────────────────────────────────────┤
│ Data Block 0  (4KB default)                       │
│   [key1_len][key1][val1_len][val1]               │
│   [key2_len][key2][val2_len][val2]               │
│   ...                                            │
├──────────────────────────────────────────────────┤
│ Data Block 1                                      │
│   ...                                            │
├──────────────────────────────────────────────────┤
│ ...more data blocks...                           │
├──────────────────────────────────────────────────┤
│ Meta Block: Bloom Filter                          │
│   [num_bits][num_hashes][bitset_bytes]            │
├──────────────────────────────────────────────────┤
│ Index Block                                       │
│   [block0_last_key][block0_offset][block0_size]  │
│   [block1_last_key][block1_offset][block1_size]  │
│   ...                                            │
├──────────────────────────────────────────────────┤
│ Footer (48 bytes)                                 │
│   [index_offset][index_size]                     │
│   [bloom_offset][bloom_size]                     │
│   [entry_count][magic: "ZSST"]                   │
└──────────────────────────────────────────────────┘
```

### Block Compression

```go
type BlockCompression uint8
const (
    NoCompression   BlockCompression = 0
    SnappyCompress  BlockCompression = 1
    ZstdCompress    BlockCompression = 2  // Best ratio for text data
)
```

For search engine data (text-heavy), **Zstandard** gives 3-5x compression vs 2-3x for Snappy, with acceptable decompression speed.

### Key Prefix Compression
Adjacent keys in sorted blocks share prefixes. Store only the diff:
```
Full:   "document:0001" "document:0002" "document:0003"
Prefix: "document:0001" [13,1,"2"]     [13,1,"3"]
         full key       shared=13,      shared=13,
                        unshared=1,     unshared=1,
                        diff="2"        diff="3"
```
Saves 30-60% space on typical key distributions.

---

## 28. The Compactor

### Strategy Recommendation: **Leveled Compaction**

```
Level 0:  [SST-1] [SST-2] [SST-3] [SST-4]  ← Unsorted, overlapping
             ↓ compact when count > 4
Level 1:  [SST-A─────] [SST-B─────] [SST-C─────]  ← Sorted, non-overlapping
             ↓ compact when size > 10× L1 target
Level 2:  [SST-X] [SST-Y] [SST-Z] [SST-W] ...     ← 10× bigger
```

### Write Amplification Analysis
| Strategy | Write Amp | Read Amp | Space Amp |
|----------|-----------|----------|-----------|
| Size-Tiered | ~10x | ~30x | ~2x |
| **Leveled** | **~30x** | **~1.1x** | **~1.1x** |
| FIFO | ~1x | ~N | ~1x |

**For search engines, read performance matters most → Leveled.**

### Compaction Goroutine

```go
type Compactor struct {
    levels    []*Level
    mu        sync.Mutex
    wg        sync.WaitGroup
    stopCh    chan struct{}
    throttle  *rate.Limiter  // I/O throttling
}

func (c *Compactor) Run() {
    c.wg.Add(1)
    go func() {
        defer c.wg.Done()
        ticker := time.NewTicker(5 * time.Second)
        for {
            select {
            case <-ticker.C:
                c.maybeCompact()
            case <-c.stopCh:
                return
            }
        }
    }()
}

func (c *Compactor) Shutdown() {
    close(c.stopCh)
    c.wg.Wait() // Wait for in-progress compaction
}
```

---

## 29. Bloom Filters

### Tuning for Search Workloads

Search engines have a unique access pattern: most lookups are **misses** (query term not in SSTable). Bloom filters turn O(disk-read) misses into O(1) memory checks.

```go
type BloomFilter struct {
    bits    []uint64   // Packed bitset
    numBits uint32
    numHash uint8
}

// Optimal parameters:
// For 1% false positive rate: ~10 bits/key, 7 hash functions
// For 0.1% FPR: ~14 bits/key, 10 hash functions
// Search recommendation: 1% FPR (good enough, saves memory)

func NewBloomFilter(expectedKeys int, fpRate float64) *BloomFilter {
    numBits := uint32(-float64(expectedKeys) * math.Log(fpRate) / (math.Ln2 * math.Ln2))
    numHash := uint8(float64(numBits) / float64(expectedKeys) * math.Ln2)
    return &BloomFilter{
        bits:    make([]uint64, (numBits+63)/64),
        numBits: numBits,
        numHash: numHash,
    }
}

// Double-hashing trick: only need 2 hash functions to simulate k
func (bf *BloomFilter) Add(key []byte) {
    h1, h2 := murmur3Hash128(key)
    for i := uint8(0); i < bf.numHash; i++ {
        pos := (h1 + uint64(i)*h2) % uint64(bf.numBits)
        bf.bits[pos/64] |= 1 << (pos % 64)
    }
}

func (bf *BloomFilter) MayContain(key []byte) bool {
    h1, h2 := murmur3Hash128(key)
    for i := uint8(0); i < bf.numHash; i++ {
        pos := (h1 + uint64(i)*h2) % uint64(bf.numBits)
        if bf.bits[pos/64]&(1<<(pos%64)) == 0 {
            return false
        }
    }
    return true
}
```

---

## 30. Sparse Indexing

### Index Granularity

```
SSTable Data Blocks:
Block 0 (4KB)  Block 1 (4KB)  Block 2 (4KB)  ...  Block N (4KB)

Sparse Index (one entry per block):
┌──────────────┬────────┬──────┐
│ Last Key     │ Offset │ Size │
├──────────────┼────────┼──────┤
│ "apple"      │ 0      │ 4096 │
│ "banana"     │ 4096   │ 4096 │
│ "cherry"     │ 8192   │ 4096 │
│ ...          │ ...    │ ...  │
└──────────────┴────────┴──────┘
```

### Lookup: Binary search on sparse index → find candidate block → scan block for exact key.

### Memory-Mapped Access

```go
type MappedSSTable struct {
    data     []byte          // mmap'd file
    index    []IndexEntry    // Loaded into memory
    bloom    *BloomFilter    // Loaded into memory
}

func OpenSSTable(path string) (*MappedSSTable, error) {
    f, _ := os.Open(path)
    info, _ := f.Stat()
    data, _ := syscall.Mmap(int(f.Fd()), 0, int(info.Size()),
        syscall.PROT_READ, syscall.MAP_SHARED)
    // Read footer → load index + bloom from mmap'd data
    return &MappedSSTable{data: data, ...}, nil
}
```

### Prefix Compression for Index Keys
Store delta-encoded keys in the sparse index:
```go
type IndexEntry struct {
    SharedPrefix uint16   // Bytes shared with previous entry
    UnsharedKey  []byte   // Remaining bytes
    Offset       uint64
    Size         uint32
}
```
Reduces index memory by 40-60% for keys with common prefixes (e.g., `"user:1001"`, `"user:1002"`).

### Cache-Line Optimization
- Index entries should be **32 or 64 bytes** to align with CPU cache lines
- Keep the index sorted and contiguous in memory (slice, not linked list)
- Use binary search (cache-friendly) rather than interpolation search
