package ranking

import (
	"slices"
	"sort"
	"strings"
)

type RRFRanker struct {
	k float64
}

func NewRRFRanker(k float64) *RRFRanker {
	return &RRFRanker{k: k}
}

func (r *RRFRanker) Score(keywordIDs []uint32, keywordScores map[uint32]float64, vectorIDs []uint32, vectorScores map[uint32]float64, idMapping map[uint32]string) []ScoredResult {
	rrfScores := make(map[uint32]float64)

	// Multi-Level Tie-Breaking: Keyword Score -> Vector Score -> ID
	slices.SortFunc(keywordIDs, func(a, b uint32) int {
		if keywordScores[a] != keywordScores[b] {
			if keywordScores[b] > keywordScores[a] {
				return 1
			}
			return -1
		}
		// Tie-breaker 1: Vector similarity (Neural context)
		if vectorScores[a] != vectorScores[b] {
			if vectorScores[b] > vectorScores[a] {
				return 1
			}
			return -1
		}
		// Final tie-breaker: Alphabetical Order
		return strings.Compare(idMapping[a], idMapping[b])
	})

	// 2. Vector Ranking (Global)
	// Stable Sort with Tie-Breaking
	slices.SortFunc(vectorIDs, func(a, b uint32) int {
		if vectorScores[a] != vectorScores[b] {
			if vectorScores[b] > vectorScores[a] {
				return 1
			}
			return -1
		}
		// Tie-breaker: Alphabetical order of original IDs
		return strings.Compare(idMapping[a], idMapping[b])
	})

	// 3. RRF Blending
	// Proper RRF without the 100x keyword multiplication bug
	for rank, id := range keywordIDs {
		rrfScores[id] += 1.0 / (r.k + float64(rank+1))
	}
	for rank, id := range vectorIDs {
		rrfScores[id] += 1.0 / (r.k + float64(rank+1))
	}

	results := make([]ScoredResult, 0, len(rrfScores))
	for id, score := range rrfScores {
		results = append(results, ScoredResult{
			ID:    idMapping[id],
			Score: score,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})

	// Return Top 5 for now
	if len(results) > 5 {
		results = results[:5]
	}

	return results
}
