# ZENITH

> A local-first hybrid search engine with an LSM-backed index, written in Go.

ZENITH is a CLI tool that indexes your local files and makes them searchable — not just by keyword, but by meaning. Point it at a directory and query it with natural language. It understands intent, tolerates typos, and ranks results using a hybrid lexical + semantic scoring pipeline.

The storage layer is built from scratch: a full LSM-tree pipeline (WAL → MemTable → SSTable → Bloom filters), a BK-tree fuzzy matcher, and an RRF-based hybrid ranker. This is not a wrapper around Elasticsearch or a vector database — it is a ground-up implementation of the primitives that make search work.

---

## Requirements

| Requirement | Version       | Notes                                                |
| ----------- | ------------- | ---------------------------------------------------- |
| Go          | 1.24 or newer | The only hard requirement                            |
| Python 3    | 3.10 or newer | Optional — enables nerve semantic embeddings         |
| Ollama      | any           | Optional — alternative to Python for semantic search |

No other dependencies. The binary is self-contained — the nerve embedding service is baked in.

---

## Install

```bash
go install github.com/shramanb113/ZENITH/cmd/zenith@latest
```

That is the complete install. One command, any platform (Linux, macOS, Windows), no extra steps.

**Build from source:**

```bash
git clone https://github.com/shramanb113/ZENITH
cd ZENITH
go build -o zenith ./cmd/zenith
```

---

## Quick Start

```bash
# Index a directory
zenith index ~/Documents

# Search — hybrid lexical + fuzzy + semantic in one query
zenith search "kubernetes memory debugging"

# More results
zenith search -n 20 "kubernetes memory debugging"

# Watch a directory and auto-start on every boot
zenith watch add ~/Documents
zenith watch install          # registers OS boot auto-start
zenith watch start            # start watching now (also runs automatically on boot)

# Start gRPC server for remote access
zenith serve --port 8080
```

The first run auto-starts the nerve embedding service in the background if Python 3 is installed. No configuration needed. If Python is not found it falls back gracefully — lexical and fuzzy search still work at full quality.

---

## Feature Status

| Feature                                              | Status          | Notes                                |
| ---------------------------------------------------- | --------------- | ------------------------------------ |
| CLI (`index`, `search`, `watch`, `serve`, `version`) | Active          |                                      |
| LSM storage (WAL, MemTable, SSTable, Compaction)     | Active          | Ground-up implementation             |
| Bloom filter + sparse index                          | Active          | Per-SSTable                          |
| BK-tree fuzzy matching                               | Active          | O(log n) Levenshtein                 |
| BM25 + TF-IDF scoring                                | Active          |                                      |
| Edge n-gram prefix search                            | Active          |                                      |
| Phonetic matching (Soundex)                          | Active          |                                      |
| Synonym expansion                                    | Active          |                                      |
| FST term dictionary                                  | Active          | Rebuilt after every flush            |
| RRF hybrid ranking                                   | Active          |                                      |
| Nerve auto-start (embedded in binary)                | Active          | Needs Python 3                       |
| Deterministic embedder                               | Active          | Default fallback — zero dependencies |
| Ollama embedder                                      | Active          | Needs Ollama installed               |
| gRPC server (`zenith serve`)                         | Active          | Port 8080 default                    |
| fsnotify incremental watching                        | Active          |                                      |
| Persistent watchlist (`~/.zenith/watchlist.json`)    | Active          |                                      |
| Boot auto-start (Windows / Linux / macOS)            | Active          |                                      |
| `.txt` `.md` `.go` `.html` extractors                | Active          |                                      |
| PDF extraction                                       | Not implemented | Planned                              |
| OpenAI embedder                                      | Not implemented | Planned                              |
| Prometheus metrics                                   | Not implemented | Phase 6                              |
| OpenTelemetry traces                                 | Not implemented | Phase 6                              |

---

## How Embedding Works

ZENITH ships with the nerve embedding service **baked into the binary**. It does not depend on Python at install time — Python is only needed at runtime to get full semantic search.

On every invocation, ZENITH runs this cascade automatically:

```
1. Is nerve already running at 127.0.0.1:8000?  → use it immediately
2. Try to start nerve:
     a. Extract main.py to ~/.zenith/nerve/
     b. Create isolated virtualenv at ~/.zenith/nerve/venv/
     c. pip install fastapi uvicorn sentence-transformers  (first run only, ~90 MB)
     d. Start uvicorn as a detached background process
     e. Wait up to 20s for the health check
3. If Python not found       → try Ollama at localhost:11434
4. If Ollama not running     → use deterministic hash embeddings (fallback)
```

**First run** with Python installed takes 1–3 minutes (pip install + model download). Every run after that checks if the process is already up — if it is, startup is instant.

The nerve process survives the parent zenith process. Kill it manually if needed:

```bash
# macOS / Linux
pkill -f "uvicorn main:app"

# Windows PowerShell
Stop-Process -Name uvicorn -Force
```

### No Python? Use Ollama instead

```bash
ollama pull nomic-embed-text
# ollama serve starts automatically on most installs

zenith index --embedder ollama ~/Documents
zenith search --embedder ollama "query"
```

### Want deterministic only (offline, no external services)?

```bash
zenith index --embedder deterministic ~/Documents
zenith search --embedder deterministic "query"
```

---

## Embedder Flags

```
--embedder auto          nerve → Ollama → deterministic  (default)
--embedder nerve         nerve sidecar only (auto-managed)
--embedder ollama        local Ollama
--embedder deterministic hash-based, zero dependencies
--ollama-url   string    Ollama URL      (default: http://localhost:11434)
--ollama-model string    Ollama model    (default: nomic-embed-text)
--nerve-url    string    nerve URL       (default: http://127.0.0.1:8000)
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

| Component          | Implementation                            | Status |
| ------------------ | ----------------------------------------- | ------ |
| Write-Ahead Log    | Append-only, CRC-framed, crash-safe       | Active |
| MemTable           | Skip-list, sorted, O(log n) ops           | Active |
| SSTable            | Immutable block-structured sorted files   | Active |
| Group Committer    | Batches concurrent flushes into one fsync | Active |
| Leveled Compaction | Background goroutine, tombstone pruning   | Active |
| Bloom Filter       | Probabilistic O(1) disk-lookup bypass     | Active |
| Sparse Index       | Memory-efficient offset map per SSTable   | Active |
| FST Dictionary     | Rebuilt after every flush, prefix-resolve | Active |

---

## Search Pipeline

### Lexical

- Inverted index with BM25 and TF-IDF scoring
- Porter2 stemming (`jumping` → `jump`)
- Edge n-grams for prefix / search-as-you-type
- Phonetic matching via Soundex
- Synonym expansion

### Fuzzy

- BK-tree over Levenshtein distance, O(log n) via triangle inequality pruning
- Tolerates up to `FuzzyMaxDist` edits (default 2, configurable in `config.go`)

### Semantic

- Embedding via nerve (`all-MiniLM-L6-v2`, 384-dim), Ollama (`nomic-embed-text`, 768-dim), or deterministic fallback
- Vector cosine similarity stored as float16 to halve memory usage
- Embedding failures are non-fatal — the engine degrades to lexical-only

### Ranking

- Reciprocal Rank Fusion (RRF, k=60) merges lexical and semantic result lists
- BM25 as tiebreaker when RRF scores are within epsilon
- All weights tunable in `internal/config/config.go`

---

## File Support

| Format                                                    | Extraction                                |
| --------------------------------------------------------- | ----------------------------------------- |
| `.txt` `.md` `.log` `.csv` `.json` `.yaml`                | Raw UTF-8 text                            |
| `.go`                                                     | AST — identifiers, comments, package name |
| `.py` `.ts` `.js` `.jsx` `.tsx` `.rs` `.java` `.c` `.cpp` | Raw source                                |
| `.html` `.htm`                                            | Tag-stripped visible text                 |
| `.pdf`                                                    | Not implemented (planned)                 |

Changed files are re-indexed automatically when using `zenith watch`.

---

## gRPC API

`zenith serve` exposes the full search and indexing API over gRPC on port `:8080` by default.

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

### `zenith index`

Recursively walks a directory, extracts text from each supported file, and adds it to the local index (`zenith.db`). Running index twice is safe — documents are re-indexed idempotently.

```bash
# Index a directory
zenith index ~/Documents

# Index with a specific embedder
zenith index --embedder ollama ~/Documents

# Index to a custom database file
zenith index --db my-index.db ~/Projects

# Index with a custom FST path
zenith index --fst ./data/custom.fst ~/Documents
```

**Flags:**
```
--db           string   Index database file            (default: zenith.db)
--fst          string   On-disk FST path               (default: ./data/index.fst)
--embedder     string   auto|nerve|ollama|deterministic (default: auto)
--ollama-url   string   Ollama URL                     (default: http://localhost:11434)
--ollama-model string   Ollama model                   (default: nomic-embed-text)
--nerve-url    string   Nerve sidecar URL              (default: http://127.0.0.1:8000)
```

---

### `zenith search`

Loads the local index and runs a hybrid search query (lexical + fuzzy + semantic). No server needed — the engine runs in-process.

```bash
# Basic search
zenith search "kubernetes memory debugging"

# Show more results (default: 10)
zenith search -n 20 "kubernetes memory debugging"

# Single-word query
zenith search goroutine

# Multi-word phrase
zenith search "connection pool timeout"

# Search with a specific embedder
zenith search --embedder deterministic "offline query"

# Search a different database
zenith search --db my-index.db "query"

# Typo-tolerant — BK-tree fuzzy matching handles this automatically
zenith search "kubernets deployement"
```

**Flags:**
```
-n, --max int   Maximum results to display             (default: 10)
--db            string   Index database file           (default: zenith.db)
--embedder      string   auto|nerve|ollama|deterministic (default: auto)
```

---

### `zenith watch`

Manages a persistent list of directories to keep indexed in real time. File changes (create, modify, rename, delete) are detected immediately via fsnotify and re-indexed without manual intervention.

#### Persistent watching (recommended)

```bash
# Add a directory to the watchlist
zenith watch add ~/Documents

# Add multiple directories
zenith watch add ~/Documents
zenith watch add ~/Projects/notes
zenith watch add ~/Desktop

# View the current watchlist
zenith watch list

# Remove a directory from the watchlist
zenith watch remove ~/Desktop

# Start watching all directories in the watchlist (blocks until Ctrl-C)
zenith watch start

# Register auto-start so 'zenith watch start' runs on every boot
zenith watch install

# Remove the auto-start entry
zenith watch uninstall
```

**Where auto-start registers:**
| Platform | Location |
|---|---|
| Windows | Task Scheduler — task named `ZenithWatch`, triggers at user login (30s delay) |
| Linux | `~/.config/systemd/user/zenith-watch.service` |
| macOS | `~/Library/LaunchAgents/com.zenith.watch.plist` |

No administrator rights are required on any platform.

#### One-shot watching (not saved to watchlist)

```bash
# Watch a directory once (not added to watchlist)
zenith watch run ~/Downloads

# Watch and bulk-index first
zenith watch run --index-first ~/Documents

# Watch with a specific embedder
zenith watch run --embedder ollama ~/Documents
```

**Flags for `watch run`:**
```
--index-first   Bulk-index directory before starting the watcher
--db            string   Index database file           (default: zenith.db)
--embedder      string   auto|nerve|ollama|deterministic (default: auto)
```

---

### `zenith log`

Displays the persistent activity log at `~/.zenith/zenith.log`.

**Event types:** `INDEXED`, `REMOVED`, `SEARCH`, `NERVE`, `SAVED`, `LOADED`, `PDF`

```bash
# Show last 50 events (default)
zenith log

# Show last 100 events
zenith log -n 100

# Show all events (no limit)
zenith log -n 0

# Stream new events live (like tail -f)
zenith log -f

# Filter to a specific event type
zenith log --type SEARCH
zenith log --type INDEXED
zenith log --type NERVE

# Filter and follow live
zenith log --type INDEXED -f

# Last 20 search events
zenith log -n 20 --type SEARCH
```

**Flags:**
```
-n, --lines int    Number of lines to show             (default: 50)
-f, --follow       Stream new events live (Ctrl-C to stop)
    --type string  Filter by event type (SEARCH, INDEXED, REMOVED, NERVE, SAVED, LOADED)
```

---

### Other commands

```bash
# Start the gRPC server (port 8080 by default)
zenith serve
zenith serve --port 9090

# Print version
zenith version

# Remove the binary and nerve sidecar (preserves index data)
zenith uninstall

# Update to the latest version
zenith update
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
- [x] Nerve sidecar embedded in binary — auto-starts on first run
- [x] Ollama embedder (`nomic-embed-text`, fully offline)
- [x] Deterministic hash embedder (zero-dependency fallback)
- [x] Auto cascade: nerve → Ollama → deterministic
- [x] Per-file type text extractors (`.md`, `.go`, `.html`, raw source)
- [x] 94 unit + integration tests across all critical packages
- [ ] OpenAI embedder
- [ ] PDF text extraction

### Phase 6 — Production Observability (planned)

- [ ] Prometheus metrics exporter
- [ ] OpenTelemetry trace spans across the full query path
- [ ] `make dev-stack` — local Prometheus + Grafana + Tempo
- [ ] `/metrics` endpoint on `zenith serve`

---

## Configuration

All tunable parameters are in `internal/config/config.go` (`DefaultConfig()`).

| Parameter         | Default                 | Description                     |
| ----------------- | ----------------------- | ------------------------------- |
| `FuzzyMaxDist`    | `2`                     | BK-tree edit distance threshold |
| `RRFConstant`     | `60.0`                  | RRF k value                     |
| `PhoneticWeight`  | `0.3`                   | Phonetic signal blend weight    |
| `VectorWeight`    | `0.7`                   | Vector signal blend weight      |
| `NeuralWeight`    | `1.0`                   | Neural signal blend weight      |
| `NerveURL`        | `http://127.0.0.1:8000` | Nerve sidecar URL               |
| `NerveTimeout`    | `5s`                    | Per-request timeout to nerve    |
| `MemTableMaxSize` | `64 MB`                 | SSTable flush threshold         |
| `CommitWindow`    | `4 ms`                  | Group-committer batch window    |

---

## Design Decisions

**Why LSM and not B-tree?**
LSM trees optimize for write throughput — critical for indexing large directories quickly. Reads are fast enough with Bloom filters handling the "does this key exist?" check before any disk access.

**Why BK-tree and not a hashmap for fuzzy search?**
The hashmap approach is O(n) on every fuzzy query — it scans the entire term dictionary. A BK-tree prunes the search space using the triangle inequality of edit distance, giving O(log n) average case. At 100k indexed terms the difference is measurable.

**Why embed nerve in the binary?**
A compiled Go binary cannot run Python. Embedding `main.py` and bootstrapping a virtualenv on first run means the binary is self-contained: users with Python 3 get full semantic search automatically, and users without fall back gracefully. No separate sidecar installer, no documentation step that says "also run this other thing."

**Why not use an existing vector database?**
Because the point of ZENITH is to understand the storage layer — not consume it. The vector store is intentionally built on top of the same LSM engine that handles the inverted index. One storage backend, two index types.

---

## Project Structure

```
ZENITH/
├── cmd/
│   ├── zenith/               # CLI entrypoint (cobra) — index, search, watch, serve, log
│   └── server/               # standalone gRPC server
├── internal/
│   ├── analysis/             # tokenizer, stemmer, n-grams, phonetic, BK-tree, FST
│   ├── crawler/              # fsnotify file watcher + text extractors
│   ├── watchlist/            # persistent watchlist (~/.zenith/watchlist.json)
│   ├── autostart/            # OS boot auto-start (Windows/Linux/macOS)
│   ├── embedding/            # Ollama, nerve, deterministic adapters + LRU cache
│   ├── nervemanager/         # nerve lifecycle: embed, extract, venv, auto-start
│   │   └── assets/           # main.py + requirements.txt (baked into binary)
│   ├── storage/              # WAL, MemTable, SSTable, Bloom filter, compaction
│   ├── index/                # inverted index + vector store + search orchestrator
│   ├── ranking/              # RRF + BM25 tiebreak + TF-IDF
│   └── config/               # all tunable parameters
├── pkg/zenith/               # public API types
├── gen/go/zenithproto/       # generated Protobuf
└── nerve/                    # Python embedding sidecar (source, for development)
```

---

## Contributing

ZENITH is built in public. Issues and PRs are open.

Every component has a clear boundary and a reason to exist. If you are working on storage engines, search infrastructure, or NLP pipelines in Go this is a good place to dig in.

---

## License

MIT
