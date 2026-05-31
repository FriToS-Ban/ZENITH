# ZENITH

> A local-first semantic search engine with an LSM-backed index, written in Go.

ZENITH is a CLI tool that indexes your local files and makes them searchable — not just by keyword, but by meaning. Point it at a directory, and you can query it with natural language. It understands intent, tolerates typos, and ranks results using hybrid lexical + semantic scoring.

Under the hood, ZENITH is built on a storage engine written from scratch: a full LSM-tree pipeline (WAL → MemTable → SSTable → Bloom filters), a BK-tree fuzzy matcher, an RRF-based hybrid ranker, and an optional gRPC server for remote access. Every component is instrumented with Prometheus metrics and OpenTelemetry traces.

This is not a wrapper around Elasticsearch or a vector database. It is a ground-up implementation of the primitives that make search work.

---

## Quick Start

```bash
# Install
go install github.com/shramanb113/ZENITH/cmd/zenith@latest

# Index a directory
zenith index ~/Documents

# Search by meaning
zenith search "kubernetes memory debugging"

# Typo-tolerant fuzzy search
zenith search --fuzzy "kubrnets deplymnt"

# Start gRPC server for remote access
zenith serve --port 50051
```

---

## Why ZENITH Exists

Most search tools make one of two tradeoffs:

- **Elasticsearch** — powerful, but vector search is bolted on, not native. Two scoring pipelines that don't compose cleanly. Memory-hungry at scale. Configuration-heavy for advanced use cases.
- **Vector databases** — great for semantic recall, but lose lexical precision. No concept of "exact match matters here."

ZENITH treats hybrid search as the foundation, not a feature. A single query pipeline scores both lexical and semantic signals and merges them with Reciprocal Rank Fusion. You get exact match precision without sacrificing semantic recall.

For local use, it also means zero infrastructure — no Elasticsearch cluster, no managed vector DB, no cloud bill. Your index lives on disk in ZENITH's own LSM storage format.

---

## Architecture

```
zenith index <path>
      │
      ▼
┌─────────────┐     ┌──────────────┐     ┌─────────────────┐
│   Crawler   │────▶│   Analyzer   │────▶│    Embedder     │
│  (fsnotify) │     │  (tokenizer, │     │  (Ollama local  │
│             │     │   stemmer,   │     │   or OpenAI)    │
│             │     │   n-grams,   │     │                 │
│             │     │   phonetic)  │     │  → []float32    │
└─────────────┘     └──────────────┘     └────────┬────────┘
                                                   │
                          ┌────────────────────────┘
                          ▼
                  ┌───────────────┐
                  │  LSM Storage  │
                  │               │
                  │  WAL (crash   │
                  │  recovery)    │
                  │  MemTable     │
                  │  (skip-list)  │
                  │  SSTable      │
                  │  (immutable)  │
                  │  Bloom filter │
                  │  Sparse index │
                  └───────┬───────┘
                          │
zenith search <query>     │
      │                   ▼
      │           ┌───────────────┐
      │           │  Query Engine │
      ├──────────▶│               │
      │  lexical  │  BM25 scoring │
      │           │  BK-tree      │
      │           │  fuzzy match  │
      │           │  Vector cosine│
      │           │  similarity   │
      │           │               │
      │           │  RRF fusion   │
      │           │  → ranked     │
      │           │    results    │
      │           └───────────────┘
      │
      └──▶ zenith serve → gRPC server (remote clients)
```

---

## Storage Engine

ZENITH's index is backed by a full LSM-tree implementation — the same architecture used by RocksDB, LevelDB, and etcd's backend storage. Nothing is outsourced to SQLite or an embedded key-value library.

| Component       | Implementation                           | Status |
| --------------- | ---------------------------------------- | ------ |
| Write-Ahead Log | Append-only, crash-safe                  | ✅     |
| MemTable        | Skip-list (sorted, O(log n) ops)         | ✅     |
| SSTable         | Immutable disk-backed sorted tables      | ✅     |
| Compactor       | Leveled compaction, background merge     | ✅     |
| Bloom Filter    | Probabilistic O(1) disk-lookup bypass    | ✅     |
| Sparse Index    | Memory-efficient offset map for SSTables | ✅     |

The WAL guarantees that no indexed document is lost on crash. Bloom filters mean queries never hit disk for documents that don't exist. Compaction keeps read amplification bounded as the index grows.

---

## Search Pipeline

### Lexical Layer

- Inverted index with BM25/TF-IDF scoring
- Porter stemming (`jumping` → `jump`)
- Edge N-grams for prefix / search-as-you-type
- Phonetic matching via Soundex/Metaphone
- Synonym expansion

### Fuzzy Layer

- BK-tree over Levenshtein distance — O(log n) lookup with edit-distance pruning
- Tolerates up to `MAX_DISTANCE` edits (configurable)
- Integrated into both indexing and query paths

### Semantic Layer

- Local embeddings via Ollama (`nomic-embed-text`, runs fully offline)
- Optional OpenAI embeddings (`--embedder openai`)
- Cosine similarity + L2 distance
- Deterministic hash embeddings as fallback (no Ollama required)

### Ranking

- Reciprocal Rank Fusion (RRF) merges lexical and semantic result lists
- Score normalization before fusion
- Weighted fusion tunable via config

---

## Observability

ZENITH instruments itself the way a production service should. When running `zenith serve`, the following are exposed:

**Prometheus metrics** (`/metrics` on configurable port):

| Metric                           | Description                                                |
| -------------------------------- | ---------------------------------------------------------- |
| `zenith_index_duration_seconds`  | Time to index each file, by file type                      |
| `zenith_search_latency_seconds`  | Query latency by search mode (lexical / semantic / hybrid) |
| `zenith_indexed_documents_total` | Total documents in the index                               |
| `zenith_embedding_calls_total`   | Embedder invocations and errors                            |
| `zenith_wal_writes_total`        | WAL append operations                                      |
| `zenith_bloom_filter_hits_total` | Bloom filter hit/miss ratio                                |

**OpenTelemetry traces**: spans across the full query path — `crawler.Walk` → `embedder.Embed` → `storage.Write` → `search.Query` → `ranker.RRF`. Export to any OTLP-compatible backend (Grafana Tempo, Jaeger).

Run the full local observability stack:

```bash
make dev-stack   # boots Prometheus + Grafana + Tempo via docker-compose
zenith serve --metrics-port 9090 --tracing-endpoint localhost:4317
```

---

## File Support

| Format              | Extraction                                    |
| ------------------- | --------------------------------------------- |
| `.txt`, `.md`       | Direct text                                   |
| `.go`, `.py`, `.ts` | Source code (comment + identifier extraction) |
| `.pdf`              | Text layer extraction                         |
| `.html`             | Tag-stripped text                             |

ZENITH watches indexed directories with `fsnotify`. Changed files are re-indexed incrementally using mtime + content hash — not full re-crawls.

---

## gRPC API

`zenith serve` exposes the full search and indexing API over gRPC. The Protobuf contract is in `gen/go/zenithproto/`.

```protobuf
service ZenithService {
  rpc IndexDocument(IndexRequest) returns (IndexResponse);
  rpc Search(SearchRequest) returns (SearchResponse);
  rpc FuzzySearch(FuzzyRequest) returns (SearchResponse);
  rpc HybridSearch(HybridRequest) returns (SearchResponse);
  rpc GetStats(StatsRequest) returns (StatsResponse);
}
```

---

## Build Roadmap

### ✅ Phase 1 — Core Foundation

- [x] Inverted index, map-based postings lists
- [x] Tokenization, stop-word filtering, lowercasing
- [x] gRPC service definition and Protobuf contract
- [x] Thread-safety via `sync.RWMutex`
- [x] Concurrent indexing via worker pools
- [x] Binary persistence with `encoding/gob` and graceful shutdown

### ✅ Phase 2 — Neural Intelligence

- [x] Distributed coordinator and modulo sharding
- [x] Vector map integration and high-dimensional schema
- [x] Dot product and magnitude in pure Go
- [x] Cosine similarity and L2 distance
- [x] Deterministic hash embeddings
- [x] Hybrid ranking with score normalization and weighted fusion
- [x] Reciprocal Rank Fusion (RRF)

### ✅ Phase 3 — Linguistic Mastery

- [x] Porter stemming
- [x] Edge N-grams
- [x] Phonetic matching (Soundex/Metaphone)
- [x] Levenshtein distance
- [x] BK-tree fuzzy matching (wiring in progress)
- [x] Synonym expansion
- [x] Finite State Transducers — future enhancement

### ✅ Phase 4 — Storage Engine

- [x] Write-Ahead Log (WAL)
- [x] MemTable skip-list (replacing current hashmap)
- [x] SSTables (group committer improvement pending)
- [x] Leveled compaction
- [x] Bloom filters
- [x] Sparse index

### 🔧 Phase 5 — CLI + Local Embeddings (current)

- [x] `cobra` CLI: `index`, `search`, `serve`, `version`
- [x] File crawler with `fsnotify` incremental watching
- [x] Ollama embedder integration (`nomic-embed-text`)
- [ ] OpenAI embedder (optional flag)
- [x] Per-file type text extractors (`.md`, `.go`, `.pdf`, `.html`)
- [ ] `goreleaser` binary releases

### 🔲 Phase 6 — Production Observability

- [ ] Prometheus metrics exporter (custom Go instrumentation)
- [ ] OpenTelemetry trace spans across full query path
- [ ] `make dev-stack` — local Prometheus + Grafana + Tempo
- [ ] `/metrics` endpoint on `zenith serve`

### 🔲 Phase 7 — Storage Hardening

- [x] MemTable skip-list replacement
- [x] Leveled compaction background worker
- [x] SSTable group committer
- [x] WAL recovery benchmarks

---

## Design Decisions

**Why LSM and not B-tree?**
LSM trees optimize for write throughput — critical for indexing large directories quickly. Reads are fast enough with Bloom filters handling the "does this key exist?" check before any disk access.

**Why BK-tree and not a hashmap for fuzzy search?**
The hashmap approach is O(n) on every fuzzy query — it scans the entire term dictionary. A BK-tree prunes the search space using the triangle inequality of edit distance, giving O(log n) average case. At 100k indexed terms, the difference is measurable.

**Why Ollama for embeddings?**
Fully local, zero cost, no data leaves the machine. `nomic-embed-text` runs on CPU, produces 768-dimensional vectors, and is fast enough for interactive search latency. OpenAI is available as an opt-in for higher quality on larger corpora.

**Why not use an existing vector database?**
Because the point of ZENITH is to understand the storage layer — not consume it. The vector store is intentionally implemented on top of the same LSM engine that handles the inverted index. One storage backend, two index types.

---

## Project Structure

```
ZENITH/
├── cmd/zenith/          # CLI entrypoint (cobra)
├── internal/
│   ├── analysis/        # tokenizer, stemmer, n-grams, phonetic, fuzzy, BK-tree
│   ├── crawler/         # fsnotify file watcher + text extractors
│   ├── embedder/        # Ollama + OpenAI adapters
│   ├── storage/         # WAL, MemTable, SSTable, Bloom filter, sparse index
│   ├── index/           # inverted index + vector map
│   ├── ranker/          # RRF + hybrid scoring
│   └── metrics/         # Prometheus exporters + OTel spans
├── pkg/zenith/          # public API types
├── gen/go/zenithproto/  # generated Protobuf
├── nerve/               # gRPC server (zenith serve)
└── deploy/
    └── docker-compose.yml  # Prometheus + Grafana + Tempo dev stack
```

---

## Contributing

ZENITH is built in public. Issues and PRs are open.

If you're working on distributed systems, storage engines, or search infrastructure in Go — this is a good place to dig in. Every component has a clear boundary and a reason to exist.

---

## License

MIT
