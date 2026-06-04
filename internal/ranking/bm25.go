package ranking

import (
	"math"
	"sort"
	"sync"
)

// BM25Scorer implements the BM25 (Okapi BM25) ranking function.
//
// BM25 improves on TF-IDF with two key parameters:
//   - k1 (term saturation): controls how much repeated terms matter.
//     After a point, more occurrences stop helping. Typical: 1.2–2.0.
//   - b (length normalisation): penalises long documents. 0 = no normalisation,
//     1 = full normalisation. Typical: 0.75.
//
// Formula per term t in document d:
//
//	IDF(t) = log( (N - df(t) + 0.5) / (df(t) + 0.5) + 1 )
//	TF_norm(t,d) = freq(t,d) * (k1+1) / (freq(t,d) + k1*(1 - b + b*(|d|/avgdl)))
//	BM25(d,Q) = Σ IDF(t) * TF_norm(t,d)  for t in Q
//
// It implements the Scorer interface so it can be used interchangeably with
// RRFRanker inside the Engine.
//
// Thread safety: Index() and Remove() mutate state — serialise these.
// Query() and Score() only read — safe for concurrent use after indexing.
type BM25Scorer struct {
	mu sync.RWMutex

	k1 float64 // term saturation parameter (default 1.2)
	b  float64 // length normalisation parameter (default 0.75)

	// Per-document state
	docLengths map[uint64]int            // internalID → term count
	termFreqs  map[uint64]map[string]int // internalID → term → frequency

	// Corpus-level state
	docFreq   map[string]int // term → number of documents containing it
	totalDocs int
	totalLen  int // sum of all document lengths (for avgdl)
}

// BM25Params allows tuning k1 and b. Zero value uses defaults.
type BM25Params struct {
	K1 float64 // default 1.2
	B  float64 // default 0.75
}

// NewBM25Scorer creates a BM25Scorer with the given parameters.
// Pass zero BM25Params{} to use defaults (k1=1.2, b=0.75).
func NewBM25Scorer(p BM25Params) *BM25Scorer {
	k1 := p.K1
	if k1 == 0 {
		k1 = 1.2
	}
	b := p.B
	if b == 0 {
		b = 0.75
	}
	return &BM25Scorer{
		k1:         k1,
		b:          b,
		docLengths: make(map[uint64]int),
		termFreqs:  make(map[uint64]map[string]int),
		docFreq:    make(map[string]int),
	}
}

// Index records term frequencies for a document.
// tokens must already be stemmed/normalised — use analysis.TokenizeString.
// If the document was previously indexed it is cleanly re-indexed.
func (s *BM25Scorer) Index(docID uint64, tokens []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove old stats if re-indexing
	if old, exists := s.termFreqs[docID]; exists {
		for term := range old {
			s.docFreq[term]--
			if s.docFreq[term] <= 0 {
				delete(s.docFreq, term)
			}
		}
		s.totalLen -= s.docLengths[docID]
		s.totalDocs--
	}

	tf := make(map[string]int, len(tokens))
	for _, tok := range tokens {
		tf[tok]++
	}

	s.termFreqs[docID] = tf
	s.docLengths[docID] = len(tokens)
	s.totalLen += len(tokens)
	s.totalDocs++

	for term := range tf {
		s.docFreq[term]++
	}
}

// Remove deletes a document from the BM25 index.
func (s *BM25Scorer) Remove(docID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	old, exists := s.termFreqs[docID]
	if !exists {
		return
	}
	for term := range old {
		s.docFreq[term]--
		if s.docFreq[term] <= 0 {
			delete(s.docFreq, term)
		}
	}
	s.totalLen -= s.docLengths[docID]
	s.totalDocs--
	delete(s.termFreqs, docID)
	delete(s.docLengths, docID)
}

// State returns a snapshot of BM25 corpus state for serialisation.
func (s *BM25Scorer) State() (map[uint64]int, map[uint64]map[string]int, map[string]int, int, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.docLengths, s.termFreqs, s.docFreq, s.totalDocs, s.totalLen
}

// LoadState restores BM25 corpus state after deserialisation.
func (s *BM25Scorer) LoadState(docLengths map[uint64]int, termFreqs map[uint64]map[string]int, docFreq map[string]int, totalDocs, totalLen int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docLengths = docLengths
	s.termFreqs = termFreqs
	s.docFreq = docFreq
	s.totalDocs = totalDocs
	s.totalLen = totalLen
}

// avgdl returns the average document length across the corpus.
func (s *BM25Scorer) avgdl() float64 {
	if s.totalDocs == 0 {
		return 0
	}
	return float64(s.totalLen) / float64(s.totalDocs)
}

// idf computes the IDF component for a term.
// Uses the Robertson-Walker IDF variant with +1 smoothing to avoid
// negative values for terms appearing in more than half the corpus.
func (s *BM25Scorer) idf(term string) float64 {
	df := float64(s.docFreq[term])
	n := float64(s.totalDocs)
	return math.Log((n-df+0.5)/(df+0.5) + 1)
}

// scoreDoc computes the BM25 score for a single document against query terms.
func (s *BM25Scorer) scoreDoc(docID uint64, queryTerms []string) float64 {
	tf := s.termFreqs[docID]
	dl := float64(s.docLengths[docID])
	avgdl := s.avgdl()

	var score float64
	for _, term := range queryTerms {
		freq := float64(tf[term])
		if freq == 0 {
			continue
		}
		idf := s.idf(term)
		// BM25 TF normalisation
		tfNorm := freq * (s.k1 + 1) / (freq + s.k1*(1-s.b+s.b*(dl/avgdl)))
		score += idf * tfNorm
	}
	return score
}

// BM25Result is a scored document from a BM25 query.
type BM25Result struct {
	DocID uint64
	Score float64
}

// Query scores all indexed documents against queryTerms and returns results
// sorted descending by score. Documents with score 0 are excluded.
// queryTerms must be stemmed/normalised the same way as at index time.
func (s *BM25Scorer) Query(queryTerms []string) []BM25Result {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(queryTerms) == 0 || s.totalDocs == 0 {
		return nil
	}

	results := make([]BM25Result, 0, len(s.termFreqs))
	for docID := range s.termFreqs {
		sc := s.scoreDoc(docID, queryTerms)
		if sc > 0 {
			results = append(results, BM25Result{DocID: docID, Score: sc})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}

// Score implements the ranking.Scorer interface so BM25Scorer can be used
// as a drop-in replacement for RRFRanker inside the Engine.
//
// When used as a Scorer, it blends its own BM25 keyword scores with the
// provided vectorScores using a simple weighted sum:
//
//	final = 0.6 * bm25_normalised + 0.4 * vector_normalised
//
// This preserves the hybrid nature of the Engine while using BM25 for the
// lexical component instead of the raw n-gram scoring.
func (s *BM25Scorer) Score(
	keywordIDs []uint64,
	keywordScores map[uint64]float64,
	vectorIDs []uint64,
	vectorScores map[uint64]float64,
	idMapping map[uint64]string,
) []ScoredResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Collect all candidate document IDs
	seen := make(map[uint64]struct{})
	for _, id := range keywordIDs {
		seen[id] = struct{}{}
	}
	for _, id := range vectorIDs {
		seen[id] = struct{}{}
	}

	// Normalise BM25 keyword scores to [0,1]
	var maxKW float64
	for _, id := range keywordIDs {
		if keywordScores[id] > maxKW {
			maxKW = keywordScores[id]
		}
	}

	// Normalise vector scores to [0,1]
	var maxVec float64
	for _, id := range vectorIDs {
		if vectorScores[id] > maxVec {
			maxVec = vectorScores[id]
		}
	}

	results := make([]ScoredResult, 0, len(seen))
	for id := range seen {
		var normKW, normVec float64
		if maxKW > 0 {
			normKW = keywordScores[id] / maxKW
		}
		if maxVec > 0 {
			normVec = vectorScores[id] / maxVec
		}
		combined := 0.6*normKW + 0.4*normVec
		if combined > 0 {
			results = append(results, ScoredResult{
				ID:    idMapping[id],
				Score: combined,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})

	if len(results) > 10 {
		results = results[:10]
	}
	return results
}

// DocCount returns the number of indexed documents.
func (s *BM25Scorer) DocCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalDocs
}
