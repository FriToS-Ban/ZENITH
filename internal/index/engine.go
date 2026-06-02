package index

import (
	"context"
	"encoding/gob"
	"fmt"
	"hash/fnv"
	"log/slog"
	"maps"
	"os"
	"time"

	"github.com/shramanb113/ZENITH/internal/analysis"
	"github.com/shramanb113/ZENITH/internal/config"
	"github.com/shramanb113/ZENITH/internal/embedding"
	"github.com/shramanb113/ZENITH/internal/ranking"
)

// SearchResponse holds a single search result.
type SearchResponse struct {
	ID    string
	Score float64
}

// BatchDoc is a single entry for AddBatch.
type BatchDoc struct {
	ID     string
	Text   string
	Vector []float32
}

// TermStore is implemented by storage backends that maintain a term vocabulary.
// storage.Engine satisfies this interface — wire it via Engine.SetTermStore()
// so the storage-layer FST stays in sync with the index vocabulary.
type TermStore interface {
	AddTerms([]string)
}

// Engine is the central orchestrator — it owns all sub-indexes and the
// scoring pipeline. All public methods are safe for concurrent use.
type Engine struct {
	config    *config.Config
	inverted  *InvertedIndex
	vectors   *VectorStore
	phonetics *PhoneticIndex
	bkTree    *analysis.BKTree

	embedder  embedding.Embedder
	scorer    ranking.Scorer
	analyzer  analysis.Analyzer

	// Supplementary scorers wired alongside the primary Scorer.
	// Both are updated on every Add/Remove so they stay in sync.
	bm25  *ranking.BM25Scorer
	tfidf *ranking.TFIDFScorer

	idMapping map[uint32]string

	// FST term dictionary — rebuilt from globalSeen after every Add() that
	// introduces new terms. Wired to the analyzer via the FSTWirer interface
	// so that resolveTerm() uses the current indexed vocabulary.
	fst     *analysis.FSTDictionary
	fstSize int    // len(globalSeen) at the time of the last successful Build()
	fstPath string // on-disk path; empty = in-memory only (tests)

	// Optional storage-layer term sink. When set, RebuildFST() forwards all
	// vocabulary terms to termStore.AddTerms() so the storage FST stays in sync.
	termStore TermStore
}

// NewEngine constructs a fully initialised Engine.
// The scorer parameter is the primary fusion scorer (typically RRFRanker).
func NewEngine(cfg *config.Config, emb embedding.Embedder, scr ranking.Scorer, ana analysis.Analyzer) *Engine {
	return &Engine{
		config:    cfg,
		inverted:  NewInvertedIndex(),
		vectors:   NewVectorStore(),
		phonetics: NewPhoneticIndex(),
		bkTree:    analysis.NewBKTree(),
		embedder:  emb,
		scorer:    scr,
		analyzer:  ana,
		idMapping: make(map[uint32]string),
		bm25:      ranking.NewBM25Scorer(ranking.BM25Params{}),
		tfidf:     ranking.NewTFIDFScorer(),
		fst:       analysis.NewFSTDictionary(),
	}
}

// SetTermStore wires an optional storage backend so that RebuildFST() also
// forwards the full vocabulary to termStore.AddTerms(). Call before the first
// Add() to ensure the storage FST is populated from the first document onward.
func (e *Engine) SetTermStore(s TermStore) { e.termStore = s }

// SetFSTPath sets the on-disk path for the FST file.
// When set, RebuildFST writes the FST to disk (atomic rename) and reopens it
// via memory mapping — keeping RAM usage near zero regardless of vocabulary
// size. Load() will also load the FST from disk instead of rebuilding.
// Call before the first Add() or Load().
func (e *Engine) SetFSTPath(path string) { e.fstPath = path }

// RebuildFST rebuilds the FST from the current global vocabulary snapshot.
//
// If a fstPath is set (via SetFSTPath), the FST is written atomically to disk
// and reopened via memory mapping — only the accessed pages stay in RAM.
// If no path is set, the FST is kept in-memory (useful for tests).
//
// After building, the FST is wired into the analyzer (if it implements
// FSTWirer) and all terms are forwarded to the optional TermStore.
// Safe to call from any goroutine — takes a brief RLock on the inverted index.
func (e *Engine) RebuildFST() error {
	e.inverted.RLock()
	glob := e.inverted.GetGlobalSeen()
	terms := make([]string, 0, len(glob))
	for t := range glob {
		terms = append(terms, t)
	}
	e.fstSize = len(glob)
	e.inverted.RUnlock()

	var buildErr error
	if e.fstPath != "" {
		buildErr = e.fst.BuildToFile(terms, e.fstPath)
	} else {
		buildErr = e.fst.Build(terms)
	}
	if buildErr != nil {
		return fmt.Errorf("index: fst build: %w", buildErr)
	}

	// Wire the rebuilt FST into the analyzer so resolveTerm() is live.
	if w, ok := e.analyzer.(analysis.FSTWirer); ok {
		w.SetFST(e.fst)
	}

	// Propagate vocabulary to the storage layer (optional).
	if e.termStore != nil {
		e.termStore.AddTerms(terms)
	}

	slog.Info("index: FST rebuilt", "terms", len(terms))
	return nil
}

// rebuildFSTIfNeeded calls RebuildFST only when globalSeen has grown since the
// last build. Called after every successful Add() to keep the FST current with
// minimal overhead.
func (e *Engine) rebuildFSTIfNeeded() {
	e.inverted.RLock()
	currentSize := len(e.inverted.GetGlobalSeen())
	e.inverted.RUnlock()
	if currentSize <= e.fstSize {
		return
	}
	if err := e.RebuildFST(); err != nil {
		slog.Warn("index: FST rebuild failed", "error", err)
	}
}

// FSTContains returns true if term is in the current FST vocabulary.
func (e *Engine) FSTContains(term string) bool { return e.fst.Contains(term) }

// FSTPrefixSearch returns up to maxResults indexed terms that begin with prefix.
func (e *Engine) FSTPrefixSearch(prefix string, maxResults int) ([]string, error) {
	return e.fst.PrefixSearch(prefix, maxResults)
}

// Add indexes a document. Safe to call with the same originalID to re-index
// (idempotent — old entries are removed cleanly before new ones are written).
// After each successful index, the FST is rebuilt if new terms were added,
// keeping query-time prefix resolution and the optional TermStore current.
func (e *Engine) Add(ctx context.Context, originalID string, fullText string) error {
	if err := e.addInternal(ctx, originalID, fullText, nil); err != nil {
		return err
	}
	e.rebuildFSTIfNeeded()
	return nil
}

// AddWithVector indexes a document using a pre-computed document embedding,
// skipping the embedder.Embed call. Word-level embeddings are still computed.
// Use when the caller already has a vector (e.g. from the PDF sidecar).
func (e *Engine) AddWithVector(ctx context.Context, originalID string, fullText string, docVec []float32) error {
	if err := e.addInternal(ctx, originalID, fullText, docVec); err != nil {
		return err
	}
	e.rebuildFSTIfNeeded()
	return nil
}

// AddBatch indexes all documents in docs and rebuilds the FST exactly once at
// the end. This is significantly faster than N individual AddWithVector calls
// because FST rebuild is O(vocab × log vocab) and involves a disk write — doing
// it once instead of once-per-chunk eliminates the dominant cost during bulk
// PDF ingestion.
func (e *Engine) AddBatch(ctx context.Context, docs []BatchDoc) error {
	for _, d := range docs {
		if err := e.addInternal(ctx, d.ID, d.Text, d.Vector); err != nil {
			return err
		}
	}
	return e.RebuildFST()
}

// Remove deletes all index entries for originalID.
// Returns nil if the ID was never indexed (idempotent).
func (e *Engine) Remove(_ context.Context, originalID string) error {
	h := fnv.New32a()
	h.Write([]byte(originalID))
	internalID := uint32(h.Sum32())

	e.inverted.Lock()
	e.vectors.Lock()
	e.phonetics.Lock()
	defer e.inverted.Unlock()
	defer e.vectors.Unlock()
	defer e.phonetics.Unlock()

	idxData := e.inverted.GetData()
	idxPhon := e.phonetics.GetData()
	idxFrags := e.inverted.GetDocFragments()
	docVecStore := e.vectors.GetVectors()

	oldFrags, exists := idxFrags[internalID]
	if !exists {
		return nil
	}

	for _, frag := range oldFrags {
		if idList, ok := idxData[frag]; ok {
			idxData[frag] = removeID(idList, internalID)
		}
		if idList, ok := idxPhon[frag]; ok {
			idxPhon[frag] = removeID(idList, internalID)
		}
	}
	delete(idxFrags, internalID)
	delete(docVecStore, internalID)
	delete(e.idMapping, internalID)

	e.bm25.Remove(internalID)
	e.tfidf.Remove(internalID)

	return nil
}

func (e *Engine) addInternal(ctx context.Context, originalID string, fullText string, preVec []float32) error {
	logger := slog.With("doc_id", originalID)

	tokens := e.analyzer.Analyze(fullText)
	rawTokens := make([]string, 0, len(tokens))
	for _, t := range tokens {
		rawTokens = append(rawTokens, t.Term)
	}

	// Embeddings — failures are non-fatal; falls back to lexical-only.
	var docVec []float32
	if preVec != nil {
		docVec = preVec
	} else {
		var err error
		docVec, err = e.embedder.Embed(ctx, fullText)
		if err != nil {
			logger.Warn("Embedding failed, indexing purely lexically", "error", err)
		}
	}

	tempWordVectors := make(map[string]VectorEntry)
	var tokensToEmbed []string
	for _, t := range rawTokens {
		if _, exists := tempWordVectors[t]; !exists && !e.vectors.HasWordVector(t) {
			tokensToEmbed = append(tokensToEmbed, t)
			tempWordVectors[t] = VectorEntry{} // reserve
		}
	}

	const embedBatchSize = 512
	for i := 0; i < len(tokensToEmbed); i += embedBatchSize {
		end := i + embedBatchSize
		if end > len(tokensToEmbed) {
			end = len(tokensToEmbed)
		}
		chunk := tokensToEmbed[i:end]
		batchVecs, err := e.embedder.EmbedBatch(ctx, chunk)
		if err == nil && len(batchVecs) == len(chunk) {
			for j, t := range chunk {
				tempWordVectors[t] = VectorEntry{
					Vector:    FloatsToFloat16(batchVecs[j]),
					Magnitude: ranking.Magnitude(batchVecs[j]),
				}
			}
		} else {
			logger.Warn("Batch embedding failed for tokens", "error", err)
			for _, t := range chunk {
				delete(tempWordVectors, t)
			}
		}
	}

	h := fnv.New32a()
	h.Write([]byte(originalID))
	internalID := uint32(h.Sum32())

	// --- Write locks ---
	e.inverted.Lock()
	e.vectors.Lock()
	e.phonetics.Lock()
	defer e.inverted.Unlock()
	defer e.vectors.Unlock()
	defer e.phonetics.Unlock()

	idxData := e.inverted.GetData()
	idxPhon := e.phonetics.GetData()
	idxFrags := e.inverted.GetDocFragments()
	wordVecs := e.vectors.GetWordVectors()
	docVecStore := e.vectors.GetVectors()

	// Idempotency: remove previous postings for this document.
	if oldFrags, exists := idxFrags[internalID]; exists {
		for _, frag := range oldFrags {
			if idList, ok := idxData[frag]; ok {
				idxData[frag] = removeID(idList, internalID)
			}
			if idList, ok := idxPhon[frag]; ok {
				idxPhon[frag] = removeID(idList, internalID)
			}
		}
		// Keep supplementary scorers in sync.
		e.bm25.Remove(internalID)
		e.tfidf.Remove(internalID)
	}

	e.idMapping[internalID] = originalID

	if docVec != nil {
		docVecStore[internalID] = VectorEntry{
			Vector:    FloatsToFloat16(docVec),
			Magnitude: ranking.Magnitude(docVec),
		}
	}
	maps.Copy(wordVecs, tempWordVectors)

	seenInDoc := make(map[string]bool)
	var docFrags []string

	tokCnt := e.inverted.GetTokenCounts()
	vocab := e.inverted.GetVocabulary()
	glob := e.inverted.GetGlobalSeen()

	for _, token := range rawTokens {
		tokCnt[token]++

		for _, frag := range generateEdgeNgrams(token) {
			if seenInDoc[frag] {
				continue
			}
			seenInDoc[frag] = true
			idxData[frag] = append(idxData[frag], internalID)
			docFrags = append(docFrags, frag)
		}

		if phon := analysis.Soundex(token); phon != "" && !seenInDoc[phon] {
			idxPhon[phon] = append(idxPhon[phon], internalID)
			seenInDoc[phon] = true
			docFrags = append(docFrags, phon)
		}

		if !glob[token] {
			vocab[len(token)] = append(vocab[len(token)], token)
			glob[token] = true
			e.bkTree.Add(token)
		}
	}

	idxFrags[internalID] = docFrags

	// Wire BM25 and TF-IDF — both index the same rawTokens.
	e.bm25.Index(internalID, rawTokens)
	e.tfidf.Index(internalID, rawTokens)

	return nil
}

// Search executes a hybrid query: lexical (n-gram + phonetic + fuzzy) +
// semantic (vector) + neural expansion on weak results.
// Returns results ranked by the configured Scorer (default: RRF).
//
// Query analysis path:
//   - If the analyzer implements QueryAnalyzer, AnalyzeQuery() is used so that
//     synonym expansion and FST prefix resolution both fire.
//   - Otherwise falls back to the base Analyze() method.
func (e *Engine) Search(ctx context.Context, query string) ([]SearchResponse, error) {
	var tokens []analysis.Token
	if qa, ok := e.analyzer.(analysis.QueryAnalyzer); ok {
		tokens = qa.AnalyzeQuery(query)
	} else {
		tokens = e.analyzer.Analyze(query)
	}
	rawTokens := make([]string, 0, len(tokens))
	for _, t := range tokens {
		rawTokens = append(rawTokens, t.Term)
	}

	// RLock #1: lexical pass
	e.inverted.RLock()
	e.phonetics.RLock()
	keywordScores, matchTokens := e.lexicalPass(rawTokens)
	e.phonetics.RUnlock()
	e.inverted.RUnlock()

	// Embedding call outside all locks
	queryVec, err := e.embedder.Embed(ctx, query)
	if err != nil {
		slog.Warn("Search vectors degraded — Nerve unreachable", "error", err)
	}

	// RLock #2: vector pass
	e.vectors.RLock()
	vectorScores := e.vectorPass(queryVec)
	e.vectors.RUnlock()

	ranks := e.rankAndFuse(keywordScores, matchTokens, rawTokens, vectorScores)

	// Neural expansion: fire when results are absent or weak.
	if len(ranks) == 0 || ranks[0].Score < 5.0 {
		expandedTokens := e.expandTokens(rawTokens)

		e.inverted.RLock()
		expandedKeywords, expandedMatches := e.neuralExpand(rawTokens, expandedTokens)
		e.inverted.RUnlock()

		// Merge original keyword scores into expanded map.
		for id, score := range keywordScores {
			expandedKeywords[id] += score
			if expandedMatches[id] == nil {
				expandedMatches[id] = make(map[string]bool)
			}
			for mt := range matchTokens[id] {
				expandedMatches[id][mt] = true
			}
		}

		ranks = e.rankAndFuse(expandedKeywords, expandedMatches, rawTokens, vectorScores)
	}

	return ranks, nil
}

// expandTokens finds semantic neighbours for each query token.
// Extracted from Search to remove the *analysis.StandardAnalyzer type assertion —
// the Analyzer interface's Analyze method is used instead.
func (e *Engine) expandTokens(rawTokens []string) []string {
	var expanded []string
	for _, token := range rawTokens {
		if len(token) < 3 {
			continue
		}
		neighbors := e.getSemanticNeighbors(token, 5, 0.70)
		for _, n := range neighbors {
			// Use Analyze (interface method) instead of a type assertion to Tokenize.
			neighborTokens := e.analyzer.Analyze(n)
			if len(neighborTokens) > 0 {
				expanded = append(expanded, neighborTokens[0].Term)
			}
		}
	}
	return expanded
}

func (e *Engine) lexicalPass(queryTokens []string) (map[uint32]float64, map[uint32]map[string]bool) {
	keywordScores := make(map[uint32]float64)
	matchTokens := make(map[uint32]map[string]bool)

	idxData := e.inverted.GetData()
	idxPhon := e.phonetics.GetData()

	for _, token := range queryTokens {
		Q := len(token)

		// 1. Edge N-grams
		var frags []string
		if Q >= 3 {
			frags = generateEdgeNgrams(token)
		} else {
			frags = []string{token}
		}

		for _, frag := range frags {
			if ids, ok := idxData[frag]; ok {
				for _, id := range ids {
					keywordScores[id] += (float64(len(frag)) / float64(Q)) * 100.0
					if matchTokens[id] == nil {
						matchTokens[id] = make(map[string]bool)
					}
					matchTokens[id][token] = true
				}
			}
		}

		// 2. Phonetic
		if phon := analysis.Soundex(token); phon != "" {
			if ids, ok := idxPhon[phon]; ok {
				for _, id := range ids {
					keywordScores[id] += e.config.PhoneticWeight
					if matchTokens[id] == nil {
						matchTokens[id] = make(map[string]bool)
					}
					matchTokens[id][token] = true
				}
			}
		}

		// 3. Fuzzy via BK-Tree (only for tokens long enough to have typos)
		if Q >= 2 {
			for _, match := range e.bkTree.Search(token, e.config.FuzzyMaxDist) {
				if match.Distance == 0 {
					continue // exact match already handled above
				}
				if ids, ok := idxData[match.Word]; ok {
					for _, id := range ids {
						keywordScores[id] += 60.0 / float64(match.Distance)
						if matchTokens[id] == nil {
							matchTokens[id] = make(map[string]bool)
						}
						matchTokens[id][token] = true
					}
				}
			}
		}
	}
	return keywordScores, matchTokens
}

func (e *Engine) vectorPass(queryVec []float32) map[uint32]float64 {
	scores := make(map[uint32]float64)
	if len(queryVec) == 0 {
		return scores
	}
	for id, entry := range e.vectors.GetVectors() {
		scores[id] = ranking.DotProduct(queryVec, Float16ToFloats(entry.Vector))
	}
	return scores
}

func (e *Engine) neuralExpand(originalTokens []string, expandedTokens []string) (map[uint32]float64, map[uint32]map[string]bool) {
	keywordScores := make(map[uint32]float64)
	matchTokens := make(map[uint32]map[string]bool)
	idxData := e.inverted.GetData()

	for _, neighbor := range expandedTokens {
		targets := make(map[uint32]bool)
		if ids, ok := idxData[neighbor]; ok {
			for _, id := range ids {
				targets[id] = true
			}
		}
		if len(neighbor) > 3 {
			if ids, ok := idxData[neighbor[:3]]; ok {
				for _, id := range ids {
					targets[id] = true
				}
			}
		}
		for id := range targets {
			keywordScores[id] += 20000.0
			if matchTokens[id] == nil {
				matchTokens[id] = make(map[string]bool)
			}
			if len(originalTokens) > 0 {
				matchTokens[id][originalTokens[0]] = true
			}
		}
	}
	return keywordScores, matchTokens
}

// rankAndFuse builds keyword and vector ID lists and calls the configured Scorer.
// FIX: no longer mutates the incoming kwScores map — builds a boosted copy instead.
func (e *Engine) rankAndFuse(
	kwScores map[uint32]float64,
	matchToks map[uint32]map[string]bool,
	qryToks []string,
	vScores map[uint32]float64,
) []SearchResponse {

	// Build boosted copy — never mutate the caller's map.
	boosted := make(map[uint32]float64, len(kwScores))
	for id, score := range kwScores {
		if score <= 0 {
			continue
		}
		boosted[id] = score + 10000.0
		if len(matchToks[id]) >= len(qryToks) {
			boosted[id] += 50000.0
		}
	}

	kwIDs := make([]uint32, 0, len(boosted))
	for id := range boosted {
		kwIDs = append(kwIDs, id)
	}
	vcIDs := make([]uint32, 0, len(vScores))
	for id := range vScores {
		vcIDs = append(vcIDs, id)
	}

	scored := e.scorer.Score(kwIDs, boosted, vcIDs, vScores, e.idMapping)

	// BM25 tiebreak pass.
	// Get BM25 scores for query tokens once — used to break RRF ties.
	bm25Results := e.bm25.Query(qryToks)
	bm25Map := make(map[string]float64, len(bm25Results))
	for _, r := range bm25Results {
		bm25Map[e.idMapping[r.DocID]] = r.Score
	}

	// Re-sort: primary = RRF score desc, secondary = BM25 score desc.
	// Only fires when RRF scores are within epsilon — otherwise RRF order
	// is preserved exactly.
	const epsilon = 1e-6
	for i := 1; i < len(scored); i++ {
		for j := i; j > 0; j-- {
			a, b := scored[j-1], scored[j]
			rrfDiff := a.Score - b.Score
			if rrfDiff >= epsilon {
				break // a is clearly ahead, stop
			}
			if rrfDiff > -epsilon {
				// Scores are within epsilon — apply BM25 tiebreak.
				if bm25Map[b.ID] > bm25Map[a.ID] {
					scored[j-1], scored[j] = scored[j], scored[j-1]
				}
			}
		}
	}

	results := make([]SearchResponse, len(scored))
	for i, r := range scored {
		results[i] = SearchResponse{ID: r.ID, Score: r.Score}
	}
	return results
}

func (e *Engine) getSemanticNeighbors(token string, topN int, threshold float32) []string {
	e.vectors.RLock()
	defer e.vectors.RUnlock()

	wordVecs := e.vectors.GetWordVectors()
	tokenEntry, ok := wordVecs[token]
	if !ok {
		return nil
	}

	var candidates []string
	tokenVec := Float16ToFloats(tokenEntry.Vector)
	for word, entry := range wordVecs {
		if word == token {
			continue
		}
		if float32(ranking.DotProduct(tokenVec, Float16ToFloats(entry.Vector))) >= threshold {
			candidates = append(candidates, word)
		}
	}
	if topN > 0 && len(candidates) > topN {
		candidates = candidates[:topN]
	}
	return candidates
}

// generateEdgeNgrams produces prefix n-grams for a token.
// FIX: the original appended the full token twice when n > MaxGram.
// Now the full token is always the first element, and prefixes fill the rest.
func generateEdgeNgrams(token string) []string {
	const (
		MinGram = 3
		MaxGram = 10
	)
	runes := []rune(token)
	n := len(runes)

	if n < MinGram {
		return []string{token}
	}

	// Cap prefix generation at MaxGram — but always include the full token.
	limit := n
	if limit > MaxGram {
		limit = MaxGram
	}

	// Capacity: full token + prefixes from MinGram up to (but not including) limit.
	cap := 1 + (limit - MinGram)
	results := make([]string, 0, cap)
	results = append(results, token) // full token always first

	for i := MinGram; i < limit; i++ {
		results = append(results, string(runes[0:i]))
	}

	return results
}

// removeID returns ids with the target removed. Avoids allocation if not found.
func removeID(ids []uint32, target uint32) []uint32 {
	out := ids[:0]
	for _, id := range ids {
		if id != target {
			out = append(out, id)
		}
	}
	return out
}

// Save serialises all index state to filepath using gob.
func (e *Engine) Save(filepath string) error {
	start := time.Now()
	e.inverted.RLock()
	e.vectors.RLock()
	e.phonetics.RLock()
	defer e.inverted.RUnlock()
	defer e.vectors.RUnlock()
	defer e.phonetics.RUnlock()

	slog.Info("Saving index state", "path", filepath)

	file, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer file.Close()

	enc := gob.NewEncoder(file)
	state := []any{
		e.inverted.GetData(), e.idMapping, e.vectors.GetVectors(),
		e.inverted.GetTokenCounts(), e.phonetics.GetData(), e.inverted.GetVocabulary(),
		e.inverted.GetGlobalSeen(), e.vectors.GetWordVectors(), e.inverted.GetDocFragments(),
	}
	for _, s := range state {
		if err := enc.Encode(s); err != nil {
			return err
		}
	}

	slog.Info("Index saved", "entries", len(e.inverted.GetData()), "duration", time.Since(start))
	return nil
}

// Load restores index state from a gob file written by Save, then makes the
// FST live for query-time term resolution.
//
// Fast path (fstPath set + file exists): opens the pre-built on-disk FST via
// memory mapping. No rebuild needed — startup is O(1) regardless of vocab size.
//
// Slow path (no path, or file missing): rebuilds the FST from the restored
// globalSeen and, if a path is set, writes it to disk for next startup.
func (e *Engine) Load(filepath string) error {
	if err := e.load(filepath); err != nil {
		return err
	}

	// Fast path: the FST file was written by the last RebuildFST call and
	// represents the same vocabulary stored in the gob file.
	if e.fstPath != "" {
		if err := e.fst.OpenFromFile(e.fstPath); err == nil {
			if w, ok := e.analyzer.(analysis.FSTWirer); ok {
				w.SetFST(e.fst)
			}
			slog.Info("index: FST loaded from disk", "path", e.fstPath, "terms", e.fst.Size())
			return nil
		}
		slog.Info("index: FST file not found, rebuilding", "path", e.fstPath)
	}

	// Slow path: rebuild from globalSeen (also saves to disk if path is set).
	if err := e.RebuildFST(); err != nil {
		slog.Warn("index: FST rebuild after load failed", "error", err)
	}
	return nil
}

func (e *Engine) load(filepath string) error {
	start := time.Now()
	e.inverted.Lock()
	e.vectors.Lock()
	e.phonetics.Lock()
	defer e.inverted.Unlock()
	defer e.vectors.Unlock()
	defer e.phonetics.Unlock()

	f, err := os.Open(filepath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Warn("No persistence file found, starting fresh", "path", filepath)
			return err
		}
		return err
	}
	defer f.Close()

	dec := gob.NewDecoder(f)

	vData := e.inverted.GetData()
	vVectors := e.vectors.GetVectors()
	vToken := e.inverted.GetTokenCounts()
	vPhon := e.phonetics.GetData()
	vVocab := e.inverted.GetVocabulary()
	vSeen := e.inverted.GetGlobalSeen()
	vWordVectors := e.vectors.GetWordVectors()
	vFrag := e.inverted.GetDocFragments()

	state := []any{
		&vData, &e.idMapping, &vVectors,
		&vToken, &vPhon, &vVocab,
		&vSeen, &vWordVectors, &vFrag,
	}
	for _, s := range state {
		if err := dec.Decode(s); err != nil {
			return err
		}
	}

	slog.Info("Index loaded", "docs", len(e.idMapping), "duration", time.Since(start))
	return nil
}
