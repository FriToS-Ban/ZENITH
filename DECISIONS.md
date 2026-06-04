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

### 9. Why "SQLite of search" is the right wedge

The competitors (Meilisearch, Typesense, Quickwit, Elasticsearch) are all **server processes** — you run them separately and talk to them over HTTP. That makes them:
- Ops overhead to deploy and maintain
- A network hop away from your code
- An external dependency your app cannot ship without

ZENITH is **a library** — it compiles into your binary and runs in your process. That is a completely different product category that none of the above compete in. You are not trying to be faster than Meilisearch. You are trying to be in the same relationship to search that SQLite is to databases: the thing you reach for when you want search *inside* your app, not *next to* your app.

This is why the target audience is web app developers and not "search engine users." Web app developers don't want to run Elasticsearch — they want to `import` something and have it work.
