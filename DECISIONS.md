# Architectural Decisions

This document records the significant architectural decisions made in ZENITH — the reasoning behind each choice, the tradeoffs accepted, and the strategic thinking that led here. It is intended for:

- **Users** who hit a limitation and want to understand why it exists
- **Engineers** evaluating ZENITH as a library who need to understand the dependency graph before importing it
- **Contributors** who want context before proposing changes
- **Anyone** who wants to understand *why* ZENITH is built the way it is, not just *what* it does

---

## Positioning

### The problem ZENITH solves (and the fight it refuses to have)

The obvious framing for ZENITH is "a fast search engine built in Go." That puts it in a fight it cannot win:

- **Meilisearch** — fast, great DX, written in Rust, has a team and production deployments
- **Typesense** — fast, cloud-native, written in C++
- **Quickwit** — log search, Rust
- **Elasticsearch** — the enterprise incumbent with a decade of adoption

Competing on raw feature count or benchmark numbers against teams with dedicated resources is a losing strategy. There is no version of that fight where ZENITH wins.

But ZENITH has something none of those have: **it can run inside a Go application, as a library, with zero external services.** No Docker. No separate server process. No ops overhead. Just `import` the package and call three methods.

The correct positioning is not "search engine." It is **embeddable search for Go applications.**

### The SQLite analogy

SQLite did not compete with PostgreSQL or MySQL on features. It won by being embeddable, zero-config, and single-file. That is a completely different category — one nobody else owned at the time. SQLite is now the most widely deployed database in the world.

ZENITH's wedge is identical: stop saying "search engine," start saying "embeddable search for Go apps." That is a category nobody else owns.

The one-line pitch: **ZENITH is to search what SQLite is to databases — zero dependencies, embeds in your app, ships in your binary.**

### Target audience

The primary user is a **Go web app developer** — someone building a Go HTTP or gRPC service who wants full-text and semantic search over their own data (blog posts, products, documents, notes) without standing up Elasticsearch or paying for a hosted search API.

This audience was chosen over CLI developers and desktop app developers for two reasons:

1. **Market size.** Go web services are the dominant Go use case. Almost every Go backend developer has hit the "I need search" moment and reached for Elasticsearch, then grimaced at the ops burden.
2. **Virality and hiring.** The demo that goes viral on X is "I added semantic search to my Go app in 5 lines, no Docker." That framing resonates with the widest audience of Go engineers and is understood immediately by infrastructure and backend companies that hire Go developers (Cloudflare, Fly.io, PlanetScale, etc.).

---

## The Credibility Gap (Problem 1)

### Why the Python sidecar had to go

An earlier version of ZENITH used a Python process (`nerve`) for ML inference and PDF extraction. The pitch was "pure Go, single binary, drop it anywhere." The reality was a Python interpreter + venv + 600 MB of packages required before the binary would function.

That is a **credibility gap** — a claim the code directly contradicts. Any engineer who reads the README and then reads the code will catch it immediately and mentally discount the whole project. The equivalent of saying "zero dependencies" and having a `requirements.txt`.

The three compounding problems:

1. **Two runtimes.** Both the Go process and the Python process had to be alive simultaneously. If `nerve` crashed, ZENITH degraded silently or failed entirely.
2. **IPC latency.** Every embed call crossed a gRPC boundary — 5–15 ms per call plus a new failure mode. This is latency you pay for nothing; the network hop exists only because the inference runs in a different process.
3. **Deployment surface.** Users needed Python 3.10+, `pip`/`uv`, and a `zenith setup` step that took up to 20 minutes on a cold machine. The "drop it in and it works" promise dies here.

The fix: run everything in-process. One binary, one runtime, embeddings happen inside the same process. Cold start drops from 30–90 seconds to under one second.

---

## Technical Decisions (Problem 1)

### 1. Why there is no Python sidecar

**Decision:** Embeddings, PDF extraction, and all ML inference run in-process inside the Go binary.

- `all-MiniLM-L6-v2` runs in-process via `yalue/onnxruntime_go` (ONNX Runtime CGo binding)
- The ONNX model file (~22 MB, int8 quantized) is embedded in the binary via `go:embed`
- PDF text extraction uses `ledongthuc/pdf` (pure Go)
- No Python interpreter. No venv. No IPC. Cold start drops from 30–90 seconds to under 1 second.

---

### 2. ONNX Runtime vs llama.cpp

**Decision:** `yalue/onnxruntime_go` (ONNX Runtime) over llama.cpp Go bindings.

**Why ONNX wins for this specific task:**

`all-MiniLM-L6-v2` is a BERT-style encoder-only model. It produces fixed-size sentence embeddings in a single forward pass. ONNX Runtime is purpose-built for this workload — sub-millisecond inference on CPU, the int8 export exists on HuggingFace, ~22 MB.

llama.cpp is designed for autoregressive decoder models (generative LLMs). Using it for an encoder-only embedding model is the wrong tool: heavier CGo build, ~45 MB GGUF model, slower inference for this architecture, and the GGUF conversion pipeline is itself Python-based.

**Where llama.cpp would be the right choice:**

If ZENITH ever adds native image captioning (replacing the dropped BLIP), llama.cpp with a small GGUF vision model (LLaVA-style) is the correct path. Additive change — does not require revisiting this decision.

**Distribution:** both approaches use the same strategy — `go generate` downloads the model from HuggingFace, file is in `.gitignore`, CI runs `go generate` before `go build`. Model file size is not a differentiator.

---

### 3. go:embed over download-on-first-run for model distribution

**Decision:** Embed the ONNX model (~22 MB) directly in the binary via `go:embed`. Not downloaded on first run.

A download-on-first-run approach keeps the binary small but requires network access on first use and a setup step before the library functions. This directly contradicts the embeddable library pitch — if you import ZENITH as a library in your app, there is no "first run setup ceremony." The user calls `zenith.Open()` and it works. A library that phones home on first use is not embeddable in the SQLite sense.

22 MB is a reasonable tradeoff for zero-setup semantics. SQLite's amalgamation is 237,000 lines of C — size was never the concern, self-containedness was.

---

### 4. CGo is acceptable; Python is not

**Decision:** CGo dependencies (`yalue/onnxruntime_go`) are an accepted part of the build. Python as a runtime dependency is not.

**Why this distinction matters:**

CGo means the binary requires a C runtime and must be compiled for its target platform. This is the same constraint as SQLite (`mattn/go-sqlite3`). It does not break the "single binary" or "embeddable library" pitches — the binary is still a single compiled artifact. Library consumers do not manage a separate runtime.

Python as a runtime dependency means users need an interpreter, a package manager, a virtualenv, and hundreds of MB of packages before the software works at all. That is a fundamentally different category of dependency.

**On Windows:** CGo requires MinGW-w64 (`C:\msys64\ucrt64\bin` via MSYS2). Install with:
```
winget install -e --id MSYS2.MSYS2
# then in MSYS2 terminal:
pacman -S --noconfirm mingw-w64-ucrt-x86_64-gcc
```
Add `C:\msys64\ucrt64\bin` to PATH, then `CGO_ENABLED=1 go build ./...` works natively.

**The `!cgo` fallback:** A `model_nocgo.go` stub (build tag `!cgo`) allows the binary to compile without a C compiler. `localembedder.New()` returns an error in that build, and the engine falls back to deterministic (hash-based) embeddings. Lexical search still works; only neural semantic search is degraded. This means `go install` without MinGW still produces a working binary — just without ONNX inference.

---

### 5. Pure-Go WordPiece tokenizer vs `daulet/tokenizers`

**Decision:** A ~300-line pure-Go WordPiece tokenizer over `daulet/tokenizers` (CGo binding to HuggingFace Rust tokenizers).

`daulet/tokenizers` gives byte-for-byte tokenization parity with Python SentenceTransformers. But it adds a second CGo dependency, a Rust toolchain requirement at build time, and more static linking complexity.

WordPiece for `all-MiniLM-L6-v2` is straightforward: lowercase → punctuation splitting → vocab lookup → `[UNK]` fallback. For English and Latin-script text, the pure-Go implementation is identical to HuggingFace output. Edge cases exist for CJK and some Unicode combining sequences — these do not materially affect search quality for the expected user base.

**If exact Python parity is needed:** swap `internal/localembedder/tokenizer.go` for a `daulet/tokenizers`-backed implementation. The interface is internal; the change is one file.

---

### 6. pdfcpu vs PyMuPDF

**Decision:** `ledongthuc/pdf` (pure Go) over PyMuPDF (`fitz`, C library via Python).

**What you gain:** pure Go, no CGo for PDF parsing, no Python dependency.

**What you accept:** less capable than `fitz` on malformed or exotic PDFs. `fitz` is a battle-hardened C library. A small fraction of PDFs that `fitz` handles silently will produce a clear error from the Go library.

**Upgrade path:** a CGo binding to `mupdf` (the C library behind PyMuPDF) could replace this without touching anything outside `internal/pdf/`. The interface is stable.

---

### 7. BLIP image captioning removed

**Decision:** Drop BLIP (`Salesforce/blip-image-captioning-base`). Index image files by filename and directory path instead.

BLIP was off by default (`ZENITH_BLIP_CAPTIONS=1` required). Getting it running in Go without Python requires a complex ONNX export of the full encoder-decoder architecture or a GGUF vision model via llama.cpp. Neither is worth the complexity for a feature nobody was using by default.

Replacement: decompose the file path into search tokens. `~/Photos/2024/vacation/portrait_sunset_beach.jpg` → `"portrait sunset beach vacation 2024 photo jpeg"`. Same strategy used by macOS Spotlight and Windows Search.

**Upgrade path:** small GGUF vision model via llama.cpp Go bindings. Additive — only `internal/image/indexer.go` changes.

---

## API Design (Problem 2)

### 8. The embeddable library design

**Decision:** Expose ZENITH as a public Go library via `pkg/zenith/`, with a `database/sql`-style API augmented by functional options.

**Why this API shape:**

The Go community has a well-established, widely-loved pattern for configurable libraries: a simple struct-based API (like `database/sql`, `bbolt`, `badger`) combined with functional options for power-user configuration (like `zap`, `grpc-go`). This pattern was popularised by Dave Cheney's 2014 post on functional options and is now the de facto standard for production Go libraries.

Three patterns were considered:

- **A — struct-based only** (`db.Add(ctx, id, text)`): hits a wall when you need extensibility. Adding a second embedder or BM25 weight tuning means a breaking API change.
- **B — functional options only** (`zenith.Open("search.db", zenith.WithEmbedder(...))`): verbose on line one, which contradicts the "zero friction" pitch.
- **C — generics** (`zenith.New[Article](...)`): ORM territory. The SQLite analogy breaks the moment the library requires type parameters. Adds complexity and requires Go 1.18+ generics familiarity.

The winner is **A + B combined**: a dead-simple happy path that reads like `database/sql`, with functional options available for power users who want to tune behaviour.

**The five methods a developer needs to know:**

```go
db, err := zenith.Open("search.db")   // like sql.Open
db.Add(ctx, "id", "text content")     // like db.Exec
results, _ := db.Search(ctx, "query") // like db.Query
db.Delete(ctx, "id")
db.Close()
```

That's it. Same surface area as `database/sql`. Anyone who has written Go knows this shape. The demo tweet writes itself: "I added semantic search to my Go app in 5 lines."

**Power user path** (functional options, no breaking changes):

```go
db, err := zenith.Open("search.db",
    zenith.WithEmbedder(myEmbedder),  // bring your own embedder
    zenith.WithBM25Only(),            // disable vector search
)
```

**Reference implementations that use this exact pattern:**
- `bbolt` / `badger` — struct API, options on `Open`
- `zap` — functional options (`zap.New(core, zap.Option...)`)
- `grpc-go` — `grpc.Dial(addr, grpc.WithTransportCredentials(...))`
- `database/sql` — `sql.Open(driver, dsn)`

### 9. Persistent by default, in-memory on demand

**Decision:** `zenith.Open("search.db")` persists. `zenith.Open(":memory:")` does not. Same function, one argument decides the mode. Directly mirrors SQLite's behaviour.

In-memory mode is not a "lightweight" option — it is a **testing feature**. Every engineering team that matters runs tests with in-memory databases. The ability to write `zenith.Open(":memory:")` in a test signals that this library was designed to be tested. That is a major adoption signal. Libraries you cannot easily test do not get adopted by serious teams.

- Persistent in production — data survives pod restarts in Kubernetes, process crashes, deploys
- In-memory in tests — fast, isolated, parallel-safe, zero cleanup

No magic paths. No library-managed directories. The caller decides where the data lives. This is the pattern that works in containerised deployments where you control exactly which volumes are mounted.

---

### 10. Two products, one engine, one repository

**Decision:** ZENITH ships as both a CLI tool (`cmd/zenith`) and a Go library (`pkg/zenith`), built on top of the same internal engine (`internal/index/engine.go`).

This is the most misunderstood part of the architecture, so it deserves a clear explanation.

**The CLI** (`go install`) installs a binary called `zenith` into `$GOPATH/bin`. Users run it from their terminal — `zenith index ~/Documents`, `zenith search "query"`. It is a standalone program that lives on your machine.

**The library** (`go get`) adds ZENITH as a dependency to a Go project. It goes into `go.mod`. The developer writes:

```go
import "github.com/shramanb113/ZENITH/pkg/zenith"

db, _ := zenith.Open("search.db")
db.Add(ctx, "id", text)
results, _ := db.Search(ctx, "query")
```

ZENITH runs **inside their application**, in the same process, with no separate binary. No `zenith` command. No setup. Just a function call.

These are **two different products** sharing the same engine. The CLI is ZENITH the tool. The library is ZENITH the platform — the thing you embed in your app. Same search logic, same embeddings, same storage engine, two entry points.

**The facade pattern:** `pkg/zenith/` is a thin public facade over `internal/index/engine.go`. The facade translates the clean five-method API (`Open`, `Add`, `Search`, `Delete`, `Close`) into the internal engine's operations. Internal types (`BKTree`, `VectorStore`, `InvertedIndex`) never leak through — the public API is fully stable regardless of internal changes.

This is the same pattern used by `database/sql` — the standard library exposes a clean `DB` type while every driver implementation lives behind an interface. Users of `database/sql` have never seen a `btree.Node` or a `page.Header`. That is the goal here.

---

### 11. Why "SQLite of search" is the right wedge

The competitors (Meilisearch, Typesense, Quickwit, Elasticsearch) are all **server processes** — you run them separately and talk to them over HTTP. That makes them:
- Ops overhead to deploy and maintain
- A network hop away from your code
- An external dependency your app cannot ship without

ZENITH is **a library** — it compiles into your binary and runs in your process. That is a completely different product category that none of the above compete in. You are not trying to be faster than Meilisearch. You are trying to be in the same relationship to search that SQLite is to databases: the thing you reach for when you want search *inside* your app, not *next to* your app.

SQLite did not win by out-featuring PostgreSQL. It won by being embeddable, zero-config, and single-file — a category nobody else owned. SQLite is now the most widely deployed database in the world, running in every iPhone, every Android device, every browser.

ZENITH owns the same category for search. The wedge nobody else has claimed: **`go get` and you have production-quality hybrid search in your app, with no infrastructure to manage and nothing to deploy.** That is the pitch. That is the category. And right now, nobody else is in it.

---

## Library Engineering: Edge Cases, Failure Modes, and Breaking Criteria

This section documents every edge case, failure mode, and silent breaking criterion identified during the design of `pkg/zenith`. It exists because these issues will surface in production and in technical interviews — understanding *why* each one breaks and *how* it is fixed is more valuable than just knowing the fix.

---

### Critical: FNV-32 Hash Collision (Data Corruption at Scale)

The internal engine stores every document under a `uint32` FNV-32a hash of its string ID:

```go
h := fnv.New32a()
h.Write([]byte(originalID))
internalID := uint32(h.Sum32()) // 2^32 possible values
```

Birthday problem: at **92,000 documents** there is a 1% chance of at least one collision. A collision means document B silently overwrites document A — A's text disappears from search results with no error, no warning, and no way to detect it from the outside.

**Fix:** replace `uint32` internal IDs with the full string key in all internal maps. Eliminates collisions entirely. This must be fixed before the library ships.

---

### Critical: Gob Corruption on Crash (Atomic Write)

The engine saves its entire state to a single gob file. A mid-write SIGKILL (OOM kill, power loss, `kill -9`) leaves a partially-written file — valid gob header, truncated body. Next `Open` fails with a confusing decode error and the user believes their data is lost.

**Fix:** write to `zenith.db.tmp`, then `os.Rename` atomically. Rename is atomic at the filesystem level on all major operating systems — the reader always sees either the old complete file or the new complete file, never a partial write.

---

### Critical: No Format Version Header (Silent Upgrade Breakage)

The gob file has no version marker. When the library is upgraded and an internal struct changes, `Open` fails with `gob: type mismatch in decoder: want struct type ...` — a message that means nothing to the user.

**Fix:** prepend a 4-byte magic (`ZNTH`) and 2-byte version number to every saved file. On `Open`, check the magic and version. Unknown version returns `ErrIncompatibleVersion` with a human-readable message and migration instructions.

---

### Critical: BM25 Statistics Not Serialised (Wrong Scores After Restart)

BM25 scoring requires corpus-level statistics: total document count, average document length, per-term document frequency. These are maintained in memory. If gob serialisation omits them (unexported fields are skipped by gob), every `Open` starts BM25 from zero. Loaded documents have no term frequency records — scores are meaningless until every document is re-indexed from scratch.

**Fix:** explicitly include BM25 scorer state in the serialised output. Add a round-trip test: save, load, verify BM25 scores are identical before and after.

---

### Critical: Chunk IDs Leaking Through the Public API

PDF and image indexing creates internal chunk IDs in the format `"docID||p3||c1||text||10.00,20.00,400.00,15.00"`. Without a library facade layer, these internal IDs surface to the caller who passed `"report.pdf"` and expected `"report.pdf"` back. They must parse `||`-separated implementation details to recover their original document ID.

**Fix:** the library layer strips chunk suffixes before returning results. The caller always gets their original document ID back. Page and position information is returned in a separate `Chunk` struct on the `Result` type for callers who need it (e.g. PDF highlighting).

---

### Input Validation: Empty and Whitespace-Only Values

```go
db.Add(ctx, "", "content")      // → ErrInvalidID: id must not be empty
db.Add(ctx, "id", "")           // → ErrEmptyDocument
db.Add(ctx, "id", "   \t\n ")   // → ErrEmptyDocument (whitespace-only = empty)
```

Silently accepting empty documents pollutes the index with zero-vector entries that can never be meaningfully retrieved. Named sentinel errors (`ErrInvalidID`, `ErrEmptyDocument`) let callers handle them with `errors.Is`.

---

### Input Validation: String Length Overflows

- **IDs** are stored in the `idMapping` — unbounded IDs are a memory attack surface. Maximum: 512 bytes. Beyond that: `ErrIDTooLong`.
- **Document text** has no length limit for lexical indexing. But the tokeniser truncates at 256 tokens (~1,000 characters) for semantic embedding. This is silent data loss for semantic search on long documents. The godoc must state explicitly: *"Semantic search covers only the first ~1,000 characters. Lexical search covers the full document."*
- **Queries** are similarly truncated at the tokeniser level.

---

### Input Validation: The NULL Value Misconception

Go has no null but it has nil, zero values, and empty strings. Every exported method behaves consistently:

- `Search` with no matches → `[]Result{}` (empty slice), **never** `nil`. Returning `nil` instead of `[]Result{}` breaks JSON serialisation and surprises callers expecting `len(results) == 0`.
- `AddBatch` with nil or empty map → no-op, no error.
- All slice return values: allocated and empty, not nil.

---

### Input Validation: Invalid UTF-8 and Null Bytes

- **IDs** must be valid UTF-8 with no null bytes or control characters (0x00–0x1F). These corrupt log output, break JSON serialisation of results, and cause subtle downstream bugs. Reject at input: `ErrInvalidID`.
- **Document text** with invalid UTF-8 or null bytes: sanitise silently via `strings.ToValidUTF8(text, "")` before indexing. Real-world documents (scraped HTML, OCR output, legacy encodings) frequently contain garbage bytes — rejecting them would break too many valid use cases.

---

### Input Validation: ID Characters Colliding with Internal Separators

The internal chunk ID format uses `||` as a separator. A user ID of `"my||doc"` silently corrupts the internal format. Fix: validate that IDs do not contain `||`. Return `ErrInvalidID` with a message explaining the restriction. Leaky abstractions must be exposed as explicit errors, not silent corruption.

---

### Input Validation: Option Values Out of Range

```go
zenith.WithLimit(-5)          // → ErrInvalidOption: limit must be positive
zenith.WithFuzzyDistance(-1)  // → ErrInvalidOption
zenith.WithFuzzyDistance(100) // → clamped to max (5) with a log warning — not an error
zenith.WithCacheSize(0)       // → valid, disables cache
```

Negative values are always errors. Absurdly large `FuzzyDistance` values are clamped — they would cause O(n) BK-tree scans that hang the caller.

---

### Output Sanitisation: Score Normalisation

Raw RRF scores are reciprocal sums — not bounded, not intuitive, not comparable across different result sets. Two guarantees for library consumers:

1. All scores are normalised to `[0.0, 1.0]` by dividing by the maximum score in the result set.
2. NaN and Inf scores (from zero-magnitude vectors in dot product calculations) are clamped to `0.0`. **Never return NaN or Inf to the caller.**

```go
type Result struct {
    ID    string
    Score float64 // always in [0.0, 1.0], never NaN, never Inf
}
```

---

### Output Sanitisation: Result Deduplication

The hybrid pipeline (lexical + fuzzy + vector) can produce the same document ID from multiple passes. RRF accumulates scores across passes by design — but a merge bug could produce duplicate IDs in the final slice. Before returning, deduplicate by ID. The caller should never see the same ID twice in one result set.

---

### Output Sanitisation: Deterministic Ordering for Equal Scores

When two results have identical normalised scores, the ordering must be deterministic across process restarts. Sort by ID lexicographically as a tiebreaker. Without this, any test that checks result ordering is flaky, and production result ordering changes unpredictably between deployments.

---

### Output Sanitisation: Error Message Cleansing

Internal errors contain file paths, goroutine IDs, and internal type names that are meaningless or alarming to end users:

```
// Raw (bad): "index: fst build: open /home/user/.zenith/data/terms.fst: permission denied"
// Wrapped (good): "zenith: failed to update search index (check write permissions)"
```

All errors returned through the public API are wrapped with `fmt.Errorf("zenith: %w", err)` — recognisably from ZENITH, still unwrappable via `errors.Is`/`errors.As`, and stripped of internal paths.

---

### Concurrency: Use-After-Close

Every method guards with an atomic `closed` flag:

```go
func (db *DB) Add(ctx context.Context, id, text string) error {
    if db.closed.Load() {
        return ErrClosed
    }
    // ...
}
```

Calling any method after `Close` returns `ErrClosed`. Never panics.

---

### Concurrency: Double-Close Safety

`Close` uses `sync.Once` — second and subsequent calls return `nil` immediately. `defer db.Close()` is always safe even if the caller also closes explicitly on an error path.

---

### Concurrency: Close Racing with Add/Search

`Close` atomically sets `closed = true` before acquiring the write lock. If `Add` is in progress: `Add` completes, `Close` then acquires the write lock, saves, tears down. No half-written documents. No torn reads. Subsequent `Add` calls return `ErrClosed`.

---

### Concurrency: Panic Recovery

If the internal engine panics (a bug — should never happen, but can), a `recover()` wrapper in each public method catches it, marks the DB as permanently closed, and returns a structured error. A panicking `DB` becomes `ErrClosed` rather than corrupting the caller's goroutine stack.

---

### Concurrency: ONNX Session Pool Exhaustion

The ONNX session pool holds up to `runtime.NumCPU()` concurrent sessions. If all sessions are busy, new goroutines block on a channel acquire. The internal context is passed to this wait — if the context is cancelled (including by `Close`), the waiting goroutine unblocks and returns `ErrClosed` rather than hanging forever.

---

### Concurrency: Nil-Safe Methods

```go
func (db *DB) Add(ctx context.Context, id, text string) error {
    if db == nil {
        return errors.New("zenith: Add called on nil DB")
    }
    // ...
}
```

Every exported method handles `db == nil` as a clean error. No nil pointer panics from caller mistakes.

---

### Concurrency: AddBatch Non-Determinism

`AddBatch` accepts `map[string]string`. Go map iteration is deliberately randomised — two identical calls produce documents indexed in different orders, causing different FST structures and different fuzzy match behaviour across runs. Fix: sort document IDs before processing. Deterministic input → deterministic index → reproducible tests.

---

### Operational: Goroutine Leak from Warmup Embed

The engine fires a detached goroutine on startup to warm the ONNX model. If `Close` is called before warmup finishes (common in tests), the goroutine outlives the `DB`, holds a reference to the ONNX session, and prevents GC. Over many test runs this accumulates into a memory leak. Fix: the warmup goroutine uses the DB's internal context — `Close` cancels it immediately.

---

### Operational: WAL Overhead for Library Use

The storage engine's WAL was designed for a long-running server that needs crash recovery. A library used in short-lived web request handlers pays WAL write overhead on every `Add` with no benefit — a request handler crashing mid-index just means that request fails, not that the whole index is corrupted. Fix: `WithNoWAL()` option. In-memory mode disables WAL automatically.

---

### Operational: Memory Scaling in `:memory:` Mode

```
1M documents × 384 dims × 2 bytes (float16) = 768 MB vectors alone
```

Plus postings lists, BK-tree nodes, ID mappings. No eviction — everything stays in RAM. GC pressure grows with index size, causing latency spikes in web handlers. Users who use `:memory:` in production without understanding this will hit OOM kills. Godoc must document the memory formula. A `WithMemoryLimit(bytes int64)` option returns `ErrIndexFull` when exceeded rather than silently growing until OOM.

---

### Operational: Two `:memory:` Opens Are Independent

```go
db1, _ := zenith.Open(":memory:")
db2, _ := zenith.Open(":memory:")
// db1 and db2 share NO state — completely independent databases
```

Users from Redis or SQLite shared-memory backgrounds expect `:memory:` to be a shared in-process singleton. It is not. Must be the first line of the `:memory:` godoc: *"Each call to Open(\":memory:\") creates a new independent database. To share an index between goroutines, pass the same \*DB instance."*

---

### Operational: File Locking (Two Processes, Same File)

Two processes calling `Open("search.db")` simultaneously would both read the file, both write back modified versions, and silently corrupt each other's work. Fix: acquire an exclusive advisory lock on `path + ".lock"` at `Open` time. Second caller gets `ErrLocked` immediately. Two `*DB` instances in the same process pointing at the same path are detected via an in-process `sync.Map` registry — also returns `ErrLocked`.

---

### Operational: Silent Quality Degradation — Vector/Inverted Desync

Embedding is non-fatal by design. If the ONNX model fails on a particular document, it is indexed lexically but not semantically. This document appears in keyword and fuzzy results but never in pure semantic search. Worse: if the embedder recovers later, the population permanently splits into vectored and vectorless documents with no way to detect the split. Fix: expose `SemanticScore float64` on `Result` (0.0 means no vector). Let callers detect and handle the split.

---

### Operational: Float16 Magnitude Cache Desync

Vectors are stored as float16, magnitudes cached separately as float32. When a document is re-indexed, if the magnitude cache is not updated atomically with the vector, cosine similarity scores for that document are computed against a stale magnitude — producing silent ranking inversions. Fix: update vector and magnitude atomically under the write lock, in the same struct.

---

### Operational: Synonym Expansion Cycle

If the synonym map contains a bidirectional entry (A→B, B→A — possible from a loaded thesaurus), expansion loops until stack overflow. Fix: expansion tracks visited terms in a `map[string]bool`. Maximum expansion depth capped at 10 terms.

---

## A Note on How This Was Built

ZENITH is a ground-up implementation of the primitives that make search work. Nothing is outsourced to an embedded key-value store or a vector database. Every component — the WAL, the skip-list, the SSTable compactor, the BK-tree, the FST dictionary, the RRF ranker, the ONNX tokenizer, the mean pooling step — was written specifically for this project.

That is not the pragmatic choice. It is the pedagogically honest one. The goal was to understand how search engines work, not to assemble one from parts. If you are reading this to understand the internals, every component has a clear boundary and a reason to exist at that boundary.

The consequence of building from scratch is that the internals are well-understood and improvable. The storage engine can be swapped. The ranker can be tuned. The embedder is an interface. The library API is stable because the internals are isolated behind it.

This is how you build something that lasts.
