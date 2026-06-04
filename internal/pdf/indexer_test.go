package pdf

import (
	"strings"
	"testing"
)

// ── splitChunks — basic behaviour ────────────────────────────────────────────

func TestSplitChunks_Empty(t *testing.T) {
	if chunks := splitChunks(""); len(chunks) != 0 {
		t.Errorf("got %d chunks, want 0", len(chunks))
	}
}

func TestSplitChunks_WhitespaceOnly(t *testing.T) {
	if chunks := splitChunks("   \t\n  "); len(chunks) != 0 {
		t.Errorf("got %d chunks, want 0 for whitespace-only input", len(chunks))
	}
}

func TestSplitChunks_ShortText(t *testing.T) {
	chunks := splitChunks("hello world")
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0] != "hello world" {
		t.Errorf("chunk[0]=%q, want %q", chunks[0], "hello world")
	}
}

func TestSplitChunks_SingleWord(t *testing.T) {
	chunks := splitChunks("hello")
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0] != "hello" {
		t.Errorf("chunk[0]=%q, want %q", chunks[0], "hello")
	}
}

// ── chunk size ───────────────────────────────────────────────────────────────

func TestSplitChunks_ExactlyChunkWords(t *testing.T) {
	// 300 words → exactly one chunk of 300 words.
	text := strings.Join(make300Words(), " ")
	chunks := splitChunks(text)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 for exactly %d words", len(chunks), chunkWords)
	}
	if len(strings.Fields(chunks[0])) != chunkWords {
		t.Errorf("chunk word count = %d, want %d", len(strings.Fields(chunks[0])), chunkWords)
	}
}

func TestSplitChunks_OneWordOverChunkSize(t *testing.T) {
	// 301 words → two chunks (second chunk has 301 - (300-50) = 51 words).
	words := append(make300Words(), "extra")
	text := strings.Join(words, " ")
	chunks := splitChunks(text)
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2 for %d words", len(chunks), len(words))
	}
	if len(strings.Fields(chunks[0])) != chunkWords {
		t.Errorf("first chunk has %d words, want %d", len(strings.Fields(chunks[0])), chunkWords)
	}
}

func TestSplitChunks_400Words(t *testing.T) {
	words := makeNWords(400)
	text := strings.Join(words, " ")
	chunks := splitChunks(text)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want ≥2 for 400 words", len(chunks))
	}
	if len(strings.Fields(chunks[0])) != chunkWords {
		t.Errorf("first chunk has %d words, want %d", len(strings.Fields(chunks[0])), chunkWords)
	}
}

// ── overlap ──────────────────────────────────────────────────────────────────

func TestSplitChunks_OverlapIsCorrect(t *testing.T) {
	// 350 words → chunk[0] covers words 0-299, chunk[1] covers words 250-349.
	words := makeNWords(350)
	text := strings.Join(words, " ")
	chunks := splitChunks(text)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want ≥2 for 350 words", len(chunks))
	}
	// The second chunk should start at word index chunkWords-chunkOverlap = 250.
	chunk1Words := strings.Fields(chunks[1])
	want := words[chunkWords-chunkOverlap]
	got := chunk1Words[0]
	if got != want {
		t.Errorf("chunk[1] starts with %q, want %q (word at index %d)",
			got, want, chunkWords-chunkOverlap)
	}
}

func TestSplitChunks_NoLostWords(t *testing.T) {
	// Every word must appear in at least one chunk.
	words := makeNWords(500)
	text := strings.Join(words, " ")
	chunks := splitChunks(text)

	// The last chunk must end at the last word.
	lastChunkWords := strings.Fields(chunks[len(chunks)-1])
	if lastChunkWords[len(lastChunkWords)-1] != words[len(words)-1] {
		t.Errorf("last word of last chunk = %q, want %q",
			lastChunkWords[len(lastChunkWords)-1], words[len(words)-1])
	}
}

func TestSplitChunks_FirstWordInFirstChunk(t *testing.T) {
	words := makeNWords(400)
	text := strings.Join(words, " ")
	chunks := splitChunks(text)
	firstChunkWords := strings.Fields(chunks[0])
	if firstChunkWords[0] != words[0] {
		t.Errorf("first word of first chunk = %q, want %q", firstChunkWords[0], words[0])
	}
}

// ── chunk content ─────────────────────────────────────────────────────────────

func TestSplitChunks_ChunksAreNonEmpty(t *testing.T) {
	for _, n := range []int{1, 50, 300, 301, 600, 1000} {
		words := makeNWords(n)
		text := strings.Join(words, " ")
		for i, c := range splitChunks(text) {
			if strings.TrimSpace(c) == "" {
				t.Errorf("chunk[%d] is empty for %d-word input", i, n)
			}
		}
	}
}

func TestSplitChunks_ExtraWhitespace(t *testing.T) {
	// Extra spaces between words should not affect chunking.
	text := "word1  word2   word3    word4"
	chunks := splitChunks(text)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	// strings.Fields normalises whitespace.
	words := strings.Fields(chunks[0])
	if len(words) != 4 {
		t.Errorf("got %d words, want 4", len(words))
	}
}

func TestSplitChunks_NewlinesAndTabs(t *testing.T) {
	text := "word1\nword2\tword3\r\nword4"
	chunks := splitChunks(text)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	words := strings.Fields(chunks[0])
	if len(words) != 4 {
		t.Errorf("got %d words, want 4", len(words))
	}
}

// ── large inputs ─────────────────────────────────────────────────────────────

func TestSplitChunks_LargeDocument(t *testing.T) {
	// 10 000 words — verify no panic and reasonable chunk count.
	words := makeNWords(10000)
	text := strings.Join(words, " ")
	chunks := splitChunks(text)
	// With stride = chunkWords-chunkOverlap = 250:
	// expected ≈ ceil((10000-300)/250) + 1 = 39 chunks
	if len(chunks) < 30 || len(chunks) > 50 {
		t.Errorf("got %d chunks for 10000 words, expected ~39", len(chunks))
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func make300Words() []string { return makeNWords(chunkWords) }

func makeNWords(n int) []string {
	words := make([]string, n)
	for i := range words {
		words[i] = strings.Repeat("w", (i%8)+1) // vary word lengths
	}
	return words
}
