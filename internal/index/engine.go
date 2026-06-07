package index

import (
	"context"
	"encoding/gob"
	"fmt"
	"hash/fnv"
	"log/slog"
	"maps"
	"os"
	"sort"
	"sync"
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
type TermStore interface {
	AddTerms([]string)
}

// DocumentJournal durably records document mutations before they touch the
// in-memory index. Satisfied by *storage.Engine — its Put/Delete signatures
// match exactly. Set via SetDocumentJournal; nil means no journaling.
type DocumentJournal interface {
	Put(ctx context.Context, key, value []byte) error
	Delete(ctx context.Context, key []byte) error
}

// Engine is the central orchestrator — it owns all sub-indexes and the
// scoring pipeline.
//
// Concurrency model: Engine.mu is the top-level gate.
//   - Add, AddWithVector, AddBatch, Remove, Load, RebuildFST: take mu.Lock()
//   - Search, Save: take mu.RLock()
//
// Sub-index locks (inverted.mu, vectors.mu, phonetics.mu, bm25.mu) are kept
// as defence-in-depth but are no longer the primary concurrency boundary.
// Lock ordering is always: Engine.mu → sub-index lock. Never reversed.
type Engine struct {
	mu sync.RWMutex // primary concurrency gate — see comment above

	config    *config.Config
	inverted  *InvertedIndex
	vectors   *VectorStore
	phonetics *PhoneticIndex
	bkTree    *analysis.BKTree

	embedder embedding.Embedder
	scorer   ranking.Scorer
	analyzer analysis.Analyzer

	bm25  *ranking.BM25Scorer
	tfidf *ranking.TFIDFScorer

	idMapping map[uint64]string

	fst     *analysis.FSTDictionary
	fstSize int
	fstPath string

	termStore TermStore
	journal   DocumentJournal
}

// NewEngine constructs a fully initialised Engine.
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
		idMapping: make(map[uint64]string),
		bm25:      ranking.NewBM25Scorer(ranking.BM25Params{}),
		tfidf:     ranking.NewTFIDFScorer(),
		fst:       analysis.NewFSTDictionary(),
	}
}

func (e *Engine) SetTermStore(s TermStore)         { e.termStore = s }
func (e *Engine) SetFSTPath(path string)           { e.fstPath = path }
func (e *Engine) SetDocumentJournal(j DocumentJournal) { e.journal = j }

// RebuildFST rebuilds the FST from the current global vocabulary.
// Takes Engine.mu.Lock() — safe to call from outside the engine.
func (e *Engine) RebuildFST() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rebuildFSTLocked()
}

// rebuildFSTLocked is the internal FST rebuild — no lock taken.
// MUST be called while Engine.mu.Lock() is held.
func (e *Engine) rebuildFSTLocked() error {
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

	if w, ok := e.analyzer.(analysis.FSTWirer); ok {
		w.SetFST(e.fst)
	}
	if e.termStore != nil {
		e.termStore.AddTerms(terms)
	}

	slog.Info("index: FST rebuilt", "terms", len(terms))
	return nil
}

// rebuildFSTIfNeeded rebuilds only when globalSeen has grown.
// MUST be called while Engine.mu.Lock() is held.
func (e *Engine) rebuildFSTIfNeeded() {
	e.inverted.RLock()
	currentSize := len(e.inverted.GetGlobalSeen())
	e.inverted.RUnlock()
	if currentSize <= e.fstSize {
		return
	}
	if err := e.rebuildFSTLocked(); err != nil {
		slog.Warn("index: FST rebuild failed", "error", err)
	}
}

func (e *Engine) FSTContains(term string) bool { return e.fst.Contains(term) }
func (e *Engine) FSTPrefixSearch(prefix string, maxResults int) ([]string, error) {
	return e.fst.PrefixSearch(prefix, maxResults)
}

// Add indexes a document. Takes Engine.mu.Lock() for its full duration.
func (e *Engine) Add(ctx context.Context, originalID string, fullText string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.addInternal(ctx, originalID, fullText, nil); err != nil {
		return err
	}
	e.rebuildFSTIfNeeded()
	return nil
}

// AddWithVector indexes a document with a pre-computed embedding.
func (e *Engine) AddWithVector(ctx context.Context, originalID string, fullText string, docVec []float32) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.addInternal(ctx, originalID, fullText, docVec); err != nil {
		return err
	}
	e.rebuildFSTIfNeeded()
	return nil
}

// AddBatch indexes all documents and rebuilds the FST once at the end.
// Takes Engine.mu.Lock() for its full duration.
func (e *Engine) AddBatch(ctx context.Context, docs []BatchDoc) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	sorted := make([]BatchDoc, len(docs))
	copy(sorted, docs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	docs = sorted

	tokenSet := make(map[string]struct{})
	for _, d := range docs {
		for _, t := range e.analyzer.Analyze(d.Text) {
			if !e.vectors.HasWordVector(t.Term) {
				tokenSet[t.Term] = struct{}{}
			}
		}
	}
	if len(tokenSet) > 0 {
		tokens := make([]string, 0, len(tokenSet))
		for t := range tokenSet {
			tokens = append(tokens, t)
		}
		const warmBatch = 512
		for i := 0; i < len(tokens); i += warmBatch {
			end := i + warmBatch
			if end > len(tokens) {
				end = len(tokens)
			}
			if _, err := e.embedder.EmbedBatch(ctx, tokens[i:end]); err != nil {
				slog.Warn("index: word-vector pre-warm failed", "error", err)
			}
		}
	}

	for _, d := range docs {
		if err := e.addInternal(ctx, d.ID, d.Text, d.Vector); err != nil {
			return err
		}
	}
	return e.rebuildFSTLocked()
}

// Remove deletes all index entries for originalID.
// Takes Engine.mu.Lock() for its full duration.
func (e *Engine) Remove(ctx context.Context, originalID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.journal != nil {
		if err := e.journal.Delete(ctx, []byte(originalID)); err != nil {
			return fmt.Errorf("index: journal delete: %w", err)
		}
	}

	h := fnv.New64a()
	h.Write([]byte(originalID))
	internalID := h.Sum64()

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
	glob := e.inverted.GetGlobalSeen()
	docToks := e.inverted.GetDocTokens()

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

	// Decrement globalSeen reference counts for this document's raw tokens.
	if rawToks, ok := docToks[internalID]; ok {
		for _, tok := range rawToks {
			if n := glob[tok]; n <= 1 {
				delete(glob, tok)
			} else {
				glob[tok] = n - 1
			}
		}
		delete(docToks, internalID)
	}

	e.bm25.Remove(internalID)
	e.tfidf.Remove(internalID)

	return nil
}

func (e *Engine) addInternal(ctx context.Context, originalID string, fullText string, preVec []float32) error {
	if e.journal != nil {
		if err := e.journal.Put(ctx, []byte(originalID), []byte(fullText)); err != nil {
			return fmt.Errorf("index: journal write: %w", err)
		}
	}

	logger := slog.With("doc_id", originalID)

	tokens := e.analyzer.Analyze(fullText)
	rawTokens := make([]string, 0, len(tokens))
	for _, t := range tokens {
		rawTokens = append(rawTokens, t.Term)
	}

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
			tempWordVectors[t] = VectorEntry{}
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

	h := fnv.New64a()
	h.Write([]byte(originalID))
	internalID := h.Sum64()

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
	glob := e.inverted.GetGlobalSeen()
	docToks := e.inverted.GetDocTokens()

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
		// Decrement globalSeen for the old tokens before overwriting.
		if oldToks, ok := docToks[internalID]; ok {
			for _, tok := range oldToks {
				if n := glob[tok]; n <= 1 {
					delete(glob, tok)
				} else {
					glob[tok] = n - 1
				}
			}
		}
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

		// Increment globalSeen reference count; add to BKTree on first occurrence.
		if glob[token] == 0 {
			vocab[len(token)] = append(vocab[len(token)], token)
			e.bkTree.Add(token)
		}
		glob[token]++
	}

	idxFrags[internalID] = docFrags
	docToks[internalID] = append([]string(nil), rawTokens...) // snapshot

	e.bm25.Index(internalID, rawTokens)
	e.tfidf.Index(internalID, rawTokens)

	return nil
}

// Search executes a hybrid query. Takes Engine.mu.RLock() for its full duration.
func (e *Engine) Search(ctx context.Context, query string) ([]SearchResponse, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

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

	e.inverted.RLock()
	e.phonetics.RLock()
	keywordScores, matchTokens := e.lexicalPass(rawTokens)
	e.phonetics.RUnlock()
	e.inverted.RUnlock()

	// Embedding call — Engine.mu.RLock is still held. This serialises Add+Search
	// but allows concurrent Search+Search (multiple RLocks are compatible).
	queryVec, err := e.embedder.Embed(ctx, query)
	if err != nil {
		slog.Warn("Search vectors degraded — embedder unreachable", "error", err)
	}

	e.vectors.RLock()
	vectorScores := e.vectorPass(queryVec)
	e.vectors.RUnlock()

	ranks := e.rankAndFuse(keywordScores, matchTokens, rawTokens, vectorScores)

	// Neural expansion fires only when the engine finds zero results.
	// RRF scores are in [0, ~0.033] — any threshold above that fires universally.
	if len(ranks) == 0 {
		expandedTokens := e.expandTokens(rawTokens)

		e.inverted.RLock()
		expandedKeywords, expandedMatches := e.neuralExpand(rawTokens, expandedTokens)
		e.inverted.RUnlock()

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

func (e *Engine) expandTokens(rawTokens []string) []string {
	var expanded []string
	for _, token := range rawTokens {
		if len(token) < 3 {
			continue
		}
		neighbors := e.getSemanticNeighbors(token, 5, 0.70)
		for _, n := range neighbors {
			neighborTokens := e.analyzer.Analyze(n)
			if len(neighborTokens) > 0 {
				expanded = append(expanded, neighborTokens[0].Term)
			}
		}
	}
	return expanded
}

func (e *Engine) lexicalPass(queryTokens []string) (map[uint64]float64, map[uint64]map[string]bool) {
	keywordScores := make(map[uint64]float64)
	matchTokens := make(map[uint64]map[string]bool)

	idxData := e.inverted.GetData()
	idxPhon := e.phonetics.GetData()

	for _, token := range queryTokens {
		Q := len(token)

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

		if Q >= 2 {
			for _, match := range e.bkTree.Search(token, e.config.FuzzyMaxDist) {
				if match.Distance == 0 {
					continue
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

// vectorPass scores all documents by dot product with the query vector.
// Negative dot products are clamped to 0 — a document pointing away from
// the query has zero semantic relevance, not negative relevance.
func (e *Engine) vectorPass(queryVec []float32) map[uint64]float64 {
	scores := make(map[uint64]float64)
	if len(queryVec) == 0 {
		return scores
	}
	for id, entry := range e.vectors.GetVectors() {
		s := ranking.DotProduct(queryVec, Float16ToFloats(entry.Vector))
		if s > 0 {
			scores[id] = s
		}
	}
	return scores
}

func (e *Engine) neuralExpand(originalTokens []string, expandedTokens []string) (map[uint64]float64, map[uint64]map[string]bool) {
	keywordScores := make(map[uint64]float64)
	matchTokens := make(map[uint64]map[string]bool)
	idxData := e.inverted.GetData()

	for _, neighbor := range expandedTokens {
		targets := make(map[uint64]bool)
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

func (e *Engine) rankAndFuse(
	kwScores map[uint64]float64,
	matchToks map[uint64]map[string]bool,
	qryToks []string,
	vScores map[uint64]float64,
) []SearchResponse {

	// BM25-only mode: no vector scores are present.
	// Use proper BM25 as the primary ranking signal over the lexical candidates.
	// N-gram coverage scores have no IDF weighting, producing worse recall than
	// BM25 (adjacent RRF score gaps ~2.64e-4 >> the 1e-6 tiebreaker epsilon,
	// so the old BM25 tiebreaker path never fired).
	if len(vScores) == 0 {
		bm25Results := e.bm25.Query(qryToks)
		bm25ByID := make(map[uint64]float64, len(bm25Results))
		for _, r := range bm25Results {
			bm25ByID[r.DocID] = r.Score
		}
		kwIDs := make([]uint64, 0, len(kwScores))
		for id := range kwScores {
			kwIDs = append(kwIDs, id)
		}
		scored := e.scorer.Score(kwIDs, bm25ByID, nil, nil, e.idMapping)
		results := make([]SearchResponse, len(scored))
		for i, r := range scored {
			results[i] = SearchResponse{ID: r.ID, Score: r.Score}
		}
		return results
	}

	boosted := make(map[uint64]float64, len(kwScores))
	for id, score := range kwScores {
		if score <= 0 {
			continue
		}
		boosted[id] = score + 10000.0
		if len(matchToks[id]) >= len(qryToks) {
			boosted[id] += 50000.0
		}
	}

	kwIDs := make([]uint64, 0, len(boosted))
	for id := range boosted {
		kwIDs = append(kwIDs, id)
	}
	vcIDs := make([]uint64, 0, len(vScores))
	for id := range vScores {
		vcIDs = append(vcIDs, id)
	}

	// e.idMapping is safe here — Engine.mu.RLock() (Search) or Lock() (others) is held.
	scored := e.scorer.Score(kwIDs, boosted, vcIDs, vScores, e.idMapping)

	bm25Results := e.bm25.Query(qryToks)
	bm25Map := make(map[string]float64, len(bm25Results))
	for _, r := range bm25Results {
		bm25Map[e.idMapping[r.DocID]] = r.Score
	}

	const epsilon = 1e-6
	for i := 1; i < len(scored); i++ {
		for j := i; j > 0; j-- {
			a, b := scored[j-1], scored[j]
			rrfDiff := a.Score - b.Score
			if rrfDiff >= epsilon {
				break
			}
			if rrfDiff > -epsilon {
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

// neighborCandidate pairs a word with its similarity score for sorting.
type neighborCandidate struct {
	word  string
	score float32
}

// getSemanticNeighbors returns the topN most similar words to token by dot
// product, sorted descending by similarity. Previously truncated without
// sorting — nondeterministic under Go's randomised map iteration.
func (e *Engine) getSemanticNeighbors(token string, topN int, threshold float32) []string {
	e.vectors.RLock()
	defer e.vectors.RUnlock()

	wordVecs := e.vectors.GetWordVectors()
	tokenEntry, ok := wordVecs[token]
	if !ok {
		return nil
	}

	tokenVec := Float16ToFloats(tokenEntry.Vector)
	var candidates []neighborCandidate
	for word, entry := range wordVecs {
		if word == token {
			continue
		}
		s := float32(ranking.DotProduct(tokenVec, Float16ToFloats(entry.Vector)))
		if s >= threshold {
			candidates = append(candidates, neighborCandidate{word: word, score: s})
		}
	}

	// Sort descending by similarity so topN is deterministic.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	if topN > 0 && len(candidates) > topN {
		candidates = candidates[:topN]
	}

	out := make([]string, len(candidates))
	for i, c := range candidates {
		out[i] = c.word
	}
	return out
}

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

	limit := n
	if limit > MaxGram {
		limit = MaxGram
	}

	cap := 1 + (limit - MinGram)
	results := make([]string, 0, cap)
	results = append(results, token)

	for i := MinGram; i < limit; i++ {
		results = append(results, string(runes[0:i]))
	}

	return results
}

func removeID(ids []uint64, target uint64) []uint64 {
	out := ids[:0]
	for _, id := range ids {
		if id != target {
			out = append(out, id)
		}
	}
	return out
}

var saveFormatMagic = [4]byte{'Z', 'N', 'T', 'H'}

// saveFormatVersion v3: globalSeen is now map[string]int (ref count),
// docTokens map[uint64][]string added for globalSeen management on Remove.
const saveFormatVersion uint16 = 3

var ErrIncompatibleVersion = fmt.Errorf("index: incompatible file version — rebuild the index with the current binary")

// Save serialises all index state to filepath. Takes Engine.mu.RLock() so
// concurrent Searches can proceed during save, but Add/Remove/Load block.
func (e *Engine) Save(filepath string) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	start := time.Now()
	e.inverted.RLock()
	e.vectors.RLock()
	e.phonetics.RLock()
	defer e.inverted.RUnlock()
	defer e.vectors.RUnlock()
	defer e.phonetics.RUnlock()

	slog.Info("Saving index state", "path", filepath)

	tmp := filepath + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}

	if _, err := file.Write(saveFormatMagic[:]); err != nil {
		file.Close()
		os.Remove(tmp)
		return err
	}
	var vbuf [2]byte
	vbuf[0] = byte(saveFormatVersion >> 8)
	vbuf[1] = byte(saveFormatVersion)
	if _, err := file.Write(vbuf[:]); err != nil {
		file.Close()
		os.Remove(tmp)
		return err
	}

	enc := gob.NewEncoder(file)

	bm25Lengths, bm25TermFreqs, bm25DocFreq, bm25TotalDocs, bm25TotalLen := e.bm25.State()
	tfidfLengths, tfidfTermFreqs, tfidfDocFreq, tfidfTotalDocs := e.tfidf.State()

	state := []any{
		e.inverted.GetData(), e.idMapping, e.vectors.GetVectors(),
		e.inverted.GetTokenCounts(), e.phonetics.GetData(), e.inverted.GetVocabulary(),
		e.inverted.GetGlobalSeen(), e.vectors.GetWordVectors(), e.inverted.GetDocFragments(),
		bm25Lengths, bm25TermFreqs, bm25DocFreq, bm25TotalDocs, bm25TotalLen,
		tfidfLengths, tfidfTermFreqs, tfidfDocFreq, tfidfTotalDocs,
		// v3: docTokens for globalSeen reference counting
		e.inverted.GetDocTokens(),
	}
	for _, s := range state {
		if err := enc.Encode(s); err != nil {
			file.Close()
			os.Remove(tmp)
			return err
		}
	}

	if err := file.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, filepath); err != nil {
		os.Remove(tmp)
		return err
	}

	slog.Info("Index saved", "entries", len(e.inverted.GetData()), "duration", time.Since(start))
	return nil
}

// Load restores index state from a gob file. Takes Engine.mu.Lock().
func (e *Engine) Load(filepath string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.load(filepath); err != nil {
		return err
	}

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

	if err := e.rebuildFSTLocked(); err != nil {
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

	var magic [4]byte
	if _, err := f.Read(magic[:]); err != nil {
		return fmt.Errorf("index: failed to read file header: %w", err)
	}
	if magic != saveFormatMagic {
		return fmt.Errorf("index: not a ZENITH index file (bad magic bytes)")
	}
	var vbuf [2]byte
	if _, err := f.Read(vbuf[:]); err != nil {
		return fmt.Errorf("index: failed to read version: %w", err)
	}
	version := uint16(vbuf[0])<<8 | uint16(vbuf[1])
	if version != saveFormatVersion {
		return ErrIncompatibleVersion
	}

	dec := gob.NewDecoder(f)

	vData := e.inverted.GetData()
	vVectors := e.vectors.GetVectors()
	vToken := e.inverted.GetTokenCounts()
	vPhon := e.phonetics.GetData()
	vVocab := e.inverted.GetVocabulary()
	vSeen := e.inverted.GetGlobalSeen()
	vWordVectors := e.vectors.GetWordVectors()
	vFrag := e.inverted.GetDocFragments()
	vDocToks := e.inverted.GetDocTokens()

	var bm25Lengths map[uint64]int
	var bm25TermFreqs map[uint64]map[string]int
	var bm25DocFreq map[string]int
	var bm25TotalDocs, bm25TotalLen int

	var tfidfLengths map[uint64]int
	var tfidfTermFreqs map[uint64]map[string]int
	var tfidfDocFreq map[string]int
	var tfidfTotalDocs int

	state := []any{
		&vData, &e.idMapping, &vVectors,
		&vToken, &vPhon, &vVocab,
		&vSeen, &vWordVectors, &vFrag,
		&bm25Lengths, &bm25TermFreqs, &bm25DocFreq, &bm25TotalDocs, &bm25TotalLen,
		&tfidfLengths, &tfidfTermFreqs, &tfidfDocFreq, &tfidfTotalDocs,
		&vDocToks, // v3
	}
	for _, s := range state {
		if err := dec.Decode(s); err != nil {
			return err
		}
	}

	e.bm25.LoadState(bm25Lengths, bm25TermFreqs, bm25DocFreq, bm25TotalDocs, bm25TotalLen)
	e.tfidf.LoadState(tfidfLengths, tfidfTermFreqs, tfidfDocFreq, tfidfTotalDocs)

	slog.Info("Index loaded", "docs", len(e.idMapping), "duration", time.Since(start))
	return nil
}
