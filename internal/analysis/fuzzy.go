package analysis

import "sort"

// MAX_DISTANCE is the maximum Levenshtein edit distance considered a fuzzy match.
// Used consistently in Levenshtein early termination AND BKTree.Search.
const MAX_DISTANCE = 2

// Levenshtein computes the edit distance between s1 and s2.
// Returns (distance, true) when the distance is within MAX_DISTANCE.
// Returns (0, false) when early termination fires — distance exceeds MAX_DISTANCE.
// Callers MUST check the bool before using the int.
func Levenshtein(s1, s2 string) (int, bool) {
	if len(s1) > len(s2) {
		s1, s2 = s2, s1
	}

	n, m := len(s1), len(s2)

	prevRow := make([]int, n+1)
	currRow := make([]int, n+1)

	for i := 0; i <= n; i++ {
		prevRow[i] = i
	}

	for j := 1; j <= m; j++ {
		currRow[0] = j
		for i := 1; i <= n; i++ {
			cost := 1
			if s1[i-1] == s2[j-1] {
				cost = 0
			}
			currRow[i] = min(prevRow[i]+1, currRow[i-1]+1, prevRow[i-1]+cost)
		}

		// Early termination: if every value in currRow exceeds MAX_DISTANCE,
		// no further rows can bring the distance back down.
		allOver := true
		for _, val := range currRow {
			if val <= MAX_DISTANCE {
				allOver = false
				break
			}
		}
		if allOver {
			return 0, false
		}

		copy(prevRow, currRow)
	}

	return prevRow[n], true
}

// -----------------------------------------------------------------------------
// FuzzySearcher
// -----------------------------------------------------------------------------

// FuzzySearcher wraps a BKTree and exposes the fuzzy search interface used by
// the query pipeline. The engine (index/engine.go) holds a *BKTree directly
// for the hot lexicalPass path — FuzzySearcher is the higher-level API for
// components that shouldn't touch BKTree internals.
//
// Lifecycle:
//
//	fs := NewFuzzySearcher()
//	fs.Build(terms)                        // bulk load from existing index
//	fs.Add("kubernetes")                   // incremental as docs are indexed
//	matches := fs.Search("kubrnets", 2)    // query time
type FuzzySearcher struct {
	tree *BKTree
}

// NewFuzzySearcher creates an empty FuzzySearcher.
func NewFuzzySearcher() *FuzzySearcher {
	return &FuzzySearcher{tree: NewBKTree()}
}

// Add inserts a single term. Call this per-term during indexing.
// Safe to call after Build — the tree grows incrementally.
func (f *FuzzySearcher) Add(term string) {
	f.tree.Add(term)
}

// Build populates the fuzzy index from a slice of terms.
// Typically called once when loading an existing index from disk.
func (f *FuzzySearcher) Build(terms []string) {
	for _, t := range terms {
		f.tree.Add(t)
	}
}

// Search returns all indexed terms within maxDist edits of query,
// sorted ascending by edit distance (closest match first).
// Pass maxDist <= 0 to use MAX_DISTANCE.
func (f *FuzzySearcher) Search(query string, maxDist int) []FuzzyMatch {
	if maxDist <= 0 {
		maxDist = MAX_DISTANCE
	}
	results := f.tree.Search(query, maxDist)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Distance < results[j].Distance
	})
	return results
}

// Size returns the number of terms in the fuzzy index.
func (f *FuzzySearcher) Size() int {
	return f.tree.Size()
}
