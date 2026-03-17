# ZENITH — Improvement & Assessment Guide

> Comprehensive architectural review and optimization blueprint.
> Generated from full codebase analysis of all 13 Go source files, Python nerve service, and proto definitions.

---

## Executive Summary

ZENITH has a **solid foundational architecture** — gRPC transport, inverted indexing, Porter stemming, phonetic/fuzzy search, RRF ranking, and neural embeddings via a Python sidecar. However, the codebase has **critical gaps** that will become blockers at scale:

| Priority | Issue | Impact |
|----------|-------|--------|
| 🔴 P0 | Single global `sync.RWMutex` bottleneck in `InMemoryIndex` | Throughput ceiling under concurrent load |
| 🔴 P0 | No test files exist (`*_test.go` = 0 files) | Zero regression safety net |
| 🔴 P0 | FNV32 hash collisions for document IDs | Silent data corruption at scale |
| 🟠 P1 | `gob` persistence — not crash-safe, no WAL | Data loss on unclean shutdown |
| 🟠 P1 | Brute-force vector scan in `Search()` — O(n) per query | Latency degrades linearly with corpus |
| 🟠 P1 | Embedding HTTP call inside `Add()` — blocks indexing | One slow nerve response stalls everything |
| 🟡 P2 | Regex compiled on every `Tokenize()` call | Unnecessary CPU waste |
| 🟡 P2 | No metrics, no structured logging | Blind in production |
| 🟡 P2 | Magic scoring constants (50000, 20000, 10000) hardcoded | Impossible to tune without recompilation |

**The detailed sections below are split into companion files for readability:**

- [Part A: Phase 2 Critique (Neural Intelligence)](./docs/PART_A_PHASE2_CRITIQUE.md)
- [Part B: Phase 3 Completion (Linguistic Mastery)](./docs/PART_B_PHASE3_LINGUISTIC.md)
- [Part C: Phase 4 Blueprint (LSM-Tree Storage)](./docs/PART_C_PHASE4_LSM.md)
- [Part D: Cross-Cutting Concerns](./docs/PART_D_CROSS_CUTTING.md)
- [Part E: Code Quality Improvements](./docs/PART_E_CODE_QUALITY.md)

---

## Priority-Ordered Action Items

### Must-Have (Before Phase 4)
1. **Replace FNV32 with FNV64 or use string IDs directly** — hash collisions are a data-corruption vector
2. **Add unit tests** for stemmer, Soundex, Levenshtein, cosine similarity, RRF ranking
3. **Pre-compile regex** in tokenizer (move to `init()` or struct field)
4. **Make scoring weights configurable** via a `ScoringConfig` struct
5. **Add embedding cache** with LRU eviction to reduce nerve calls
6. **Introduce `context.Context`** propagation through `Add()` and `Search()`

### Should-Have (During Phase 4)
7. **Implement WAL before replacing gob persistence**
8. **Add structured logging** (slog) with request tracing
9. **Shard the RWMutex** — per-term or per-bucket locking
10. **Background embedding** pipeline with batching

### Nice-to-Have (Post Phase 4)
11. **HNSW or IVF index** for vector search (replace brute-force)
12. **Prometheus metrics** endpoint
13. **Connection pooling** for nerve HTTP client
14. **Admin gRPC service** for runtime configuration
