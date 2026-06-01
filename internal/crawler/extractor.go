package crawler

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
)

// ExtractText reads the file at path and returns its indexable text content.
// Extraction is dispatched by file extension:
//
//	.txt .md .log .csv .json .yaml .yml  → raw UTF-8 content
//	.go                                  → package name, type/func/var identifiers, comments
//	.py .ts .js .jsx .tsx .rs .java .c .cpp .h → raw source (tokeniser handles the rest)
//	.html .htm                           → tag-stripped visible text
//	.pdf                                 → unsupported (returns ErrUnsupported)
//
// Unknown extensions fall back to raw content.
func ExtractText(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return extractGo(path)
	case ".html", ".htm":
		return extractHTML(path)
	case ".pdf":
		return "", fmt.Errorf("%w: .pdf requires an external PDF library", ErrUnsupported)
	default:
		return extractRaw(path)
	}
}

// ErrUnsupported is returned for file types that cannot be extracted.
var ErrUnsupported = fmt.Errorf("extractor: unsupported file type")

// SupportedExt reports whether ext is a known indexable file extension.
// ext must include the leading dot (e.g. ".go").
// Only extensions whose content can meaningfully be full-text-indexed are
// accepted; binary/media formats are rejected so that Remove events for image,
// archive, or other binary files are silently ignored by the Watcher.
func SupportedExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".txt", ".md", ".log", ".csv",
		".json", ".yaml", ".yml",
		".go",
		".py", ".ts", ".js", ".jsx", ".tsx", ".rs", ".java", ".c", ".cpp", ".h",
		".html", ".htm":
		return true
	default:
		return false
	}
}

// ─── Implementations ─────────────────────────────────────────────────────────

// extractRaw reads the entire file as UTF-8 text.
// Used for .txt, .md, .log, .csv, .json, .yaml, .yml, .py, .ts, .js, and any
// unknown extension. The analysis pipeline tokenises the result.
func extractRaw(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("extractor: read %s: %w", path, err)
	}
	return string(data), nil
}

// extractGo uses the Go AST to extract the most search-relevant parts of a
// source file: package declaration, exported and unexported identifiers
// (types, functions, methods, variables, constants), and all comments.
// This produces much higher quality index content than raw source because it
// omits noise (punctuation, keywords) while preserving semantic tokens.
func extractGo(path string) (string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		// Fall back to raw source on parse error (e.g. build-constrained files).
		return extractRaw(path)
	}

	var sb strings.Builder

	// Package name.
	sb.WriteString(f.Name.Name)
	sb.WriteByte('\n')

	// Doc comments on the file itself.
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			text := strings.TrimPrefix(c.Text, "//")
			text = strings.TrimPrefix(text, "/*")
			text = strings.TrimSuffix(text, "*/")
			sb.WriteString(strings.TrimSpace(text))
			sb.WriteByte('\n')
		}
	}

	// Walk declarations to collect identifiers.
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Ident:
			if v.Name != "_" {
				sb.WriteString(v.Name)
				sb.WriteByte(' ')
			}
		}
		return true
	})

	return sb.String(), nil
}

// extractHTML strips HTML tags and returns the visible text content.
// Uses golang.org/x/net/html which is already a transitive dependency via gRPC.
func extractHTML(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("extractor: read %s: %w", path, err)
	}

	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		// Fall back to raw if parse fails.
		return string(data), nil
	}

	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				sb.WriteString(text)
				sb.WriteByte('\n')
			}
		}
		// Skip <script> and <style> subtrees — they're code/CSS, not content.
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	return sb.String(), nil
}
