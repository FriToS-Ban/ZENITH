package ranking

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

// bruteForceQuery is the original O(N) implementation, kept as the reference:
// score every indexed document, keep the positives, sort descending.
func bruteForceQuery(s *BM25Scorer, queryTerms []string) map[uint64]float64 {
	out := make(map[uint64]float64)
	for docID := range s.termFreqs {
		var sc float64
		for _, term := range queryTerms {
			sc += s.scoreDocParams(docID, []string{term}, s.k1, s.b)
		}
		if sc > 0 {
			out[docID] = sc
		}
	}
	return out
}

func randomCorpus(s *BM25Scorer, rng *rand.Rand, nDocs, vocab int) {
	for d := 0; d < nDocs; d++ {
		n := 20 + rng.Intn(80)
		tokens := make([]string, n)
		for i := range tokens {
			// Zipf-ish skew: low word IDs are common, high are rare.
			w := int(math.Floor(math.Pow(rng.Float64(), 2) * float64(vocab)))
			tokens[i] = fmt.Sprintf("w%d", w)
		}
		s.Index(uint64(d+1), tokens)
	}
}

// TestQuery_PostingListMatchesBruteForce verifies the posting-list Query
// produces exactly the same document set and scores as scoring the full
// corpus, including queries with repeated and unknown terms, and after
// re-index and Remove operations.
func TestQuery_PostingListMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	s := NewBM25Scorer(BM25Params{})
	randomCorpus(s, rng, 2000, 500)

	// Mutate: re-index some docs, remove some — postings must stay in sync.
	for d := 0; d < 100; d++ {
		s.Index(uint64(d+1), []string{"w1", "w2", "w3", "reindexed"})
	}
	for d := 200; d < 250; d++ {
		s.Remove(uint64(d + 1))
	}

	queries := [][]string{
		{"w1"},
		{"w1", "w400", "w499"},
		{"w7", "w7", "w7"}, // repeated terms must count per occurrence
		{"unknownterm"},
		{"w3", "unknownterm", "reindexed"},
		{},
	}
	for _, q := range queries {
		got := s.Query(q)
		want := bruteForceQuery(s, q)

		if len(got) != len(want) {
			t.Fatalf("query %v: got %d results, brute force %d", q, len(got), len(want))
		}
		for _, r := range got {
			ref, ok := want[r.DocID]
			if !ok {
				t.Fatalf("query %v: doc %d returned but brute force scored it 0", q, r.DocID)
			}
			if math.Abs(r.Score-ref) > 1e-9 {
				t.Fatalf("query %v: doc %d score %v, brute force %v", q, r.DocID, r.Score, ref)
			}
		}
		for i := 1; i < len(got); i++ {
			if got[i].Score > got[i-1].Score {
				t.Fatalf("query %v: results not sorted descending at %d", q, i)
			}
		}
	}
}

// TestLoadState_RebuildsPostings verifies that postings derived in LoadState
// behave identically to postings built incrementally via Index.
func TestLoadState_RebuildsPostings(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	s := NewBM25Scorer(BM25Params{})
	randomCorpus(s, rng, 500, 200)

	restored := NewBM25Scorer(BM25Params{})
	restored.LoadState(s.State())

	q := []string{"w1", "w50", "w199"}
	a, b := s.Query(q), restored.Query(q)
	if len(a) != len(b) {
		t.Fatalf("result count mismatch after LoadState: %d vs %d", len(a), len(b))
	}
	aScores := make(map[uint64]float64, len(a))
	for _, r := range a {
		aScores[r.DocID] = r.Score
	}
	for _, r := range b {
		if math.Abs(aScores[r.DocID]-r.Score) > 1e-9 {
			t.Fatalf("doc %d score mismatch after LoadState", r.DocID)
		}
	}
}

// BenchmarkQuery100k measures posting-list Query latency on a 100k-doc corpus
// with a 4-term query. The previous O(N) implementation took ~280ms here.
func BenchmarkQuery100k(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	s := NewBM25Scorer(BM25Params{})
	randomCorpus(s, rng, 100_000, 30_000)
	q := []string{"w100", "w5000", "w20000", "w29000"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Query(q)
	}
}
