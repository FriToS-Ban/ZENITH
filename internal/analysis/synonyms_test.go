package analysis

import (
	"slices"
	"testing"
)

func TestSynonyms(t *testing.T) {
	// Known entry — must have synonyms.
	syns := Synonyms("search")
	if len(syns) == 0 {
		t.Error("Synonyms('search') returned empty slice")
	}
	// Unknown entry — must return nil.
	if Synonyms("xyzzy") != nil {
		t.Error("Synonyms for unknown term should be nil")
	}
}

func TestExpandWithSynonyms_NoDuplicates(t *testing.T) {
	tokens := []string{"search", "search"}
	expanded := ExpandWithSynonyms(tokens)
	seen := make(map[string]int)
	for _, tok := range expanded {
		seen[tok]++
	}
	for tok, count := range seen {
		if count > 1 {
			t.Errorf("duplicate token %q in expanded result", tok)
		}
	}
}

func TestExpandWithSynonyms_PreservesOriginal(t *testing.T) {
	tokens := []string{"search", "cluster"}
	expanded := ExpandWithSynonyms(tokens)
	for _, orig := range tokens {
		if !slices.Contains(expanded, orig) {
			t.Errorf("original token %q missing from expanded result %v", orig, expanded)
		}
	}
}

func TestExpandWithSynonyms_AddsSynonyms(t *testing.T) {
	// "search" maps to ["retriev","query","find","lookup"]
	expanded := ExpandWithSynonyms([]string{"search"})
	if len(expanded) <= 1 {
		t.Errorf("expected synonyms added for 'search', got %v", expanded)
	}
}

func TestExpandWithSynonyms_Empty(t *testing.T) {
	got := ExpandWithSynonyms(nil)
	if len(got) != 0 {
		t.Errorf("expected empty for nil input, got %v", got)
	}
}
