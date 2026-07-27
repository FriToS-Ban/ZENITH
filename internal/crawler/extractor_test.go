package crawler

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── SupportedExt ─────────────────────────────────────────────────────────────

func TestSupportedExt(t *testing.T) {
	supported := []string{
		".txt", ".md", ".go", ".py", ".html", ".htm", ".json", ".yaml", ".log",
		".pdf",
		".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp", ".tiff", ".tif",
	}
	for _, ext := range supported {
		if !SupportedExt(ext) {
			t.Errorf("SupportedExt(%q) = false, want true", ext)
		}
	}
	unsupported := []string{".exe", ".zip", ".mp4", ".docx"}
	for _, ext := range unsupported {
		if SupportedExt(ext) {
			t.Errorf("SupportedExt(%q) = true, want false", ext)
		}
	}
}

// ─── Raw text extraction ─────────────────────────────────────────────────────

func TestExtractText_PlainText(t *testing.T) {
	content := "hello world this is a test file"
	f := writeTempFile(t, "test.txt", content)

	got, err := ExtractText(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != content {
		t.Errorf("ExtractText(.txt) = %q, want %q", got, content)
	}
}

func TestExtractText_Markdown(t *testing.T) {
	content := "# Heading\n\nSome paragraph text."
	f := writeTempFile(t, "test.md", content)

	got, err := ExtractText(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "Heading") || !strings.Contains(got, "paragraph") {
		t.Errorf("markdown extraction lost content: %q", got)
	}
}

// ─── Go AST extraction ────────────────────────────────────────────────────────

func TestExtractText_Go(t *testing.T) {
	src := `package foo

// ComputeSum adds two integers.
func ComputeSum(a, b int) int {
	return a + b
}
`
	f := writeTempFile(t, "test.go", src)
	got, err := ExtractText(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"ComputeSum", "foo", "adds two integers"} {
		if !strings.Contains(got, want) {
			t.Errorf("Go extraction missing %q in output: %q", want, got)
		}
	}
}

func TestExtractText_Go_FallbackOnParseError(t *testing.T) {
	// Syntactically invalid Go should fall back to raw extraction without error.
	bad := `package ??? this is not valid go`
	f := writeTempFile(t, "broken.go", bad)
	got, err := ExtractText(f)
	if err != nil {
		t.Fatalf("expected fallback on parse error, got error: %v", err)
	}
	if !strings.Contains(got, "not valid go") {
		t.Errorf("fallback extraction lost content: %q", got)
	}
}

// ─── HTML extraction ──────────────────────────────────────────────────────────

func TestExtractText_HTML(t *testing.T) {
	html := `<html><head><title>My Page</title></head>
<body>
<p>Hello <b>World</b></p>
<script>var x = 1;</script>
<style>body{color:red}</style>
</body></html>`
	f := writeTempFile(t, "test.html", html)

	got, err := ExtractText(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "World") {
		t.Errorf("HTML extraction missing visible text: %q", got)
	}
	if strings.Contains(got, "var x") {
		t.Error("HTML extraction should strip <script> content")
	}
	if strings.Contains(got, "color:red") {
		t.Error("HTML extraction should strip <style> content")
	}
}

// ─── PDF ──────────────────────────────────────────────────────────────────────

func TestExtractText_PDF_Unsupported(t *testing.T) {
	f := writeTempFile(t, "test.pdf", "%PDF-1.4 fake content")
	_, err := ExtractText(f)
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("expected ErrUnsupported for .pdf, got %v", err)
	}
}

// ─── Unknown extension ────────────────────────────────────────────────────────

func TestExtractText_UnknownExtFallback(t *testing.T) {
	content := "some raw content"
	f := writeTempFile(t, "test.xyz", content)
	got, err := ExtractText(f)
	if err != nil {
		t.Fatalf("unexpected error for unknown ext: %v", err)
	}
	if got != content {
		t.Errorf("unknown ext fallback = %q, want %q", got, content)
	}
}

// ─── Missing file ─────────────────────────────────────────────────────────────

func TestExtractText_MissingFile(t *testing.T) {
	_, err := ExtractText(filepath.Join(t.TempDir(), "does_not_exist.txt"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// ─── helper ───────────────────────────────────────────────────────────────────

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}
