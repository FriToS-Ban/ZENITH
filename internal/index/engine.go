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

// SearchResponse holds search results.
type SearchResponse struct {
	ID    string
	Score float64
}

// Engine acts as the central orchestrator for all search components (AP-4 Fix).
type Engine struct {
	config    *config.Config
	inverted  *InvertedIndex
	vectors   *VectorStore
	phonetics *PhoneticIndex
	bkTree    *analysis.BKTree

	embedder embedding.Embedder
	scorer   ranking.Scorer
	analyzer analysis.Analyzer

	// ID mappings
	idMapping map[uint32]string
}

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
	}
}

// AP-5 Add API Fix: Adds context for proper trace cancellation and error handling
func (e *Engine) Add(ctx context.Context, originalID string, fullText string) error {
	logger := slog.With("doc_id", originalID)

	// Tokenizer flow
	tokens := e.analyzer.Analyze(fullText)
	var rawTokens []string
	for _, t := range tokens {
		rawTokens = append(rawTokens, t.Term)
	}

	// Vector Generation - handle embedding errors
	docVec, err := e.embedder.Embed(ctx, fullText)
	if err != nil {
		logger.Warn("Embedding failed, indexing purely lexically", "error", err)
	}

	tempWordVectors := make(map[string]VectorEntry)
	var tokensToEmbed []string

	for _, t := range rawTokens {
		_, exists := tempWordVectors[t]
		if !e.vectors.HasWordVector(t) && !exists {
			tokensToEmbed = append(tokensToEmbed, t)
			tempWordVectors[t] = VectorEntry{} // reserve spot
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
			// Remove empty reservations if batch failed
			for _, t := range tokensToEmbed {
				delete(tempWordVectors, t)
			}
		}
	}

	h := fnv.New32a()
	h.Write([]byte(originalID))
	internalID := uint32(h.Sum32())

	// Write Locks over disparate indexes
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

	// Idempotency: Remove previous entries if document already exists
	if oldFrags, exists := idxFrags[internalID]; exists {
		for _, frag := range oldFrags {
			if idList, ok := idxData[frag]; ok {
				var newList []uint32
				for _, id := range idList {
					if id != internalID {
						newList = append(newList, id)
					}
				}
				idxData[frag] = newList
			}
			// Clean up phonetic data
			if idList, ok := idxPhon[frag]; ok {
				var newList []uint32
				for _, id := range idList {
					if id != internalID {
						newList = append(newList, id)
					}
				}
				idxPhon[frag] = newList
			}
		}
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

		fragments := generateEdgeNgrams(token)

		for _, frag := range fragments {
			if seenInDoc[frag] {
				continue
			}
			seenInDoc[frag] = true
			idxData[frag] = append(idxData[frag], internalID)
			docFrags = append(docFrags, frag)
		}

		phon := analysis.Soundex(token)
		if phon != "" && !seenInDoc[phon] {
			idxPhon[phon] = append(idxPhon[phon], internalID)
			seenInDoc[phon] = true
			docFrags = append(docFrags, phon)
		}

		if !glob[token] {
			L := len(token)
			vocab[L] = append(vocab[L], token)
			glob[token] = true
			e.bkTree.Add(token)
		}
	}
	idxFrags[internalID] = docFrags

	return nil
}

// AP-2 and AP-5 Fixes: Structured Search passes context and splits locking zones
func (e *Engine) Search(ctx context.Context, query string) ([]SearchResponse, error) {
	tokens := e.analyzer.Analyze(query)
	var rawTokens []string
	for _, t := range tokens {
		rawTokens = append(rawTokens, t.Term)
	}

	// RLock #1: Lexical
	e.inverted.RLock()
	e.phonetics.RLock()
	keywordScores, matchTokens := e.lexicalPass(rawTokens)
	e.phonetics.RUnlock()
	e.inverted.RUnlock()

	// Embed API Call
	queryVec, err := e.embedder.Embed(ctx, query)
	if err != nil {
		slog.Warn("Search vectors degraded. Nerve unreachable", "error", err)
	}

	// RLock #2: Vectors
	e.vectors.RLock()
	vectorScores := e.vectorPass(queryVec)
	e.vectors.RUnlock()

	// Locklessly score
	ranks := e.rankAndFuse(keywordScores, matchTokens, rawTokens, vectorScores)

	// Expansion phase condition
	if len(ranks) == 0 || (len(ranks) > 0 && ranks[0].Score < 5.0) {

		// Network calls outside of internal engine index locks
		var expandedTokens []string
		for _, token := range rawTokens {
			if len(token) >= 3 {
				neighbors := e.getSemanticNeighbors(token, 5, 0.70)
				for _, n := range neighbors {
					stemmedNeighbor := e.analyzer.(*analysis.StandardAnalyzer).Tokenize(n)
					if len(stemmedNeighbor) > 0 {
						expandedTokens = append(expandedTokens, stemmedNeighbor[0])
					}
				}
			}
		}

		// RLock #3: Expansion Application
		e.inverted.RLock()
		expandedKeywords, expandedMatches := e.neuralExpand(rawTokens, expandedTokens)
		e.inverted.RUnlock()

		// Merge mappings logic
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

func (e *Engine) lexicalPass(queryTokens []string) (map[uint32]float64, map[uint32]map[string]bool) {
	keywordScores := make(map[uint32]float64)
	matchTokens := make(map[uint32]map[string]bool)

	idxData := e.inverted.GetData()
	idxPhon := e.phonetics.GetData()

	for _, token := range queryTokens {
		Q := len(token)

		// 1. N-Grams
		var searchFragments []string
		if Q >= 3 {
			searchFragments = generateEdgeNgrams(token)
		} else {
			searchFragments = []string{token}
		}

		for _, frag := range searchFragments {
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
		phon := analysis.Soundex(token)
		if ids, ok := idxPhon[phon]; ok {
			for _, id := range ids {
				keywordScores[id] += e.config.PhoneticWeight // configurable
				if matchTokens[id] == nil {
					matchTokens[id] = make(map[string]bool)
				}
				matchTokens[id][token] = true
			}
		}

		// 3. Fuzzy (Levenshtein)
		if Q > 3 {
			matches := e.bkTree.Search(token, e.config.FuzzyMaxDist)
			for _, match := range matches {
				if match.Distance == 0 {
					continue // exact match already handled by n-gram path
				}
				if ids, exists := idxData[match.Word]; exists {
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
	vectorScores := make(map[uint32]float64)
	if len(queryVec) == 0 {
		return vectorScores
	}
	vStore := e.vectors.GetVectors()
	for id, docEntry := range vStore {
		vectorScores[id] = ranking.DotProduct(queryVec, Float16ToFloats(docEntry.Vector))
	}
	return vectorScores
}

func (e *Engine) neuralExpand(originalTokens []string, expandedTokens []string) (map[uint32]float64, map[uint32]map[string]bool) {
	keywordScores := make(map[uint32]float64)
	matchTokens := make(map[uint32]map[string]bool)
	idxData := e.inverted.GetData()

	for _, stemmedNeighbor := range expandedTokens {
		targets := make(map[uint32]bool)
		if ids, ok := idxData[stemmedNeighbor]; ok {
			for _, id := range ids {
				targets[id] = true
			}
		}
		if len(stemmedNeighbor) > 3 {
			prefix := stemmedNeighbor[:3]
			if ids, ok := idxData[prefix]; ok {
				for _, id := range ids {
					targets[id] = true
				}
			}
		}

		for id := range targets {
			keywordScores[id] += 20000.0 // from ap-1 score
			if matchTokens[id] == nil {
				matchTokens[id] = make(map[string]bool)
			}

			// Map back onto the original query array context safely.
			// Realistically we'd trace neighbor->original parent
			if len(originalTokens) > 0 {
				matchTokens[id][originalTokens[0]] = true
			}
		}
	}

	return keywordScores, matchTokens
}

func (e *Engine) rankAndFuse(kwScores map[uint32]float64, matchToks map[uint32]map[string]bool, qryToks []string, vScores map[uint32]float64) []SearchResponse {
	for id, score := range kwScores {
		if score > 0 {
			kwScores[id] += 10000.0
			if len(matchToks[id]) >= len(qryToks) {
				kwScores[id] += 50000.0
			}
		}
	}

	kwIDs := make([]uint32, 0, len(kwScores))
	for id, score := range kwScores {
		if score > 0 {
			kwIDs = append(kwIDs, id)
		}
	}

	vcIDs := make([]uint32, 0, len(vScores))
	for id := range vScores {
		vcIDs = append(vcIDs, id)
	}

	scored := e.scorer.Score(kwIDs, kwScores, vcIDs, vScores, e.idMapping)
	var finalRes []SearchResponse
	for _, r := range scored {
		finalRes = append(finalRes, SearchResponse{
			ID:    r.ID,
			Score: r.Score,
		})
	}

	return finalRes
}

func (e *Engine) getSemanticNeighbors(token string, topN int, threshold float32) []string {
	// Simple passthrough representation
	e.vectors.RLock()
	defer e.vectors.RUnlock()
	wordAndVec := e.vectors.GetWordVectors()

	tokenEntry, ok := wordAndVec[token]
	if !ok {
		return []string{}
	}

	var candidates []string
	for word, entry := range wordAndVec {
		if word == token {
			continue
		}
		score := float32(ranking.DotProduct(Float16ToFloats(tokenEntry.Vector), Float16ToFloats(entry.Vector)))
		if score >= threshold {
			candidates = append(candidates, word)
		}
	}
	limit := min(topN, len(candidates))
	return candidates[:limit]
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

	results := make([]string, 0, limit-MinGram+1)
	results = append(results, token)
	for i := MinGram; i < limit; i++ {
		results = append(results, string(runes[0:i]))
	}

	if n > MaxGram {
		// keeping old behavior initially
		results = append(results, token)
	}

	return results
}

// Global persistence maps
func (e *Engine) Save(filepath string) error {
	start := time.Now()
	// Acquiring locks for serialisation
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

	encoder := gob.NewEncoder(file)

	// Persist EVERYTHING
	state := []any{
		e.inverted.GetData(), e.idMapping, e.vectors.GetVectors(),
		e.inverted.GetTokenCounts(), e.phonetics.GetData(), e.inverted.GetVocabulary(),
		e.inverted.GetGlobalSeen(), e.vectors.GetWordVectors(), e.inverted.GetDocFragments(),
	}

	for _, s := range state {
		if err := encoder.Encode(s); err != nil {
			return err
		}
	}

	slog.Info("Index saved successfully", "entries", len(e.inverted.GetData()), "duration", time.Since(start))
	return nil
}

func (e *Engine) Load(filepath string) error {
	start := time.Now()
	e.inverted.Lock()
	e.vectors.Lock()
	e.phonetics.Lock()
	defer e.inverted.Unlock()
	defer e.vectors.Unlock()
	defer e.phonetics.Unlock()

	osClient, err := os.Open(filepath)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Warn("No persistence file found. Starting fresh", "path", filepath)
			return err
		}
		return err
	}
	defer osClient.Close()

	info := gob.NewDecoder(osClient)

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
		if err := info.Decode(s); err != nil {
			return err
		}
	}

	slog.Info("Successfully loaded internal IDs from disk", "count", len(e.idMapping), "duration", time.Since(start))
	return nil
}
