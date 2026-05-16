package analysis

import (
	"regexp"
	"strings"

	"github.com/kljensen/snowball"
)

// TokenType classifies what kind of token was produced.
type TokenType int

const (
	WORD     TokenType = iota
	NGRAM              // produced by edge n-gram expansion
	PHONETIC           // produced by Soundex/Metaphone
)

// Token is a single unit of analysis with position and type metadata.
type Token struct {
	Term     string
	Position int
	Type     TokenType
}

// Analyzer is the interface the Engine depends on.
// Any struct implementing Analyze can be injected as the Engine's analyzer.
type Analyzer interface {
	Analyze(text string) []Token
}

// StandardAnalyzer is the default analysis pipeline:
//  1. Regex tokenisation (camelCase-aware, alphanumeric)
//  2. Lowercasing
//  3. Stop-word removal
//  4. Snowball Porter2 stemming (kljensen/snowball, english)
//
// The internal *Stemmer field has been removed — snowball.Stem is a
// stateless function call, no struct needed, one less allocation per token.
type StandardAnalyzer struct {
	stopWords  map[string]struct{}
	tokenRegex *regexp.Regexp // compiled once at init
}

// NewStandardAnalyzer constructs a StandardAnalyzer with a full English
// stop-word list and a camelCase-aware token regex.
func NewStandardAnalyzer() *StandardAnalyzer {
	stopList := []string{
		"a", "about", "above", "after", "again", "against", "all", "am",
		"an", "and", "any", "are", "as", "at", "be", "because", "been",
		"before", "being", "below", "between", "both", "but", "by", "can",
		"did", "do", "does", "doing", "don", "down", "during", "each",
		"few", "for", "from", "further", "had", "has", "have", "having",
		"he", "her", "here", "hers", "herself", "him", "himself", "his",
		"how", "i", "if", "in", "into", "is", "it", "its", "itself",
		"just", "me", "more", "most", "my", "myself", "no", "nor", "not",
		"now", "of", "off", "on", "once", "only", "or", "other", "our",
		"ours", "ourselves", "out", "over", "own", "s", "same", "she",
		"should", "so", "some", "such", "t", "than", "that", "the",
		"their", "theirs", "them", "themselves", "then", "there", "these",
		"they", "this", "those", "through", "to", "too", "under", "until",
		"up", "very", "was", "we", "were", "what", "when", "where",
		"which", "while", "who", "whom", "why", "will", "with", "you",
		"your", "yours", "yourself", "yourselves",
	}

	stopMap := make(map[string]struct{}, len(stopList))
	for _, s := range stopList {
		stopMap[s] = struct{}{}
	}

	return &StandardAnalyzer{
		stopWords: stopMap,
		// Handles camelCase, PascalCase, acronyms, lowercase alphanumeric.
		// AP-1: compiled once — not per call.
		tokenRegex: regexp.MustCompile(`[A-Z][a-z0-9]*|[a-z0-9]+|[A-Z]+`),
	}
}

// stem calls kljensen/snowball's English Porter2 stemmer.
// Returns the original word unchanged on error (graceful degradation).
func stem(word string) string {
	s, err := snowball.Stem(word, "english", true)
	if err != nil {
		return word
	}
	return s
}

// Tokenize returns stemmed, filtered string tokens from text.
// Used directly by the BM25/TF-IDF scorers and the BKTree build path.
// Single pass: regex → lowercase → stop-word filter → stem.
func (a *StandardAnalyzer) Tokenize(text string) []string {
	raw := a.tokenRegex.FindAllString(text, -1)
	out := make([]string, 0, len(raw))
	for _, tok := range raw {
		tok = strings.ToLower(tok)
		if _, stop := a.stopWords[tok]; stop {
			continue
		}
		if s := stem(tok); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Analyze implements the Analyzer interface.
// Returns structured Tokens with position and type metadata.
// Internally calls Tokenize — one codepath, no duplication.
func (a *StandardAnalyzer) Analyze(text string) []Token {
	terms := a.Tokenize(text)
	tokens := make([]Token, len(terms))
	for i, t := range terms {
		tokens[i] = Token{
			Term:     t,
			Position: i,
			Type:     WORD,
		}
	}
	return tokens
}
