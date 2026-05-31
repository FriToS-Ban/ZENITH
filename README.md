# ZENITH

> A local-first hybrid search engine with an LSM-backed index, written in Go.

ZENITH is a CLI tool that indexes your local files and makes them searchable — not just by keyword, but by meaning. Point it at a directory and query it with natural language. It understands intent, tolerates typos, and ranks results using a hybrid lexical + semantic scoring pipeline.

The storage layer is built from scratch: a full LSM-tree pipeline (WAL → MemTable → SSTable → Bloom filters), a BK-tree fuzzy matcher, and an RRF-based hybrid ranker. This is not a wrapper around Elasticsearch or a vector database — it is a ground-up implementation of the primitives that make search work.

---

## Feature Status

| Feature | Status | Notes |
|---|---|---|
| CLI (`index`, `search`, `watch`, `serve`, `version`) | Active | Full cobra CLI |
| LSM storage (WAL, MemTable, SSTable, Compaction) | Active | Ground-up implementation |
| Bloom filter + sparse index | Active | Per-SSTable |
| BK-tree fuzzy matching | Active | O(log n) Levenshtein |
| BM25 + TF-IDF scoring | Active | |
| Edge n-gram prefix search | Active | |
| Phonetic matching (Soundex) | Active | |
| Synonym expansion | Active | |
| FST term dictionary | Active | Rebuilt after every flush |
| RRF hybrid ranking | Active | |
| Deterministic embedder | Active | Default — zero dependencies |
| Ollama embedder | Active | Requires local Ollama install |
| Nerve Python sidecar | Active | Requires running the sidecar |
| gRPC server (`zenith serve`) | Active | Port 8080 default |
| fsnotify incremental watching | Active | |
| `.txt` `.md` `.go` `.html` extractors | Active | |
| PDF extraction | Not implemented | Returns unsupported error |
| OpenAI embedder | Not implemented | Planned |
| Prometheus metrics | Not implemented | Phase 6 |
| OpenTelemetry traces | Not implemented | Phase 6 |
| goreleaser binary releases | Not implemented | Phase 5 pending |

---

## Quick Start

```bash
# Install from source
go install github.com/shramanb113/ZENITH/cmd/zenith@latest

# Index a directory
zenith index ~/Documents

# Search (hybrid: lexical + fuzzy + semantic)
zenith search "kubernetes memory debugging"

# Search with more results
zenith search -n 20 "kubernetes memory debugging"

# Index and watch for live updates
zenith watch --index-first ~/Documents

# Start gRPC server for remote access
zenith serve --port 8080
```

By default ZENITH uses the **deterministic embedder** — no external services needed. For real semantic search, pick an embedder backend (see below).

---

## Embedder Backends

ZENITH supports three embedding backends selectable via `--embedder`. All three work with a pre-built binary — no Python or sidecar needs to be bundled.

### Deterministic (default)

Hash-based embeddings. No setup, no external process. Lexical and fuzzy search work at full quality; semantic ranking is approximate.

```bash
zenith index ~/Documents
zenith search "query"
```

### Ollama (recommended for semantic search)

Runs fully offline. Many users already have Ollama installed. ZENITH uses `nomic-embed-text` (768-dimensional, CPU-friendly).

```bash
# One-time setup
ollama pull nomic-embed-text
# ollama serve runs automatically on most installs

# Use Ollama for indexing and search
zenith index --embedder ollama ~/Documents
zenith search --embedder ollama "query"

# Custom Ollama URL or model
zenith index --embedder ollama --ollama-url http://localhost:11434 --ollama-model nomic-embed-text ~/Documents
```

If Ollama is offline when you run a query, ZENITH degrades gracefully to lexical-only search — it does not crash.

### Nerve Python Sidecar

A FastAPI service bundled in the repo that serves `all-MiniLM-L6-v2` (384-dimensional) embeddings. Use this if you prefer not to install Ollama.

```bash
# One-time setup (from the ZENITH repo root)
cd nerve
pip install -e .          # installs fastapi, uvicorn, sentence-transformers
uvicorn main:app --host 0.0.0.0 --port 8000

# In another terminal
zenith index --embedder nerve ~/Documents
zenith search --embedder nerve "query"

# Custom nerve URL
zenith search --embedder nerve --nerve-url http://localhost:8000 "query"
```

The nerve sidecar is not bundled in binary releases — it lives in the repo and must be started manually. For most users, Ollama is simpler.

---

## Architecture

```
zenith index <path>
      │
      ▼
┌─────────────┐     ┌──────────────────────┐     ┌────────────────────────────┐
│   Crawler   │────▶│       Analyzer        │────▶│         Embedder           │
│  (fsnotify) │     │                      │     │                            │
│  recursive  │     │  regex tokenise      │     │  deterministic (default)   │
│  dir walk   │     │  camelCase-aware     │     │  ── or ──                  │
│  + live     │     │  lowercase           │     │  ollama nomic-embed-text   │
│  watching   │     │  stop-word filter    │     │  ── or ──                  │
│             │     │  Porter2 stem        │     │  nerve all-MiniLM-L6-v2   │
│             │     │  FST prefix-resolve  │     │                            │
│             │     │  synonym expand      │     │  → []float32               │
└─────────────┘     └──────────────────────┘     └────────────┬───────────────┘
                                                              │
                             ┌────────────────────────────────┘
                             ▼
                     ┌───────────────┐
                     │  LSM Storage  │
                     │               │
                     │  WAL          │  crash-safe append log
                     │  MemTable     │  skip-list, O(log n)
                     │  SSTable      │  immutable, block-structured
                     │  Bloom filter │  O(1) miss bypass
                     │  Sparse index │  memory-efficient offsets
                     │  Compaction   │  leveled, background worker
                     └───────┬───────┘
                             │
zenith search <query>        │
      │                      ▼
      │              ┌───────────────┐
      │              │  Query Engine │
      ├─────────────▶│               │
      │  lexical     │  BM25 scoring │
      │  fuzzy       │  TF-IDF       │
      │  semantic    │  edge n-grams │
      │              │  phonetic     │
      │              │  BK-tree      │  O(log n) fuzzy
      │              │  vector cosine│  dot product
      │              │               │
      │              │  RRF fusion   │  merges all signals
      │              └───────────────┘
      │
      └──▶ zenith serve ──▶ gRPC server (port :8080)
```

---

## Storage Engine

ZENITH's index is backed by a full LSM-tree — the same architecture used by RocksDB and LevelDB. Nothing is outsourced to SQLite or an embedded key-value library.

| Component | Implementation | Status |
|---|---|---|
| Write-Ahead Log | Append-only, CRC-framed, crash-safe | Active |
| MemTable | Skip-list, sorted, O(log n) ops | Active |
| SSTable | Immutable block-structured sorted files | Active |
| Group Committer | Batches concurrent flushes into one fsync | Active |
| Leveled Compaction | Background goroutine, tombstone pruning | Active |
| Bloom Filter | Probabilistic O(1) disk-lookup bypass | Active |
| Sparse Index | Memory-efficient offset map per SSTable | Active |
| FST Dictionary | Rebuilt after every flush, prefix-resolve | Active |

The WAL guarantees no indexed document is lost on crash. Bloom filters mean queries never hit disk for keys that don't exist. Compaction keeps read amplification bounded as the index grows.

---

## Search Pipeline

### Lexical Layer

- Inverted index with BM25 and TF-IDF scoring
- Porter2 stemming (`jumping` → `jump`)
- Edge n-grams for prefix / search-as-you-type
- Phonetic matching via Soundex
- Synonym expansion

### Fuzzy Layer

- BK-tree over Levenshtein distance, O(log n) via triangle inequality pruning
- Tolerates up to `FuzzyMaxDist` edits (default 2, configurable in `config.go`)

### Semantic Layer

- Embedding via Ollama, Nerve sidecar, or deterministic fallback
- Vector cosine similarity (dot product with cached magnitudes)
- Stored as float16 to halve memory; magnitudes cached separately
- Embedding failures are non-fatal — engine degrades to lexical-only

### Ranking

- Reciprocal Rank Fusion (RRF, k=60) merges lexical and semantic result lists
- BM25 as tiebreaker when RRF scores are within epsilon
- All weights tunable in `internal/config/config.go`

---

## File Support

| Format | Extraction | Status |
|---|---|---|
| `.txt` `.md` `.log` `.csv` `.json` `.yaml` | Raw UTF-8 text | Active |
| `.go` | AST — identifiers, comments, package name | Active |
| `.py` `.ts` `.js` `.jsx` `.tsx` `.rs` `.java` `.c` `.cpp` | Raw source | Active |
| `.html` `.htm` | Tag-stripped visible text (skips `<script>`, `<style>`) | Active |
| `.pdf` | Not implemented | Planned |

ZENITH watches indexed directories with `fsnotify`. Changed files are re-indexed on write events without a full re-crawl.

---

## gRPC API

`zenith serve` exposes the full search and indexing API over gRPC (default port `:8080`). The Protobuf contract is in `gen/go/zenithproto/`.

```protobuf
service SearchService {
  rpc IndexDocument(IndexRequest)  returns (IndexResponse);
  rpc Search(SearchRequest)        returns (SearchResponse);
  rpc FuzzySearch(FuzzyRequest)    returns (SearchResponse);
  rpc HybridSearch(HybridRequest)  returns (SearchResponse);
  rpc GetStats(StatsRequest)       returns (StatsResponse);
}
```

---

## CLI Reference

```
zenith index  <directory>   Bulk-index all supported files
zenith search <query>       Run a hybrid search query
zenith watch  <directory>   Incrementally index on file changes
zenith serve                Start the gRPC server
zenith version              Print version

Common flags (all commands):
  --db           string   Index database path (default: zenith.db)
  --fst          string   On-disk FST path (default: ./data/index.fst)
  --embedder     string   ollama | nerve | deterministic (default: deterministic)
  --ollama-url   string   Ollama server URL (default: http://localhost:11434)
  --ollama-model string   Ollama model name (default: nomic-embed-text)
  --nerve-url    string   Nerve sidecar URL (default: http://localhost:8000)

search flags:
  -n, --max int   Maximum results to display (default: 10)

watch flags:
  --index-first   Bulk-index before starting the watcher

serve flags:
  -p, --port string   gRPC listen port (default: 8080)
```

---

## Build Roadmap

### Phase 1 — Core Foundation

- [x] Inverted index with map-based postings lists
- [x] Tokenization, stop-word filtering, lowercasing
- [x] gRPC service definition and Protobuf contract
- [x] Thread-safety via `sync.RWMutex`
- [x] Concurrent indexing via worker pools
- [x] Binary persistence with `encoding/gob` and graceful shutdown

### Phase 2 — Neural Intelligence

- [x] Vector map integration and high-dimensional schema
- [x] Dot product and magnitude in pure Go
- [x] Cosine similarity and L2 distance
- [x] Deterministic hash embeddings
- [x] Hybrid ranking with score normalization and weighted fusion
- [x] Reciprocal Rank Fusion (RRF)

### Phase 3 — Linguistic Mastery

- [x] Porter2 stemming
- [x] Edge n-grams
- [x] Phonetic matching (Soundex)
- [x] Levenshtein distance
- [x] BK-tree fuzzy matching (O(log n))
- [x] Synonym expansion
- [x] Finite State Transducers

### Phase 4 — Storage Engine

- [x] Write-Ahead Log (WAL) with CRC framing
- [x] MemTable skip-list
- [x] SSTables with group committer
- [x] Leveled compaction background worker
- [x] Bloom filters
- [x] Sparse index
- [x] WAL recovery benchmarks

### Phase 5 — CLI + Local Embeddings (current)

- [x] `cobra` CLI: `index`, `search`, `watch`, `serve`, `version`
- [x] File crawler with `fsnotify` incremental watching
- [x] Ollama embedder (`nomic-embed-text`, fully offline)
- [x] Nerve Python sidecar embedder (`all-MiniLM-L6-v2`)
- [x] Deterministic hash embedder (zero-dependency default)
- [x] Per-file type text extractors (`.md`, `.go`, `.html`, raw source)
- [ ] OpenAI embedder (optional flag)
- [ ] PDF text extraction
- [ ] goreleaser binary releases

### Phase 6 — Production Observability (planned)

- [ ] Prometheus metrics exporter
- [ ] OpenTelemetry trace spans across the full query path
- [ ] `make dev-stack` — local Prometheus + Grafana + Tempo via docker-compose
- [ ] `/metrics` endpoint on `zenith serve`

---

## Configuration

All tunable parameters live in `internal/config/config.go` (`DefaultConfig()`).

| Parameter | Default | Description |
|---|---|---|
| `FuzzyMaxDist` | `2` | BK-tree edit distance threshold |
| `RRFConstant` | `60.0` | RRF k value |
| `PhoneticWeight` | — | Phonetic signal weight in fusion |
| `VectorWeight` | — | Vector signal weight in fusion |
| `NeuralWeight` | — | Neural signal weight in fusion |
| `NerveURL` | `http://localhost:8000` | Nerve sidecar URL |
| `MemTableMaxSize` | `64MB` | SSTable flush threshold |
| `CommitWindow` | `4ms` | Group-committer batch window |

---

## Design Decisions

**Why LSM and not B-tree?**
LSM trees optimize for write throughput — critical for indexing large directories quickly. Reads are fast enough with Bloom filters handling the "does this key exist?" check before any disk access.

**Why BK-tree and not a hashmap for fuzzy search?**
The hashmap approach is O(n) on every fuzzy query — it scans the entire term dictionary. A BK-tree prunes the search space using the triangle inequality of edit distance, giving O(log n) average case. At 100k indexed terms the difference is measurable.

**Why three embedder backends?**
A pre-built binary cannot bundle a Python runtime. The deterministic embedder means the binary works with zero setup. Ollama is the recommended semantic path — it is local, free, and a large fraction of developers already have it. The Nerve sidecar exists for users who want a lightweight Python option without Ollama's full install.

**Why not use an existing vector database?**
Because the point of ZENITH is to understand the storage layer — not consume it. The vector store is intentionally implemented on top of the same LSM engine that handles the inverted index. One storage backend, two index types.

---

## Project Structure

```
ZENITH/
├── cmd/
│   ├── zenith/        # CLI entrypoint (cobra) — index, search, watch, serve
│   └── server/        # standalone gRPC server (without CLI wrapper)
├── internal/
│   ├── analysis/      # tokenizer, stemmer, n-grams, phonetic, BK-tree, FST
│   ├── crawler/       # fsnotify file watcher + text extractors
│   ├── embedding/     # Ollama, Nerve, deterministic adapters + LRU cache
│   ├── storage/       # WAL, MemTable, SSTable, Bloom filter, compaction
│   ├── index/         # inverted index + vector store + search orchestrator
│   ├── ranking/       # RRF + BM25 tiebreak + TF-IDF
│   └── config/        # all tunable parameters
├── pkg/zenith/        # public API types
├── gen/go/zenithproto/ # generated Protobuf
└── nerve/             # Python embedding sidecar (FastAPI + sentence-transformers)
```

---

## Contributing

ZENITH is built in public. Issues and PRs are open.

Every component has a clear boundary and a reason to exist. If you're working on distributed systems, storage engines, or search infrastructure in Go this is a good place to dig in.

---

## License

MIT
