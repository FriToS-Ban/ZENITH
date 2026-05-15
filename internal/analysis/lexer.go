package analysis

import (
	"strings"
	"unicode"
)

// Lexer tokenizes text the same way seroost does:
//   - skip whitespace
//   - numeric runs  → token as-is
//   - alphabetic runs → lowercased → Porter stemmed via ZENITH's Stemmer
//   - single punctuation chars → token as-is (filtered by CollectAlpha)
//
// It uses ZENITH's existing Stemmer (stemmer.go) — NOT a separate Snowball
// implementation. The seroost Snowball stemmer is functionally equivalent to
// ZENITH's Porter implementation for English text.
//
// Usage — iterate manually:
//
//	l := NewLexer("Kubernetes deployments are running")
//	for l.HasNext() {
//	    fmt.Println(l.Next())  // "kubernet", "deploy", "run"
//	}
//
// Usage — collect all:
//
//	tokens := NewLexer(text).CollectAlpha()
type Lexer struct {
	runes   []rune
	pos     int
	stemmer *Stemmer // ZENITH's Porter stemmer from stemmer.go
}

// NewLexer creates a Lexer over text. Allocates a Stemmer once per Lexer.
func NewLexer(text string) *Lexer {
	return &Lexer{
		runes:   []rune(text),
		pos:     0,
		stemmer: New(), // analysis.New() from stemmer.go
	}
}

// HasNext returns true if there are more tokens remaining.
func (l *Lexer) HasNext() bool {
	l.trimLeft()
	return l.pos < len(l.runes)
}

// Next returns the next token.
// Alphabetic tokens are lowercased and Porter-stemmed.
// Numeric tokens are returned as-is.
// Single punctuation characters are returned as-is.
// Returns "" when exhausted — use HasNext() to guard.
func (l *Lexer) Next() string {
	l.trimLeft()
	if l.pos >= len(l.runes) {
		return ""
	}

	ch := l.runes[l.pos]

	// numeric run — return as-is, no stemming
	if unicode.IsNumber(ch) {
		return l.chopWhile(unicode.IsNumber)
	}

	// alphabetic run — lowercase + stem (mirrors seroost's next_token)
	if unicode.IsLetter(ch) {
		raw := l.chopWhile(func(r rune) bool {
			return unicode.IsLetter(r) || unicode.IsNumber(r)
		})
		lower := strings.ToLower(raw)
		return l.stemmer.Stem(lower)
	}

	// single punctuation / symbol — advance and return
	l.pos++
	return string(ch)
}

// Collect drains the Lexer and returns every token including punctuation.
// Prefer CollectAlpha for search indexing.
func (l *Lexer) Collect() []string {
	var tokens []string
	for l.HasNext() {
		tok := l.Next()
		if tok != "" {
			tokens = append(tokens, tok)
		}
	}
	return tokens
}

// CollectAlpha drains the Lexer and returns only meaningful tokens:
//   - length > 1, OR a single alphabetic character
//   - no lone punctuation characters
//
// This is what TFIDFIndexer and BM25Scorer consume.
func (l *Lexer) CollectAlpha() []string {
	var tokens []string
	for l.HasNext() {
		tok := l.Next()
		if tok == "" {
			continue
		}
		runes := []rune(tok)
		if len(runes) > 1 || (len(runes) == 1 && unicode.IsLetter(runes[0])) {
			tokens = append(tokens, tok)
		}
	}
	return tokens
}

// trimLeft advances pos past any leading whitespace.
func (l *Lexer) trimLeft() {
	for l.pos < len(l.runes) && unicode.IsSpace(l.runes[l.pos]) {
		l.pos++
	}
}

// chopWhile advances pos while predicate is true and returns the consumed
// slice as a string.
func (l *Lexer) chopWhile(pred func(rune) bool) string {
	start := l.pos
	for l.pos < len(l.runes) && pred(l.runes[l.pos]) {
		l.pos++
	}
	return string(l.runes[start:l.pos])
}

// TokenizeString is a convenience wrapper — tokenizes text and returns
// all meaningful alpha tokens in a single call.
// Used by TFIDFIndexer, BM25Scorer, and the query pipeline.
func TokenizeString(text string) []string {
	return NewLexer(text).CollectAlpha()
}
