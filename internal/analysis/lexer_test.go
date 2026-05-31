package analysis

import (
	"slices"
	"testing"
)

func TestTokenizeString(t *testing.T) {
	cases := []struct {
		input string
		want  []string // stemmed tokens expected to be present
	}{
		{
			"kubernetes deployment cluster",
			[]string{"kubernet", "deploy", "cluster"},
		},
		{
			"running faster",
			[]string{"run", "faster"}, // "run" from stemming "running"
		},
		{
			"", // empty
			nil,
		},
		{
			"123 numbers 456",
			[]string{"123", "456"}, // numbers kept as-is
		},
	}
	for _, c := range cases {
		got := TokenizeString(c.input)
		for _, want := range c.want {
			if !slices.Contains(got, want) {
				t.Errorf("TokenizeString(%q): expected token %q in %v", c.input, want, got)
			}
		}
	}
}

func TestLexerCollectAlpha(t *testing.T) {
	// Punctuation-only tokens must be filtered.
	l := NewLexer("hello, world! foo.")
	tokens := l.CollectAlpha()
	for _, tok := range tokens {
		if tok == "," || tok == "!" || tok == "." {
			t.Errorf("CollectAlpha returned punctuation token %q", tok)
		}
	}
	if len(tokens) == 0 {
		t.Error("CollectAlpha returned empty slice for non-empty input")
	}
}

func TestLexerStemsTokens(t *testing.T) {
	cases := []struct {
		word   string
		expect string // expected stem
	}{
		{"running", "run"},
		{"indexed", "index"},
		{"searching", "search"},
		{"clustering", "cluster"},
	}
	for _, c := range cases {
		l := NewLexer(c.word)
		tokens := l.CollectAlpha()
		if len(tokens) == 0 {
			t.Errorf("no tokens for %q", c.word)
			continue
		}
		if tokens[0] != c.expect {
			t.Errorf("stem(%q) = %q, want %q", c.word, tokens[0], c.expect)
		}
	}
}

func TestLexerNumbers(t *testing.T) {
	l := NewLexer("error 404 not found")
	tokens := l.CollectAlpha()
	hasNumber := false
	for _, tok := range tokens {
		if tok == "404" {
			hasNumber = true
		}
	}
	if !hasNumber {
		t.Error("expected numeric token '404' in output")
	}
}

func TestLexerEmptyInput(t *testing.T) {
	l := NewLexer("")
	tokens := l.CollectAlpha()
	if len(tokens) != 0 {
		t.Errorf("expected empty result for empty input, got %v", tokens)
	}
}
