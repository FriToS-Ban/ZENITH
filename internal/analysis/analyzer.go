package analysis

import (
	"regexp"
	"strings"
)

// API 2: Analyzer pipeline definition
type TokenType int

const (
	WORD TokenType = iota
	NGRAM
	PHONETIC
)

type Token struct {
	Term     string
	Position int
	Type     TokenType
}

type Analyzer interface {
	Analyze(text string) []Token
}

type StandardAnalyzer struct {
	stopWords map[string]struct{}
	stemmer   *Stemmer
	// AP-1: Compiled once at struct initialization
	tokenRegex *regexp.Regexp
}

func NewStandardAnalyzer() *StandardAnalyzer {
	stopList := []string{
		"a", "about", "above", "after", "again", "against", "all", "am", "an", "and", "any", "are", "as", "at", "be", "because", "been", "before", "being", "below", "between", "both", "but", "by", "can", "did", "do", "does", "doing", "don", "down", "during", "each", "few", "for", "from", "further", "had", "has", "have", "having", "he", "her", "here", "hers", "herself", "him", "himself", "his", "how", "i", "if", "in", "into", "is", "it", "its", "itself", "just", "me", "more", "most", "my", "myself", "no", "nor", "not", "now", "of", "off", "on", "once", "only", "or", "other", "our", "ours", "ourselves", "out", "over", "own", "s", "same", "she", "should", "so", "some", "such", "t", "than", "that", "the", "their", "theirs", "them", "themselves", "then", "there", "these", "they", "this", "those", "through", "to", "too", "under", "until", "up", "very", "was", "we", "were", "what", "when", "where", "which", "while", "who", "whom", "why", "will", "with", "you", "your", "yours", "yourself", "yourselves",
	}

	stopMap := make(map[string]struct{})
	for _, s := range stopList {
		stopMap[s] = struct{}{}
	}

	return &StandardAnalyzer{
		stopWords:  stopMap,
		stemmer:    New(),
		tokenRegex: regexp.MustCompile(`[A-Z][a-z0-9]*|[a-z0-9]+|[A-Z]+`), // AP-1 fix
	}
}

// Tokenize to string array (retaining original method signature essentially for compatibility where needed)
func (a *StandardAnalyzer) Tokenize(text string) []string {
	rawTokens := a.tokenRegex.FindAllString(text, -1)

	var filtered []string

	for _, token := range rawTokens {
		token = strings.ToLower(token)

		if _, ok := a.stopWords[token]; ok {
			continue
		}

		stemmed := a.stemmer.Stem(token)
		if stemmed != "" {
			filtered = append(filtered, stemmed)
		}
	}
	return filtered
}

// Analyze returns structured Tokens
func (a *StandardAnalyzer) Analyze(text string) []Token {
	stringTokens := a.Tokenize(text)
	var tokens []Token
	for i, t := range stringTokens {
		tokens = append(tokens, Token{
			Term:     t,
			Position: i,
			Type:     WORD,
		})
	}
	return tokens
}
