package ranking

import (
	"testing"
)

func TestBM25_IndexAndQuery(t *testing.T) {
	s := NewBM25Scorer(BM25Params{})

	s.Index(1, []string{"kubernetes", "cluster", "deploy", "pod"})
	s.Index(2, []string{"docker", "container", "image", "build"})
	s.Index(3, []string{"kubernetes", "pod", "service", "ingress"})

	if s.DocCount() != 3 {
		t.Errorf("DocCount = %d, want 3", s.DocCount())
	}

	results := s.Query([]string{"kubernetes", "pod"})
	if len(results) == 0 {
		t.Fatal("expected results for known terms")
	}

	// Docs 1 and 3 both contain "kubernetes" and "pod"; doc 2 has neither.
	docIDs := make(map[uint32]bool)
	for _, r := range results {
		docIDs[r.DocID] = true
	}
	if !docIDs[1] || !docIDs[3] {
		t.Errorf("expected docs 1 and 3 in results, got %v", results)
	}
	if docIDs[2] {
		t.Error("doc 2 should not appear for 'kubernetes pod' query")
	}
}

func TestBM25_ScoresDescending(t *testing.T) {
	s := NewBM25Scorer(BM25Params{})
	s.Index(1, []string{"search", "search", "search", "index"})
	s.Index(2, []string{"search", "index"})
	s.Index(3, []string{"index", "store"})

	results := s.Query([]string{"search"})
	for i := 1; i < len(results); i++ {
		if results[i-1].Score < results[i].Score {
			t.Errorf("results not sorted descending at index %d: %.4f < %.4f",
				i, results[i-1].Score, results[i].Score)
		}
	}
}

func TestBM25_Remove(t *testing.T) {
	s := NewBM25Scorer(BM25Params{})
	s.Index(1, []string{"kubernetes", "deploy"})
	s.Index(2, []string{"kubernetes", "cluster"})

	s.Remove(1)

	if s.DocCount() != 1 {
		t.Errorf("DocCount after Remove = %d, want 1", s.DocCount())
	}

	results := s.Query([]string{"kubernetes"})
	for _, r := range results {
		if r.DocID == 1 {
			t.Error("removed doc 1 still appears in query results")
		}
	}
}

func TestBM25_ReIndex(t *testing.T) {
	s := NewBM25Scorer(BM25Params{})
	s.Index(1, []string{"old", "content"})
	s.Index(1, []string{"new", "content"}) // re-index same doc

	if s.DocCount() != 1 {
		t.Errorf("DocCount after re-index = %d, want 1", s.DocCount())
	}

	// "old" should no longer score; "new" should.
	oldResults := s.Query([]string{"old"})
	for _, r := range oldResults {
		if r.DocID == 1 {
			t.Error("old term still scores after re-index")
		}
	}

	newResults := s.Query([]string{"new"})
	found := false
	for _, r := range newResults {
		if r.DocID == 1 {
			found = true
		}
	}
	if !found {
		t.Error("new term not found after re-index")
	}
}

func TestBM25_EmptyQuery(t *testing.T) {
	s := NewBM25Scorer(BM25Params{})
	s.Index(1, []string{"hello"})
	results := s.Query(nil)
	if len(results) != 0 {
		t.Errorf("expected empty results for nil query, got %v", results)
	}
}

func TestBM25_UnknownTerm(t *testing.T) {
	s := NewBM25Scorer(BM25Params{})
	s.Index(1, []string{"known"})
	results := s.Query([]string{"unknown_term_xyz"})
	if len(results) != 0 {
		t.Errorf("expected no results for unknown term, got %v", results)
	}
}

func TestBM25_LengthNormalisation(t *testing.T) {
	// Two documents: same term frequency but different length.
	// BM25 should favour the shorter doc (same "search" occurrence in fewer words).
	s := NewBM25Scorer(BM25Params{K1: 1.2, B: 0.75})
	// Short: 2 tokens
	s.Index(1, []string{"search", "fast"})
	// Long: 10 tokens — "search" diluted by length
	s.Index(2, []string{"search", "a", "b", "c", "d", "e", "f", "g", "h", "i"})

	results := s.Query([]string{"search"})
	if len(results) < 2 {
		t.Fatal("expected 2 results")
	}
	if results[0].DocID != 1 {
		t.Errorf("expected short doc (1) to rank higher, got doc %d first", results[0].DocID)
	}
}
