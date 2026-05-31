package analysis

import "testing"

// ─── Levenshtein ─────────────────────────────────────────────────────────────

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b    string
		want    int
		withinN bool // expects ok==true
	}{
		{"", "", 0, true},
		{"a", "a", 0, true},
		{"abc", "abc", 0, true},
		{"kitten", "sitting", 3, false}, // 3 edits > MAX_DISTANCE(2) → ok=false
		{"kubernetes", "kubrnetes", 1, true},
		{"kubernetes", "kubrnets", 2, true},
		{"abc", "xyz", 3, false},       // 3 edits → ok=false
		{"hello", "helo", 1, true},
		{"deploy", "depoly", 2, true},  // transposition costs 2 edits
	}
	for _, c := range cases {
		dist, ok := Levenshtein(c.a, c.b)
		if ok != c.withinN {
			t.Errorf("Levenshtein(%q,%q) ok=%v, want %v", c.a, c.b, ok, c.withinN)
		}
		if ok && dist != c.want {
			t.Errorf("Levenshtein(%q,%q) dist=%d, want %d", c.a, c.b, dist, c.want)
		}
	}
}

func TestLevenshteinSymmetric(t *testing.T) {
	pairs := [][2]string{
		{"kubernetes", "kubrnetes"},
		{"hello", "helo"},
		{"abc", "ab"},
	}
	for _, p := range pairs {
		d1, ok1 := Levenshtein(p[0], p[1])
		d2, ok2 := Levenshtein(p[1], p[0])
		if ok1 != ok2 || (ok1 && d1 != d2) {
			t.Errorf("Levenshtein not symmetric for (%q, %q): (%d,%v) vs (%d,%v)",
				p[0], p[1], d1, ok1, d2, ok2)
		}
	}
}

// ─── BKTree ───────────────────────────────────────────────────────────────────

func TestBKTreeAddAndSearch(t *testing.T) {
	tree := NewBKTree()
	words := []string{"kubernetes", "deploy", "cluster", "network", "service"}
	for _, w := range words {
		tree.Add(w)
	}
	if tree.Size() != len(words) {
		t.Errorf("size = %d, want %d", tree.Size(), len(words))
	}

	// Exact match
	results := tree.Search("kubernetes", 0)
	if len(results) != 1 || results[0].Word != "kubernetes" {
		t.Errorf("exact match failed: %v", results)
	}

	// One-edit typo
	results = tree.Search("kubrnetes", 1)
	found := false
	for _, r := range results {
		if r.Word == "kubernetes" && r.Distance == 1 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'kubernetes' with distance 1 in results %v", results)
	}
}

func TestBKTreeNoDuplicates(t *testing.T) {
	tree := NewBKTree()
	for i := 0; i < 5; i++ {
		tree.Add("same")
	}
	if tree.Size() != 1 {
		t.Errorf("expected size 1 after 5 inserts of same word, got %d", tree.Size())
	}
}

func TestBKTreeEmptySearch(t *testing.T) {
	tree := NewBKTree()
	results := tree.Search("anything", 2)
	if results != nil {
		t.Errorf("expected nil from empty tree, got %v", results)
	}
}

func TestBKTreeSearchReturnsSortedByDistance(t *testing.T) {
	tree := NewBKTree()
	// "kub" is 7 edits from "kubernetes"; "kubernetx" is 1 edit
	for _, w := range []string{"kubernetes", "kubernetx", "kubernets"} {
		tree.Add(w)
	}
	results := tree.Search("kubernetes", 2)
	for i := 1; i < len(results); i++ {
		if results[i-1].Distance > results[i].Distance {
			t.Errorf("results not sorted by distance: %v", results)
		}
	}
}

// ─── FuzzySearcher ────────────────────────────────────────────────────────────

func TestFuzzySearcher(t *testing.T) {
	fs := NewFuzzySearcher()
	fs.Build([]string{"kubernetes", "cluster", "network", "service", "deploy"})

	// Typo within MAX_DISTANCE
	matches := fs.Search("kubrnetes", MAX_DISTANCE)
	found := false
	for _, m := range matches {
		if m.Word == "kubernetes" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'kubernetes' in fuzzy results for 'kubrnetes', got %v", matches)
	}

	// Query too far away — should not match
	matches = fs.Search("zzzzzzz", MAX_DISTANCE)
	for _, m := range matches {
		if m.Word == "kubernetes" {
			t.Errorf("unexpected match 'kubernetes' for totally different query")
		}
	}
}

func TestFuzzySearcherIncrementalAdd(t *testing.T) {
	fs := NewFuzzySearcher()
	fs.Add("deploy")
	if fs.Size() != 1 {
		t.Errorf("size after single add = %d, want 1", fs.Size())
	}
	fs.Add("deploy") // duplicate
	if fs.Size() != 1 {
		t.Errorf("size after duplicate add = %d, want 1", fs.Size())
	}
	fs.Add("service")
	if fs.Size() != 2 {
		t.Errorf("size after second unique add = %d, want 2", fs.Size())
	}
}
