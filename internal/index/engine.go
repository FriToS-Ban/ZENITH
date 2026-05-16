package index

import (
	"context"
	"encoding/gob"
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

// Engine is the central orchestrator — it owns all sub-indexes and the
// scoring pipeline. All public methods are safe for concurrent use.
type Engine struct {
	config    *config.Config
	inverted  *InvertedIndex
	vectors   *VectorStore
	phonetics *PhoneticIndex
	bkTree    *analysis.BKTree

	embedder embedding.Embedder
	scorer   ranking.Scorer
	analyzer analysis.Analyzer

	// Supplementary scorers wired alongside the primary Scorer.
	// Both are updated on every Add/Remove so they stay in sync.
	bm25  *ranking.BM25Scorer
	tfidf *ranking.TFIDFScorer

	idMapping map[uint32]string
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
	}
}

// Add indexes a document. Safe to call with the same originalID to re-index
// (idempotent — old entries are removed cleanly before new ones are written).
func (e *Engine) Add(ctx context.Context, originalID string, fullText string) error {
	logger := slog.With("doc_id", originalID)

	tokens := e.analyzer.Analyze(fullText)
	rawTokens := make([]string, 0, len(tokens))
	for _, t := range tokens {
		rawTokens = append(rawTokens, t.Term)
	}

	// Embeddings — failures are non-fatal; falls back to lexical-only.
	docVec, err := e.embedder.Embed(ctx, fullText)
	if err != nil {
		logger.Warn("Embedding failed, indexing purely lexically", "error", err)
	}

	tempWordVectors := make(map[string]VectorEntry)
	var tokensToEmbed []string
	for _, t := range rawTokens {
		if _, exists := tempWordVectors[t]; !exists && !e.vectors.HasWordVector(t) {
			tokensToEmbed = append(tokensToEmbed, t)
			tempWordVectors[t] = VectorEntry{} // reserve
		}
	}

	if len(tokensToEmbed) > 0 {
		batchVecs, err := e.embedder.EmbedBatch(ctx, tokensToEmbed)
		if err == nil && len(batchVecs) == len(tokensToEmbed) {
			for i, t := range tokensToEmbed {
				tempWordVectors[t] = VectorEntry{
					Vector:    FloatsToFloat16(batchVecs[i]),
					Magnitude: ranking.Magnitude(batchVecs[i]),
				}
			}
		} else {
			logger.Warn("Batch embedding failed for tokens", "error", err)
			for _, t := range tokensToEmbed {
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
func (e *Engine) Search(ctx context.Context, query string) ([]SearchResponse, error) {
	tokens := e.analyzer.Analyze(query)
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

// Load restores index state from a gob file written by Save.
func (e *Engine) Load(filepath string) error {
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
