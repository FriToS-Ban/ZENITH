# Part D: Cross-Cutting Concerns

---

## 1. Integration Architecture

### How Phase 4 Storage Integrates with Phase 2 Vector Storage

Currently vectors live in `map[uint32][]float32`. With LSM storage:

```
┌─────────────────────────────────────────────────┐
│ Write Path (IndexDocument)                       │
│                                                  │
│  Document ─→ Tokenize ─→ WAL Append             │
│                ↓            ↓                    │
│           Get Embedding   MemTable.Put()         │
│                ↓          for each:              │
│                ↓          • "idx:{term}:{docID}" │
│                ↓          • "vec:{docID}" = bytes │
│                ↓          • "doc:{docID}" = meta  │
│                ↓          • "phon:{code}:{docID}" │
│           Store in VectorIndex (separate)         │
│           (HNSW or flat, memory-resident)         │
└─────────────────────────────────────────────────┘
```

**Key Decision: Vectors should NOT go in the LSM tree.** Vector data requires fast random access and distance computations — LSM trees optimize for sorted sequential access. Keep two storage engines:

| Data Type | Storage | Reason |
|-----------|---------|--------|
| Inverted index postings | LSM | Sorted, append-friendly, compactable |
| Document metadata | LSM | Key-value, low update frequency |
| Vector embeddings | Separate in-memory index (HNSW) | Requires distance computations, ANN search |
| Phonetic/N-gram index | LSM | Same access pattern as inverted index |

### How Linguistic Indexes (Phase 3) Store in LSM

Key encoding scheme for multi-purpose storage:

```
Key Format                         Value
─────────────────────────────────  ─────────────────────
"t:{term}"                        → encoded postings list
"p:{soundex_code}"                → encoded postings list  
"n:{ngram}"                       → encoded postings list
"d:{docID}"                       → document metadata (JSON/protobuf)
"v:{docID}"                       → vector bytes (if not using separate index)
"s:{term}"                        → synonym list
"f:{fst_block_id}"                → FST node data
```

Prefix-based key groups enable efficient range scans per data type.

### Query Execution Pipeline

```
             ┌─────────────────────────────────────────────┐
             │            Query: "machne lerning"           │
             └──────────────────┬──────────────────────────┘
                                │
                    ┌───────────▼───────────┐
                    │   1. TOKENIZE & STEM  │
                    │   → ["machn", "lern"] │
                    └───────────┬───────────┘
                                │
          ┌─────────────────────┼─────────────────────┐
          ↓                     ↓                     ↓
  ┌───────────────┐   ┌────────────────┐   ┌──────────────────┐
  │ 2a. LEXICAL   │   │ 2b. FUZZY      │   │ 2c. PHONETIC     │
  │ Edge N-gram   │   │ BK-Tree lookup │   │ Soundex match    │
  │ lookup in LSM │   │ "machine"≈1    │   │ M250 → postings  │
  └──────┬────────┘   └──────┬─────────┘   └──────┬───────────┘
         ↓                    ↓                     ↓
  ┌──────────────────────────────────────────────────────────┐
  │            3. MERGE CANDIDATE SETS                       │
  │     Union of all posting lists with source weights       │
  └──────────────────────────┬───────────────────────────────┘
                             ↓
         ┌───────────────────┼───────────────────┐
         ↓                                       ↓
  ┌──────────────┐                     ┌──────────────────┐
  │ 4a. KEYWORD  │                     │ 4b. VECTOR       │
  │  RANKING     │                     │  SIMILARITY      │
  │  BM25/Custom │                     │  HNSW ANN search │
  └──────┬───────┘                     └──────┬───────────┘
         ↓                                     ↓
  ┌──────────────────────────────────────────────────────────┐
  │             5. RECIPROCAL RANK FUSION                    │
  │     RRF(d) = Σ 1/(k + rank_i(d))  for each ranker      │
  └──────────────────────────┬───────────────────────────────┘
                             ↓
                    ┌────────────────┐
                    │ 6. TOP-K       │
                    │ Return results │
                    └────────────────┘
```

### Consistency Model

For the current single-node design, consistency is straightforward:

```
Write: WAL.Append() → MemTable.Put() → Return OK
Read:  MemTable (newest) → L0 SSTables → L1 → L2 → ...
       First match wins (covers updates/deletes automatically)
```

For future distributed shards:
- **Per-shard WAL**: Each shard has its own WAL + LSM tree
- **Eventual consistency**: Reads may see stale data during replication lag
- **Quorum reads** (optional): Read from N/2+1 shards for strong consistency
- **Vector clock versioning**: The `Version int64` field in `Document` struct is the foundation — increment on every write

---

## 2. Performance Engineering

### Memory Hierarchy Exploitation

```
┌───────────────────────────────────────────────────────┐
│ L1 Cache (32KB, ~1ns)   → Bloom filter bit checks    │
│ L2 Cache (256KB, ~4ns)  → Sparse index binary search  │
│ L3 Cache (8MB, ~12ns)   → Hot SSTable index blocks    │
│ RAM (~40ns)              → MemTable, full indexes     │
│ SSD (~100μs)             → SSTable data blocks        │
│ HDD (~10ms)              → Cold SSTable tiers         │
└───────────────────────────────────────────────────────┘
```

**Go-specific tips:**
- **Avoid `interface{}`**: Your `Metadata map[string]interface{}` forces heap allocation on every value. Use a concrete type or proto.
- **Pre-allocate slices**: `results := make([]SearchResponse, 0, estimatedSize)` — you already do this in some places but not consistently.
- **Struct packing**: Go aligns fields by size. Reorder for minimal padding:

```go
// Current (40 bytes with padding):
type Document struct {
    ID       string           // 16 bytes
    Fields   map[string]string // 8 bytes
    Vectors  map[string][]float32 // 8 bytes
    Metadata map[string]interface{} // 8 bytes
    Version  int64            // 8 bytes
    Status   StatusString     // 16 bytes
}
// The map types are all 8-byte pointers, already well-packed.
// But StatusString should be a uint8 enum, not a string:
type StatusString uint8
const (StatusPending StatusString = iota; StatusConverted)
```

- **Reuse buffers with `sync.Pool`:**
```go
var bufPool = sync.Pool{
    New: func() interface{} { return make([]byte, 0, 4096) },
}
// In hot paths:
buf := bufPool.Get().([]byte)[:0]
defer bufPool.Put(buf)
```

### Concurrency Model Summary

| Component | Pattern | Justification |
|-----------|---------|---------------|
| WAL | `sync.Mutex` + background fsync | Single writer, high throughput via batching |
| MemTable | `sync.RWMutex` | Many readers, few writers |
| SSTable reads | Lock-free (immutable files) | SSTables never change after creation |
| Compactor | Single goroutine + `rate.Limiter` | Avoid I/O storms |
| Embedding | Worker pool (N=4) + batch queue | Amortize HTTP overhead |
| Index search | Per-query goroutines + `errgroup` | Parallelize across ranking pipelines |

### Benchmarking Strategy

```go
// For every component, follow this pattern:
func BenchmarkCosineSimilarity384(b *testing.B) {
    a := randomVector(384)
    v := randomVector(384)
    b.ResetTimer()
    b.ReportAllocs()
    for i := 0; i < b.N; i++ {
        CosineSimilarity(a, v)
    }
}

// Key benchmarks to write:
// BenchmarkSkipListInsert, BenchmarkSkipListGet, BenchmarkSkipListRange
// BenchmarkBloomFilterAdd, BenchmarkBloomFilterCheck
// BenchmarkSSTableWrite, BenchmarkSSTablePointLookup
// BenchmarkWALAppend, BenchmarkWALRecovery
// BenchmarkLevenshtein, BenchmarkSoundex, BenchmarkStemming
// BenchmarkEdgeNgrams, BenchmarkRRFFusion
// BenchmarkFullSearchPipeline (end-to-end)
```

---

## 3. Operational Excellence

### Monitoring Hooks

```go
// Central metrics registry
type Metrics struct {
    IndexedDocs      atomic.Int64
    SearchQueries    atomic.Int64
    SearchLatencyP50 atomic.Int64  // microseconds
    SearchLatencyP99 atomic.Int64
    WALBytesWritten  atomic.Int64
    MemTableSize     atomic.Int64
    SSTTableCount    atomic.Int64
    CompactionsRun   atomic.Int64
    BloomFPRate      atomic.Int64  // false positive count
    NerveLatency     atomic.Int64  // embedding service latency
    NerveErrors      atomic.Int64
}

// Expose via gRPC health service or HTTP /metrics endpoint
```

### Graceful Degradation Patterns

```go
// Embedding service down → fall back to keyword-only search
func (idx *InMemoryIndex) SearchWithFallback(query string, tokens []string) []SearchResponse {
    queryVec, err := analysis.GetEmbedding(query)
    if err != nil {
        log.Printf("WARN: nerve offline, keyword-only mode: %v", err)
        return idx.keywordOnlySearch(tokens)
    }
    return idx.fullHybridSearch(query, queryVec, tokens)
}
```

### Configuration With Sensible Defaults

```go
type Config struct {
    // Storage
    WALDir           string        `default:"./data/wal"`
    DataDir          string        `default:"./data/sst"`
    MemTableMaxSize  int64         `default:"67108864"` // 64MB
    
    // Search
    MaxResults       int           `default:"10"`
    FuzzyMaxDist     int           `default:"2"`
    BloomFPRate      float64       `default:"0.01"` // 1%
    RRFConstant      float64       `default:"60"`
    
    // Performance
    EmbeddingWorkers int           `default:"4"`
    CompactionInterv time.Duration `default:"30s"`
    WALSyncInterval  time.Duration `default:"100ms"`
    
    // Nerve
    NerveURL         string        `default:"http://localhost:5000"`
    NerveTimeout     time.Duration `default:"5s"`
}
```

---

## 4. Testing Strategy

### Unit Tests (Missing — Priority P0)

```go
// analysis/stemmer_test.go — Test against known Porter outputs
func TestPorterStemmer(t *testing.T) {
    cases := map[string]string{
        "caresses": "caress", "ponies": "poni", "ties": "ti",
        "cats": "cat", "feed": "feed", "agreed": "agre",
        "disabled": "disabl", "matting": "mat", "mating": "mate",
        "meeting": "meet", "milling": "mill", "messing": "mess",
        "meetings": "meet",
    }
    s := New()
    for input, expected := range cases {
        got := s.Stem(input)
        if got != expected {
            t.Errorf("Stem(%q) = %q, want %q", input, got, expected)
        }
    }
}

// analysis/fuzzy_test.go
func TestLevenshtein(t *testing.T) {
    dist, ok := Levenshtein("kitten", "sitting")
    assert(t, ok && dist == 3)
    dist, ok = Levenshtein("", "abc")
    assert(t, ok && dist == 3)
}

// index/index_test.go — Integration
func TestSearchFindsExactMatch(t *testing.T) { ... }
func TestSearchFuzzyMatch(t *testing.T) { ... }
func TestSearchPhoneticMatch(t *testing.T) { ... }
func TestRRFFusionDeterministic(t *testing.T) { ... }
func TestHashCollisionHandling(t *testing.T) { ... }
func TestConcurrentAddAndSearch(t *testing.T) { ... }
```

### Chaos Engineering

```go
// Test nerve service failures during indexing
func TestIndexWithNerveDown(t *testing.T) {
    // Don't start nerve → embeddings should fail gracefully
    // Index should still work (keyword-only mode)
}

// Test concurrent read/write under load
func TestConcurrentStress(t *testing.T) {
    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(2)
        go func() { defer wg.Done(); idx.Add(...) }()
        go func() { defer wg.Done(); idx.Search(...) }()
    }
    wg.Wait()
    // Verify no panics, no data corruption
}
```
