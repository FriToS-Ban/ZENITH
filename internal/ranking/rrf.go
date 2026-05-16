package ranking

import (
	"cmp"
	"math"
	"slices"
	"sort"
	"strings"
)

const (
	defaultK    = 60.0 // standard RRF k — 60 is the Cormack et al. recommendation
	defaultTopN = 10   // returnable results cap; was hardcoded 5 before
)

// RRFRanker implements Reciprocal Rank Fusion for merging lexical and
// vector result lists into a single ranked output.
//
// Formula: score(d) = Σ 1/(k + rank(d, list_i))  for each ranked list i
//
// Key properties:
//   - rank positions are 1-indexed (rank 1 = best)
//   - k dampens the advantage of top-ranked results (default 60)
//   - scores from both lists are simply summed — no normalisation needed
//   - documents appearing in both lists get a natural boost
type RRFRanker struct {
	k    float64
	topN int
}

// NewRRFRanker creates an RRFRanker.
// k=0 uses the standard value of 60.
// topN=0 uses the default of 10.
func NewRRFRanker(k float64, topN int) *RRFRanker {
	if k == 0 {
		k = defaultK
	}
	if topN == 0 {
		topN = defaultTopN
	}
	return &RRFRanker{k: k, topN: topN}
}

// Score implements the ranking.Scorer interface.
//
// Fixes vs the original implementation:
//
//  1. Copies input slices before sorting — the original mutated the
//     caller's slices via slices.SortFunc, which is a hidden side effect
//     that corrupts repeated searches using the same ID lists.
//
//  2. Float comparison uses a tolerance epsilon instead of != to avoid
//     false tie-breaks from floating-point noise.
//
//  3. Single result-collection loop — the original built rrfScores map
//     then ranged over it a second time. Now it's one pass.
//
//  4. topN is configurable on the struct, not hardcoded to 5.
//
//  5. cmp.Compare used for the string tie-breaker — cleaner, same semantics.
func (r *RRFRanker) Score(
	keywordIDs []uint32,
	keywordScores map[uint32]float64,
	vectorIDs []uint32,
	vectorScores map[uint32]float64,
	idMapping map[uint32]string,
) []ScoredResult {

	// --- 1. Sort copies, not the caller's slices ---

	kwSorted := make([]uint32, len(keywordIDs))
	copy(kwSorted, keywordIDs)
	vcSorted := make([]uint32, len(vectorIDs))
	copy(vcSorted, vectorIDs)

	// Keyword list: sort by keyword score desc,
	// tie-break by vector score desc, then alphabetically.
	slices.SortFunc(kwSorted, func(a, b uint32) int {
		if d := cmpFloat(keywordScores[b], keywordScores[a]); d != 0 {
			return d
		}
		if d := cmpFloat(vectorScores[b], vectorScores[a]); d != 0 {
			return d
		}
		return strings.Compare(idMapping[a], idMapping[b])
	})

	// Vector list: sort by vector score desc, tie-break alphabetically.
	slices.SortFunc(vcSorted, func(a, b uint32) int {
		if d := cmpFloat(vectorScores[b], vectorScores[a]); d != 0 {
			return d
		}
		return strings.Compare(idMapping[a], idMapping[b])
	})

	// --- 2. RRF accumulation ---

	// Pre-size to the union of both lists to avoid rehashing.
	capacity := len(kwSorted) + len(vcSorted)
	rrfScores := make(map[uint32]float64, capacity)

	for rank, id := range kwSorted {
		rrfScores[id] += 1.0 / (r.k + float64(rank+1))
	}
	for rank, id := range vcSorted {
		rrfScores[id] += 1.0 / (r.k + float64(rank+1))
	}

	// --- 3. Collect results in one pass ---

	results := make([]ScoredResult, 0, len(rrfScores))
	for id, score := range rrfScores {
		if score > 0 {
			results = append(results, ScoredResult{
				ID:    idMapping[id],
				Score: score,
			})
		}
	}

	// --- 4. Final sort: score desc, then ID asc for determinism ---

	sort.Slice(results, func(i, j int) bool {
		if d := cmpFloat(results[i].Score, results[j].Score); d != 0 {
			return d > 0 // higher score first
		}
		return cmp.Compare(results[i].ID, results[j].ID) < 0
	})

	// --- 5. Configurable cap ---

	if r.topN > 0 && len(results) > r.topN {
		results = results[:r.topN]
	}

	return results
}

// cmpFloat compares two float64s with an epsilon tolerance to avoid
// false tie-breaks caused by floating-point arithmetic noise.
// Returns negative, zero, or positive — same contract as cmp.Compare.
func cmpFloat(a, b float64) int {
	const eps = 1e-9
	diff := a - b
	if math.Abs(diff) < eps {
		return 0
	}
	if diff > 0 {
		return 1
	}
	return -1
}
