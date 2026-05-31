package index

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/shramanb113/ZENITH/internal/analysis"
	"github.com/shramanb113/ZENITH/internal/config"
	"github.com/shramanb113/ZENITH/internal/embedding"
	"github.com/shramanb113/ZENITH/internal/ranking"
)

// newTestEngine returns a minimal Engine backed by a deterministic embedder.
// No FST path is set — keeps tests portable (no disk I/O for the FST).
func newTestEngine() *Engine {
	cfg := config.DefaultConfig()
	emb := embedding.NewDeterministicEmbedder(384)
	scorer := ranking.NewRRFRanker(0, 0)
	ana := analysis.NewStandardAnalyzer()
	return NewEngine(cfg, emb, scorer, ana)
}

// ─── generateEdgeNgrams ───────────────────────────────────────────────────────

func TestGenerateEdgeNgrams_ShortToken(t *testing.T) {
	// Token shorter than MinGram (3): should return just the token itself.
	got := generateEdgeNgrams("ab")
	if len(got) != 1 || got[0] != "ab" {
		t.Errorf("short token: got %v, want [ab]", got)
	}
}

func TestGenerateEdgeNgrams_ExactMinGram(t *testing.T) {
	// Exactly 3 chars: full token + no prefixes (MinGram..MinGram range is empty).
	got := generateEdgeNgrams("abc")
	if len(got) != 1 || got[0] != "abc" {
		t.Errorf("3-char token: got %v, want [abc]", got)
	}
}

func TestGenerateEdgeNgrams_LongToken(t *testing.T) {
	token := "kubernetes"
	got := generateEdgeNgrams(token)

	// Full token must always be present.
	if !slices.Contains(got, token) {
		t.Errorf("full token %q missing from ngrams %v", token, got)
	}

	// "kub" (len 3) must be present.
	if !slices.Contains(got, "kub") {
		t.Errorf("prefix 'kub' missing from ngrams %v", got)
	}

	// No duplicates.
	seen := make(map[string]bool)
	for _, ng := range got {
		if seen[ng] {
			t.Errorf("duplicate ngram %q in %v", ng, got)
		}
		seen[ng] = true
	}
}

func TestGenerateEdgeNgrams_NoDuplicateFullToken(t *testing.T) {
	// Original bug: the full token was appended twice when n > MaxGram.
	// "superlongword" has 13 chars > MaxGram(10).
	got := generateEdgeNgrams("superlongword")
	count := 0
	for _, ng := range got {
		if ng == "superlongword" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("full token appears %d times in ngrams (want exactly 1): %v", count, got)
	}
}

// ─── Add / Search ─────────────────────────────────────────────────────────────

func TestEngine_AddAndSearch_BasicMatch(t *testing.T) {
	e := newTestEngine()
	ctx := context.Background()

	if err := e.Add(ctx, "doc1", "kubernetes cluster deployment pod service"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := e.Add(ctx, "doc2", "docker container image registry"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	results, err := e.Search(ctx, "kubernetes deployment")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected non-empty search results")
	}
	if results[0].ID != "doc1" {
		t.Errorf("expected doc1 first, got %q (score=%.4f)", results[0].ID, results[0].Score)
	}
}

func TestEngine_AddAndSearch_FuzzyMatch(t *testing.T) {
	e := newTestEngine()
	ctx := context.Background()

	if err := e.Add(ctx, "doc1", "kubernetes cluster deployment"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// "kubrnetes" is 1 edit away from "kubernetes"
	results, err := e.Search(ctx, "kubrnetes")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	found := false
	for _, r := range results {
		if r.ID == "doc1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("fuzzy search for 'kubrnetes' should match doc1; got %v", results)
	}
}

func TestEngine_AddAndSearch_PhoneticMatch(t *testing.T) {
	e := newTestEngine()
	ctx := context.Background()

	if err := e.Add(ctx, "doc1", "Robert Smith engineer"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// "Rupert" has the same Soundex code as "Robert"
	results, err := e.Search(ctx, "Rupert")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	found := false
	for _, r := range results {
		if r.ID == "doc1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("phonetic search for 'Rupert' should match doc with 'Robert'; got %v", results)
	}
}

func TestEngine_AddIdempotent(t *testing.T) {
	e := newTestEngine()
	ctx := context.Background()

	// Index the same doc twice with different content — second should win.
	if err := e.Add(ctx, "doc1", "kubernetes cluster"); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if err := e.Add(ctx, "doc1", "docker container image"); err != nil {
		t.Fatalf("second Add: %v", err)
	}

	// Should match new content.
	results, err := e.Search(ctx, "docker container")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	found := false
	for _, r := range results {
		if r.ID == "doc1" {
			found = true
		}
	}
	if !found {
		t.Error("re-indexed doc1 should match new content 'docker container'")
	}
}

func TestEngine_Search_NoResults(t *testing.T) {
	e := newTestEngine()
	ctx := context.Background()

	if err := e.Add(ctx, "doc1", "kubernetes cluster"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Query for something completely unrelated and not fuzzy-close to anything indexed.
	results, err := e.Search(ctx, "zzzzzzzzz")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// May return no results or very low scores — either is acceptable.
	for _, r := range results {
		if r.Score > 0.5 {
			t.Errorf("unexpectedly high score %.4f for unrelated query (doc=%q)", r.Score, r.ID)
		}
	}
}

func TestEngine_Search_MultipleDocumentRanking(t *testing.T) {
	e := newTestEngine()
	ctx := context.Background()

	// doc_relevant has 3 occurrences of search terms; doc_partial has 1.
	docs := map[string]string{
		"doc_relevant": "search engine search ranking search algorithm",
		"doc_partial":  "database storage index",
		"doc_unrelated": "cooking recipe pasta tomato sauce",
	}
	for id, content := range docs {
		if err := e.Add(ctx, id, content); err != nil {
			t.Fatalf("Add(%q): %v", id, err)
		}
	}

	results, err := e.Search(ctx, "search ranking")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results")
	}
	if results[0].ID != "doc_relevant" {
		t.Errorf("expected doc_relevant first, got %q", results[0].ID)
	}
}

func TestEngine_FSTContains_AfterAdd(t *testing.T) {
	e := newTestEngine()
	ctx := context.Background()

	if err := e.Add(ctx, "doc1", "kubernetes deployment cluster"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Stemmed terms should be in the FST.
	// "kubernetes" is stemmed to "kubernet" by Porter2 (approximately).
	// We check that the FST was rebuilt and contains some indexed term.
	// Use FSTPrefixSearch for robustness.
	terms, err := e.FSTPrefixSearch("kub", 10)
	if err != nil {
		t.Fatalf("FSTPrefixSearch: %v", err)
	}
	if len(terms) == 0 {
		t.Error("expected FST to return terms with prefix 'kub' after indexing 'kubernetes'")
	}
}

// ─── Save / Load round-trip ───────────────────────────────────────────────────

func TestEngine_SaveLoad_RoundTrip(t *testing.T) {
	e := newTestEngine()
	ctx := context.Background()

	docs := []struct{ id, text string }{
		{"alpha", "kubernetes cluster pod service"},
		{"beta", "docker container image registry"},
		{"gamma", "search index ranking score"},
	}
	for _, d := range docs {
		if err := e.Add(ctx, d.id, d.text); err != nil {
			t.Fatalf("Add(%q): %v", d.id, err)
		}
	}

	path := filepath.Join(t.TempDir(), "zenith.db")
	if err := e.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load into fresh engine.
	e2 := newTestEngine()
	if err := e2.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Search must work on the loaded engine.
	results, err := e2.Search(ctx, "kubernetes cluster")
	if err != nil {
		t.Fatalf("Search after Load: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results from loaded engine")
	}
	if results[0].ID != "alpha" {
		t.Errorf("expected 'alpha' first after load, got %q", results[0].ID)
	}
}

func TestEngine_Load_MissingFile(t *testing.T) {
	e := newTestEngine()
	err := e.Load(filepath.Join(t.TempDir(), "no_such_file.db"))
	if err == nil {
		t.Error("expected error when loading non-existent file")
	}
}

func TestEngine_Save_CreatesFile(t *testing.T) {
	e := newTestEngine()
	ctx := context.Background()
	_ = e.Add(ctx, "doc1", "hello world")

	path := filepath.Join(t.TempDir(), "out.db")
	if err := e.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("Save did not create file: %v", err)
	}
}

// ─── removeID ─────────────────────────────────────────────────────────────────

func TestRemoveID(t *testing.T) {
	cases := []struct {
		ids    []uint32
		target uint32
		want   []uint32
	}{
		{[]uint32{1, 2, 3}, 2, []uint32{1, 3}},
		{[]uint32{1, 2, 3}, 1, []uint32{2, 3}},
		{[]uint32{1, 2, 3}, 3, []uint32{1, 2}},
		{[]uint32{1, 2, 3}, 9, []uint32{1, 2, 3}}, // not found
		{[]uint32{}, 1, []uint32{}},
		{nil, 1, nil},
	}
	for _, c := range cases {
		got := removeID(c.ids, c.target)
		if len(got) != len(c.want) {
			t.Errorf("removeID(%v, %d) = %v, want %v", c.ids, c.target, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("removeID(%v, %d)[%d] = %d, want %d", c.ids, c.target, i, got[i], c.want[i])
			}
		}
	}
}
