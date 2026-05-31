# ZENITH

> A local-first hybrid search engine with an LSM-backed index, written in Go.

ZENITH is a CLI tool that indexes your local files and makes them searchable — not just by keyword, but by meaning. Point it at a directory and query it with natural language. It understands intent, tolerates typos, and ranks results using a hybrid lexical + semantic scoring pipeline.

The storage layer is built from scratch: a full LSM-tree pipeline (WAL → MemTable → SSTable → Bloom filters), a BK-tree fuzzy matcher, and an RRF-based hybrid ranker. This is not a wrapper around Elasticsearch or a vector database — it is a ground-up implementation of the primitives that make search work.

---

## Install

### Pre-built binary (recommended)

Download the latest release from the [Releases page](https://github.com/shramanb113/ZENITH/releases) and add to your PATH.

```bash
# macOS / Linux — amd64
curl -L https://github.com/shramanb113/ZENITH/releases/latest/download/zenith_linux_amd64.tar.gz | tar xz
sudo mv zenith /usr/local/bin/

# macOS ARM (Apple Silicon)
curl -L https://github.com/shramanb113/ZENITH/releases/latest/download/zenith_darwin_arm64.tar.gz | tar xz
sudo mv zenith /usr/local/bin/

# Windows — download zenith_windows_amd64.zip from the Releases page
# Extract zenith.exe and add its folder to your PATH
```

### Go install

```bash
go install github.com/shramanb113/ZENITH/cmd/zenith@latest
```

---

## Quick Start

```bash
# Index a directory
zenith index ~/Documents

# Search — hybrid lexical + fuzzy + semantic
zenith search "kubernetes memory debugging"

# More results
zenith search -n 20 "kubernetes memory debugging"

# Index and watch for live file changes
zenith watch --index-first ~/Documents

# Start gRPC server for remote access
zenith serve --port 8080
```

The first time you run any command, ZENITH auto-starts the nerve embedding service in the background (see below). No configuration needed.

---

## Feature Status

| Feature | Status | Notes |
|---|---|---|
| CLI (`index`, `search`, `watch`, `serve`, `version`) | Active | |
| LSM storage (WAL, MemTable, SSTable, Compaction) | Active | Ground-up implementation |
| Bloom filter + sparse index | Active | Per-SSTable |
| BK-tree fuzzy matching | Active | O(log n) Levenshtein |
| BM25 + TF-IDF scoring | Active | |
| Edge n-gram prefix search | Active | |
| Phonetic matching (Soundex) | Active | |
| Synonym expansion | Active | |
| FST term dictionary | Active | Rebuilt after every flush |
| RRF hybrid ranking | Active | |
| Nerve auto-start (embedded in binary) | Active | Requires Python 3 |
| Deterministic embedder | Active | Fallback — zero dependencies |
| Ollama embedder | Active | Requires local Ollama |
| gRPC server (`zenith serve`) | Active | Port 8080 default |
| fsnotify incremental watching | Active | |
| `.txt` `.md` `.go` `.html` extractors | Active | |
| PDF extraction | Not implemented | Planned |
| OpenAI embedder | Not implemented | Planned |
| Prometheus metrics | Not implemented | Phase 6 |
| OpenTelemetry traces | Not implemented | Phase 6 |

---

## How Nerve Works

ZENITH ships with the nerve embedding service **baked into the binary**. On every invocation it checks if nerve is already running at `127.0.0.1:8000`. If not, it:

1. Extracts `main.py` to `~/.zenith/nerve/`
2. Creates an isolated virtualenv at `~/.zenith/nerve/venv/`
3. Installs `fastapi`, `uvicorn`, and `sentence-transformers` (once, ~90 MB)
4. Starts uvicorn as a detached background process on port 8000
5. Waits up to 20 s for the health check to pass

**First run** (deps not installed) takes 1–3 minutes and requires Python 3 and internet access. Every subsequent run reuses the running process and starts in under 2 s.

If Python 3 is not found, ZENITH tries Ollama next. If that is also unavailable, it falls back to deterministic embeddings. None of these fallbacks crash — they just reduce semantic search quality.

The nerve process survives the parent zenith process. Kill it manually if needed:

```bash
# macOS / Linux
pkill -f "uvicorn main:app"

# Windows
taskkill /f /im uvicorn.exe
```

> **No Python?** Install [Ollama](https://ollama.com) and run `ollama pull nomic-embed-text`. Use `--embedder ollama` to always prefer it.

---

## Embedder Backends

Select with `--embedder`. The default (`auto`) runs the cascade automatically.

```
auto          nerve → Ollama → deterministic  (default)
nerve         nerve sidecar only (auto-managed at ~/.zenith/nerve/)
ollama        local Ollama (needs: ollama serve + ollama pull nomic-embed-text)
deterministic hash-based, zero dependencies
```

```bash
# Always use Ollama
zenith index --embedder ollama ~/Documents
zenith search --embedder ollama "query"

# Always use deterministic (fully offline, no Python needed)
zenith index --embedder deterministic ~/Documents
zenith search --embedder deterministic "query"

# Nerve at a custom URL (e.g. on another machine)
zenith search --embedder nerve --nerve-url http://192.168.1.10:8000 "query"
```

---

## Architecture

```
zenith index <path>
      │
      ▼
┌─────────────┐     ┌──────────────────────┐     ┌─────────────────────────────┐
│   Crawler   │────▶│       Analyzer        │────▶│         Embedder            │
│  (fsnotify) │     │                      │     │                             │
│  recursive  │     │  regex tokenise      │     │  auto cascade (default):    │
│  dir walk   │     │  camelCase-aware     │     │  1. nerve  all-MiniLM-L6-v2 │
│  + live     │     │  lowercase           │     │  2. ollama nomic-embed-text  │
│  watching   │     │  stop-word filter    │     │  3. deterministic (fallback) │
│             │     │  Porter2 stem        │     │                             │
│             │     │  FST prefix-resolve  │     │  → []float32                │
│             │     │  synonym expand      │     │                             │
└─────────────┘     └──────────────────────┘     └──────────────┬──────────────┘
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
zenith search <query>          │
      │                        ▼
      │                ┌───────────────┐
      │                │  Query Engine │
      ├───────────────▶│               │
      │  lexical       │  BM25 scoring │
      │  fuzzy         │  TF-IDF       │
      │  semantic      │  edge n-grams │
      │                │  phonetic     │
      │                │  BK-tree      │  O(log n) fuzzy
      │                │  vector cosine│  dot product
      │                │               │
      │                │  RRF fusion   │  merges all signals
      │                └───────────────┘
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

- Embedding via nerve (`all-MiniLM-L6-v2`, 384-dim), Ollama (`nomic-embed-text`, 768-dim), or deterministic fallback
- Vector cosine similarity (dot product with cached magnitudes, stored as float16)
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
| `.html` `.htm` | Tag-stripped visible text | Active |
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
  --db           string   Index database path             (default: zenith.db)
  --fst          string   On-disk FST path                (default: ./data/index.fst)
  --embedder     string   auto|nerve|ollama|deterministic (default: auto)
  --ollama-url   string   Ollama server URL               (default: http://localhost:11434)
  --ollama-model string   Ollama model name               (default: nomic-embed-text)
  --nerve-url    string   Nerve sidecar URL               (default: http://127.0.0.1:8000)

search flags:
  -n, --max int   Maximum results to display              (default: 10)

watch flags:
  --index-first   Bulk-index before starting the watcher

serve flags:
  -p, --port string   gRPC listen port                    (default: 8080)
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
- [x] Nerve sidecar embedded in binary, auto-started on first run
- [x] Ollama embedder (`nomic-embed-text`, fully offline)
- [x] Deterministic hash embedder (zero-dependency fallback)
- [x] Auto cascade: nerve → Ollama → deterministic
- [x] Per-file type text extractors (`.md`, `.go`, `.html`, raw source)
- [x] goreleaser binary releases (linux/darwin amd64+arm64, windows amd64)
- [ ] OpenAI embedder (optional flag)
- [ ] PDF text extraction

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
| `PhoneticWeight` | `0.3` | Phonetic signal weight in fusion |
| `VectorWeight` | `0.7` | Vector signal weight in fusion |
| `NeuralWeight` | `1.0` | Neural signal weight in fusion |
| `NerveURL` | `http://127.0.0.1:8000` | Nerve sidecar URL |
| `NerveTimeout` | `5s` | Per-request timeout to nerve |
| `MemTableMaxSize` | `64 MB` | SSTable flush threshold |
| `CommitWindow` | `4 ms` | Group-committer batch window |

---

## Design Decisions

**Why LSM and not B-tree?**
LSM trees optimize for write throughput — critical for indexing large directories quickly. Reads are fast enough with Bloom filters handling the "does this key exist?" check before any disk access.

**Why BK-tree and not a hashmap for fuzzy search?**
The hashmap approach is O(n) on every fuzzy query — it scans the entire term dictionary. A BK-tree prunes the search space using the triangle inequality of edit distance, giving O(log n) average case. At 100k indexed terms the difference is measurable.

**Why embed nerve in the binary?**
A pre-built binary cannot run Python. Embedding `main.py` and bootstrapping a virtualenv on first run means the binary is self-contained: users with Python 3 get full semantic search automatically, and users without fall back gracefully. No separate installer, no PATH configuration for a sidecar, no docs page that says "also run this other thing."

**Why Ollama as the second choice?**
A large fraction of developers already have Ollama installed. It runs fully offline, has no API key requirement, and `nomic-embed-text` is CPU-friendly. When nerve is unavailable, Ollama is often already there.

**Why not use an existing vector database?**
Because the point of ZENITH is to understand the storage layer — not consume it. The vector store is intentionally built on top of the same LSM engine that handles the inverted index. One storage backend, two index types.

---

## Project Structure

```
ZENITH/
├── cmd/
│   ├── zenith/               # CLI entrypoint (cobra) — index, search, watch, serve
│   └── server/               # standalone gRPC server (without CLI)
├── internal/
│   ├── analysis/             # tokenizer, stemmer, n-grams, phonetic, BK-tree, FST
│   ├── crawler/              # fsnotify file watcher + text extractors
│   ├── embedding/            # Ollama, nerve, deterministic adapters + LRU cache
│   ├── nervemanager/         # nerve lifecycle: embed, extract, venv, auto-start
│   │   └── assets/           # main.py and requirements.txt (baked into binary)
│   ├── storage/              # WAL, MemTable, SSTable, Bloom filter, compaction
│   ├── index/                # inverted index + vector store + search orchestrator
│   ├── ranking/              # RRF + BM25 tiebreak + TF-IDF
│   └── config/               # all tunable parameters
├── pkg/zenith/               # public API types
├── gen/go/zenithproto/       # generated Protobuf
├── nerve/                    # Python embedding sidecar (source, for development)
└── .goreleaser.yml           # multi-platform binary release config
```

---

## Contributing

ZENITH is built in public. Issues and PRs are open.

Every component has a clear boundary and a reason to exist. If you are working on storage engines, search infrastructure, or NLP pipelines in Go this is a good place to dig in.

---

## License

MIT
