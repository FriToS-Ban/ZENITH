# Part E: Code Quality Improvements

---

## 1. Anti-Patterns Found in the Codebase

### 🔴 AP-1: Regex Compiled Per Call
```go
// tokenizer.go:31 — compiled on EVERY Tokenize() call
re := regexp.MustCompile(`[A-Z][a-z0-9]*|[a-z0-9]+|[A-Z]+`)
```
**Fix:** Compile once at struct initialization:
```go
var tokenRegex = regexp.MustCompile(`[A-Z][a-z0-9]*|[a-z0-9]+|[A-Z]+`)
```

### 🔴 AP-2: Lock/Unlock Spaghetti in Search()
The `Search()` method (lines 142-286) has 5 separate lock/unlock operations across two execution paths with conditional branching. One missed unlock = deadlock, one extra unlock = panic.

**Fix:** Split into isolated methods, each with its own clean lock scope:
```go
func (idx *InMemoryIndex) Search(query string, tokens []string) []SearchResponse {
    candidates := idx.lexicalPass(tokens)          // RLock/RUnlock
    vectorScores := idx.vectorPass(query)           // RLock/RUnlock
    results := idx.rankAndFuse(candidates, vectorScores)
    if needsExpansion(results) {
        expanded := idx.neuralExpand(tokens)        // RLock/RUnlock
        results = idx.rankAndFuse(merge(candidates, expanded), vectorScores)
    }
    return topK(results, 5)
}
```

### 🟠 AP-3: Error Swallowing
```go
// embedder.go:24
reqBody, _ := json.Marshal(EmbedRequest{Text: text})  // Error ignored

// index.go:63
docVec, _ := analysis.GetEmbedding(fullText)          // nil vector on error, silent
```
**Fix:** Propagate errors or log with context:
```go
docVec, err := analysis.GetEmbedding(fullText)
if err != nil {
    log.Printf("WARN: embedding failed for doc %s: %v (keyword-only indexing)", originalID, err)
}
```

### 🟠 AP-4: God Struct
`InMemoryIndex` holds 9 maps and handles indexing, searching, ranking, synonym expansion, persistence, and word vector management. It violates single responsibility.

**Fix:** Decompose into focused components:
```go
type Engine struct {
    Index     *InvertedIndex    // Term → postings
    Vectors   *VectorStore      // Doc → embedding
    Phonetics *PhoneticIndex    // Soundex → postings
    Synonyms  *SynonymEngine    // Word → expansions
    Ranker    *RRFRanker        // Score fusion
    Storage   *Persistence      // Save/Load
}
```

### 🟡 AP-5: No Context Propagation
Neither `Add()` nor `Search()` accept `context.Context`. You can't implement timeouts, cancellation, or tracing.

```go
// Current
func (idx *InMemoryIndex) Search(query string, queryTokens []string) []SearchResponse

// Should be
func (idx *InMemoryIndex) Search(ctx context.Context, query string, queryTokens []string) ([]SearchResponse, error)
```

---

## 2. Interface Design Recommendations

```go
// Clean boundaries between components

// Analyzer pipeline
type Analyzer interface {
    Analyze(text string) []Token
}

type Token struct {
    Term     string
    Position int
    Type     TokenType  // WORD, NGRAM, PHONETIC
}

// Storage abstraction (swappable in-memory vs LSM)
type Storage interface {
    Put(key, value []byte) error
    Get(key []byte) ([]byte, bool, error)
    Delete(key []byte) error
    Scan(prefix []byte) Iterator
    Close() error
}

// Vector operations
type VectorIndex interface {
    Add(id string, vector []float32) error
    Search(query []float32, topK int) ([]VectorMatch, error)
    Remove(id string) error
}

// Embedding service (swappable neural vs deterministic)
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
    Dimensions() int
}

// Scorer (pluggable ranking strategies)
type Scorer interface {
    Score(candidates []Candidate) []ScoredResult
}
```

---

## 3. Error Handling Philosophy for Search Systems

### Principle: Search Should Always Return Results (or an Empty List)

Search is **not** a transactional system. A degraded result is better than an error page.

```go
// Error hierarchy
var (
    // Retryable — caller can retry
    ErrNerveUnavailable = errors.New("embedding service unavailable")
    ErrTimeout          = errors.New("search timeout exceeded")
    
    // Degraded — return partial results with warning
    ErrVectorSearchSkipped = errors.New("vector search unavailable, keyword-only results")
    
    // Fatal — should not happen in production
    ErrCorruptedIndex     = errors.New("index data corruption detected")
)

// Wrap errors with context
func (idx *InMemoryIndex) Search(ctx context.Context, query string) ([]SearchResponse, []Warning, error) {
    var warnings []Warning
    
    queryVec, err := analysis.GetEmbedding(query)
    if err != nil {
        warnings = append(warnings, Warning{
            Code:    "VECTOR_UNAVAILABLE",
            Message: "Results may be less relevant (keyword-only mode)",
        })
        // Continue with keyword-only search, don't return error
    }
    // ...
    return results, warnings, nil
}
```

---

## 4. Logging & Observability

### Migrate to `log/slog` (Go 1.21+)

```go
import "log/slog"

// Structured, leveled logging
var logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelInfo,
}))

// Usage in search:
logger.Info("search completed",
    slog.String("query", query),
    slog.Int("results", len(results)),
    slog.Duration("latency", time.Since(start)),
    slog.String("mode", mode),
)

// Usage in indexing:
logger.Info("document indexed",
    slog.String("doc_id", originalID),
    slog.Int("tokens", len(tokens)),
    slog.Bool("has_vector", docVec != nil),
)

// Errors with context:
logger.Error("nerve embedding failed",
    slog.String("doc_id", originalID),
    slog.Any("error", err),
)
```

### Request Tracing

```go
// Add request ID to context
func (s *ZenithServer) Search(ctx context.Context, req *zenithproto.SearchRequest) (*zenithproto.SearchResponse, error) {
    requestID := uuid.NewString()
    ctx = context.WithValue(ctx, "request_id", requestID)
    logger := slog.With(slog.String("request_id", requestID))
    
    logger.Info("search started", slog.String("query", req.Query))
    start := time.Now()
    defer func() {
        logger.Info("search completed", slog.Duration("total_latency", time.Since(start)))
    }()
    // ...
}
```

---

## 5. Code Organization

### Recommended Directory Structure

```
ZENITH/
├── cmd/
│   ├── server/main.go          # Server entrypoint
│   └── client/main.go          # Test client
├── internal/
│   ├── analysis/               # Text processing pipeline
│   │   ├── tokenizer.go
│   │   ├── stemmer.go
│   │   ├── phonetic.go
│   │   ├── fuzzy.go
│   │   └── analyzer.go         # [NEW] Unified Analyzer interface
│   ├── embedding/              # [NEW] Separated from analysis
│   │   ├── embedder.go         # Embedder interface
│   │   ├── neural.go           # HTTP nerve client
│   │   ├── deterministic.go    # Hash-based fallback
│   │   └── cache.go            # LRU embedding cache
│   ├── index/
│   │   ├── inverted.go         # Inverted index (renamed)
│   │   ├── vector.go           # [NEW] Vector index (HNSW/flat)
│   │   └── phonetic_index.go   # [NEW] Separated phonetic index
│   ├── ranking/                # [NEW] Scoring & fusion
│   │   ├── scorer.go           # Scorer interface
│   │   ├── bm25.go             # [NEW] Future BM25 implementation
│   │   └── rrf.go              # RRF fusion
│   ├── storage/                # [NEW] Phase 4
│   │   ├── wal/
│   │   │   ├── wal.go
│   │   │   └── recovery.go
│   │   ├── memtable/
│   │   │   ├── skiplist.go
│   │   │   └── memtable.go
│   │   ├── sstable/
│   │   │   ├── writer.go
│   │   │   ├── reader.go
│   │   │   └── bloom.go
│   │   ├── compaction/
│   │   │   └── leveled.go
│   │   └── engine.go           # LSM engine orchestrator
│   ├── server/
│   │   └── server.go
│   ├── config/                 # [NEW]
│   │   └── config.go
│   └── core/
│       └── document.go
├── pkg/                        # [NEW] Public APIs
│   └── zenith/
│       └── client.go           # Go client SDK
├── proto/
│   └── document.proto
├── nerve/                      # Python embedding service
│   └── main.py
└── docs/                       # This guide
```

### Key Principles
1. **`internal/` for private packages** — prevents external imports
2. **One concern per package** — `embedding/` separate from `analysis/`
3. **Interface at package boundary** — consumers depend on interfaces, not implementations
4. **`pkg/` for public API** — client SDK for external consumers
5. **`config/` for all knobs** — single source of truth for defaults and overrides
