# Part B: Phase 3 Completion & Enhancement — Linguistic Mastery (#19-24)

---

## Completed Components Design Review

### 19. Porter Stemming

**Implementation Quality: ✅ Good — faithful to Porter's original paper.**

Your `stemmer.go` (358 lines) implements all 5 steps correctly with proper `m()` measure counting, CVC detection, and double-consonant handling.

**Issues found:**

1. **Step1a ordering bug**: The `"s"` suffix rule is checked before `"ss"`, meaning `"ss"` endings get their final `s` stripped incorrectly. The canonical Porter order is: `sses→ss`, `ies→i`, `ss→ss`, `s→∅`. Your order:
```go
// Current (buggy order):
{"sses", "ss"}, {"s", ""}, {"ies", "i"}, {"ss", "ss"}
// Fix:
{"sses", "ss"}, {"ies", "i"}, {"ss", "ss"}, {"s", ""}
```

2. **Stemmer allocated per token**: `New()` is called inside `Tokenize()` on every call. The stemmer is stateless — allocate once:
```go
type StandardTokenizer struct {
    stopWords map[string]struct{}
    stemmer   *Stemmer            // Reuse across calls
    re        *regexp.Regexp      // Pre-compiled regex
}
```

3. **No irregular word handling**: Porter cannot handle irregulars like `"ran"→"run"`, `"better"→"good"`. Consider an exception dictionary for the top 200 irregular English words for search quality.

4. **Accuracy**: Porter stemming is ~95% accurate. Snowball (Porter2) improves this to ~97%. Consider upgrading when precision matters.

---

### 20. Edge N-Grams

**Implementation in `generateEdgeNgrams()`:**
```go
const (MinGram = 3; MaxGram = 10)
```

**Issues:**

1. **Duplicate full token**: If `n > MaxGram`, the full token is appended twice (line 490 and 496):
```go
results = append(results, token)      // Line 490: always added
// ...
if n > MaxGram {
    results = append(results, token)  // Line 496: added AGAIN
}
```

2. **Missing the full token in n-gram range**: For a 7-letter word, you generate `[full, 3-char, 4-char, 5-char, 6-char]` — but you skip the 7-char (full length) in the loop because `i < limit` instead of `i <= limit`. This means 7-letter words have no exact-length n-gram entry, only the full token.

3. **Memory optimization**: Pre-allocate with exact capacity:
```go
func generateEdgeNgrams(token string) []string {
    runes := []rune(token)
    n := len(runes)
    if n < MinGram {
        return []string{token}
    }
    limit := min(n, MaxGram)
    // Exact capacity: full token + ngrams from MinGram to limit
    results := make([]string, 0, limit-MinGram+2)
    results = append(results, token)
    for i := MinGram; i <= limit; i++ {
        frag := string(runes[:i])
        if frag != token { // Avoid duplicate of full token
            results = append(results, frag)
        }
    }
    return results
}
```

---

### 21. Phonetic Matching (Soundex)

**Implementation Quality: ✅ Correct Soundex implementation.**

**Limitations:**

1. **Soundex only — no Metaphone**: Soundex is designed for English surnames and produces poor results for general vocabulary. Example: `"smith"→S530`, `"schmidt"→S253` — these SHOULD match phonetically but don't.

2. **No Double Metaphone**: For a search engine, Double Metaphone is the industry standard:
```go
// Metaphone would give both "smith" and "schmidt" -> "XMT" or "SMT"
// Implementation recommendation:
func DoubleMetaphone(word string) (primary string, secondary string)
```

3. **Missing HW rule**: Standard Soundex treats H and W as separators between identical codes. Your implementation resets `lastCode` on vowels (`code='0'`) but doesn't follow the HW separator rule precisely.

4. **Phonetic scoring is flat**: All phonetic matches get `+50.0` regardless of match quality. Consider weighting by how many Soundex digits match:
```go
// Partial phonetic score based on matching prefix length
func phoneticScore(a, b string) float64 {
    matches := 0
    for i := 0; i < min(len(a), len(b)); i++ {
        if a[i] == b[i] { matches++ } else { break }
    }
    return float64(matches) / 4.0 * 50.0
}
```

---

### 22. Fuzzy Matching (Levenshtein Distance)

**Implementation Quality: ✅ Good — uses two-row DP with early termination.**

**Performance concern — O(V × L²) per query token:**
```go
// In Search(): iterates ALL vocabulary words of similar length
for size, list := range idx.vocabulary {
    if size >= minL && size <= maxL {
        for _, candidate := range list {        // O(V) candidates
            analysis.Levenshtein(token, candidate)  // O(L²) each
        }
    }
}
```

For a vocabulary of 100K unique words, this is **100K Levenshtein computations per query token**. At 5 tokens per query, that's 500K string comparisons.

**Optimizations:**

1. **BK-Tree**: Pre-build a BK-Tree over vocabulary for O(log V) fuzzy lookups instead of O(V):
```go
type BKNode struct {
    Word     string
    Children map[int]*BKNode  // distance -> child
}

func (t *BKNode) Search(query string, maxDist int) []string {
    dist, _ := Levenshtein(query, t.Word)
    var results []string
    if dist <= maxDist { results = append(results, t.Word) }
    for d := dist - maxDist; d <= dist+maxDist; d++ {
        if child, ok := t.Children[d]; ok {
            results = append(results, child.Search(query, maxDist)...)
        }
    }
    return results
}
```

2. **SymSpell**: Pre-compute all deletion variants within max edit distance. Lookup becomes O(1) hash lookup. Memory-intensive but extremely fast.

3. **Your early termination is good** but `MAX_DISTANCE=3` is never actually used in `Search()` — you hardcode `dist <= 2`. Align the constants.

---

### 23. Synonyms & Thesaurus

**Implementation: Neural synonym discovery via `GetSemanticNeighbors()`.**

This is creative — using word vector cosine similarity to find synonyms dynamically rather than a static thesaurus. However:

1. **O(V) scan for every synonym lookup**: You copy the entire `wordVectors` map and iterate all entries:
```go
wordAndVec := make(map[string][]float32, len(idx.wordVectors))
for k, v := range idx.wordVectors { wordAndVec[k] = v }  // Full copy!
```
For 50K vocabulary, this copies ~75MB of vectors per query.

2. **No static synonym support**: Common synonyms like `"car"↔"automobile"`, `"quick"↔"fast"` should be instant, not require neural computation.

3. **Recommendation**: Hybrid approach:
```go
type SynonymEngine struct {
    Static  map[string][]string             // Curated synonyms
    Dynamic func(word string) []string      // Neural fallback
}

func (se *SynonymEngine) Expand(word string) []string {
    if syns, ok := se.Static[word]; ok {
        return syns
    }
    return se.Dynamic(word)
}
```

---

## Pending: 24. Finite State Transducers (FST)

### Implementation Blueprint

An FST maps input sequences to output values with minimal memory — perfect for dictionary storage, prefix search, and suggestion systems.

### Data Structures
```go
// FST Node — cache-friendly layout (fits in 1-2 cache lines)
type FSTNode struct {
    Transitions [256]uint32  // byte -> node index (sparse: use map for >26 symbols)
    Output      uint64       // Associated value (e.g., postings list offset)
    IsFinal     bool
}

// For memory efficiency, use a compact representation:
type CompactFSTNode struct {
    // Packed: [numTransitions:8][isFinal:1][reserved:7]
    Header      uint16
    Transitions []FSTTransition  // Sorted by label for binary search
    Output      uint64
}

type FSTTransition struct {
    Label    byte
    TargetID uint32
    Output   uint64  // Output accumulated along this edge
}
```

### Construction Algorithm (Sorted Input Required)
```go
type FSTBuilder struct {
    nodes      []CompactFSTNode
    register   map[string]uint32  // Signature -> node ID (for minimization)
    tempNodes  []*BuildNode       // In-progress path nodes
}

// Steps:
// 1. Sort all dictionary entries lexicographically
// 2. For each entry, share common prefix with previous entry
// 3. Freeze completed nodes and minimize (share suffixes)
// 4. Output: minimal deterministic acyclic FST

func (b *FSTBuilder) Add(key []byte, value uint64) {
    // Find longest common prefix with previous key
    // Freeze nodes beyond the common prefix
    // Extend path for new suffix
    // Set output on final node
}
```

### Query Execution
```go
func (fst *FST) Get(key []byte) (uint64, bool) {
    nodeID := uint32(0) // Start at root
    var output uint64
    for _, b := range key {
        t, found := fst.nodes[nodeID].FindTransition(b)
        if !found { return 0, false }
        output += t.Output
        nodeID = t.TargetID
    }
    if fst.nodes[nodeID].IsFinal {
        return output + fst.nodes[nodeID].Output, true
    }
    return 0, false
}

// Prefix search — returns all keys with given prefix
func (fst *FST) PrefixSearch(prefix []byte) []FSTMatch {
    nodeID, output := fst.walkPrefix(prefix)
    if nodeID == InvalidNode { return nil }
    return fst.enumerate(nodeID, prefix, output)
}
```

### Integration with Phase 4
- **FST as SSTable index**: Replace sparse index with FST mapping `key prefix → block offset`
- **FST for term dictionary**: Replace `map[string][]uint32` with FST mapping `term → postings list file offset`
- **Memory savings**: A 1M-term dictionary uses ~50MB as a Go map vs ~5MB as an FST
- **Build FST during SSTable flush**: Sort memtable keys → build FST → write as SSTable index block
