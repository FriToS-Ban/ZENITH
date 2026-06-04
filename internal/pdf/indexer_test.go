package pdf

import (
	"strings"
	"testing"
)

func TestSplitChunks_Basic(t *testing.T) {
	words := make([]string, 400)
	for i := range words {
		words[i] = "word"
	}
	text := strings.Join(words, " ")
	chunks := splitChunks(text)

	if len(strings.Fields(chunks[0])) != 300 {
		t.Errorf("first chunk: got %d words, want 300", len(strings.Fields(chunks[0])))
	}
	if len(chunks) < 2 {
		t.Fatal("expected at least 2 chunks for 400 words")
	}
}

func TestSplitChunks_ShortText(t *testing.T) {
	chunks := splitChunks("hello world")
	if len(chunks) != 1 {
		t.Errorf("short text: got %d chunks, want 1", len(chunks))
	}
	if chunks[0] != "hello world" {
		t.Errorf("short text chunk: got %q, want %q", chunks[0], "hello world")
	}
}

func TestSplitChunks_EmptyText(t *testing.T) {
	chunks := splitChunks("   ")
	if len(chunks) != 0 {
		t.Errorf("empty text: got %d chunks, want 0", len(chunks))
	}
}
