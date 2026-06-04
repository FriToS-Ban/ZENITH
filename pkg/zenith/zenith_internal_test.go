package zenith

import (
	"math"
	"strings"
	"testing"
)

// --- validateID ---

func TestValidateID_Valid(t *testing.T) {
	cases := []string{"doc1", "uuid-1234", "path/to/file", "a", strings.Repeat("x", 512)}
	for _, id := range cases {
		if err := validateID(id); err != nil {
			t.Errorf("validateID(%q): unexpected error %v", id, err)
		}
	}
}

func TestValidateID_Empty(t *testing.T) {
	if err := validateID(""); err != ErrInvalidID {
		t.Fatalf("want ErrInvalidID, got %v", err)
	}
}

func TestValidateID_TooLong(t *testing.T) {
	id := strings.Repeat("a", 513)
	if err := validateID(id); err != ErrIDTooLong {
		t.Fatalf("want ErrIDTooLong, got %v", err)
	}
}

func TestValidateID_ExactlyMaxLength(t *testing.T) {
	id := strings.Repeat("a", 512)
	if err := validateID(id); err != nil {
		t.Fatalf("512-byte ID should be valid, got %v", err)
	}
}

func TestValidateID_ControlChars(t *testing.T) {
	cases := []string{"\x00id", "\x01id", "\x1fid", "id\nwith\nnewline"}
	for _, id := range cases {
		if err := validateID(id); err != ErrInvalidID {
			t.Errorf("validateID(%q): want ErrInvalidID, got %v", id, err)
		}
	}
}

func TestValidateID_PipeSeparator(t *testing.T) {
	if err := validateID("my||doc"); err != ErrInvalidID {
		t.Fatalf("want ErrInvalidID for || in ID, got %v", err)
	}
}

func TestValidateID_SinglePipeAllowed(t *testing.T) {
	// single pipe is fine, only || is reserved
	if err := validateID("my|doc"); err != nil {
		t.Fatalf("single pipe should be valid, got %v", err)
	}
}

// --- validateText ---

func TestValidateText_Valid(t *testing.T) {
	cases := []string{"hello world", "  content  ", "a"}
	for _, text := range cases {
		if err := validateText(text); err != nil {
			t.Errorf("validateText(%q): unexpected error %v", text, err)
		}
	}
}

func TestValidateText_Empty(t *testing.T) {
	if err := validateText(""); err != ErrEmptyDocument {
		t.Fatalf("want ErrEmptyDocument, got %v", err)
	}
}

func TestValidateText_WhitespaceOnly(t *testing.T) {
	cases := []string{" ", "\t", "\n", "   \t\n  "}
	for _, text := range cases {
		if err := validateText(text); err != ErrEmptyDocument {
			t.Errorf("validateText(%q): want ErrEmptyDocument, got %v", text, err)
		}
	}
}

// --- normaliseScore ---

func TestNormaliseScore_Normal(t *testing.T) {
	n := normaliseScore(5.0, 10.0)
	if n != 0.5 {
		t.Fatalf("want 0.5, got %v", n)
	}
}

func TestNormaliseScore_MaxScore(t *testing.T) {
	n := normaliseScore(10.0, 10.0)
	if n != 1.0 {
		t.Fatalf("want 1.0, got %v", n)
	}
}

func TestNormaliseScore_ZeroMax(t *testing.T) {
	n := normaliseScore(5.0, 0)
	if n != 0 {
		t.Fatalf("want 0, got %v", n)
	}
}

func TestNormaliseScore_NaN(t *testing.T) {
	n := normaliseScore(math.NaN(), 1.0)
	if n != 0 {
		t.Fatalf("want 0 for NaN input, got %v", n)
	}
}

func TestNormaliseScore_Inf(t *testing.T) {
	n := normaliseScore(math.Inf(1), 1.0)
	// Inf/1.0 = Inf, which is clamped to 0
	if n != 0 {
		t.Fatalf("want 0 for Inf input, got %v", n)
	}
}

func TestNormaliseScore_Negative(t *testing.T) {
	n := normaliseScore(-5.0, 10.0)
	if n != 0 {
		t.Fatalf("want 0 for negative score, got %v", n)
	}
}

func TestNormaliseScore_GreaterThanOne(t *testing.T) {
	// score > maxScore shouldn't happen but is clamped to 1.0
	n := normaliseScore(15.0, 10.0)
	if n != 1.0 {
		t.Fatalf("want 1.0 for score > max, got %v", n)
	}
}

// --- parseChunkID ---

func TestParseChunkID_Plain(t *testing.T) {
	id, chunk := parseChunkID("report.pdf")
	if id != "report.pdf" || chunk != nil {
		t.Fatalf("plain ID: got id=%q chunk=%v", id, chunk)
	}
}

func TestParseChunkID_WithChunk(t *testing.T) {
	id, chunk := parseChunkID("report.pdf||p3||c1||text||10.00,20.00,400.00,15.00")
	if id != "report.pdf" {
		t.Fatalf("want id=report.pdf, got %q", id)
	}
	if chunk == nil {
		t.Fatal("want non-nil chunk")
	}
	if chunk.Page != 3 {
		t.Errorf("want Page=3, got %d", chunk.Page)
	}
	if chunk.Index != 1 {
		t.Errorf("want Index=1, got %d", chunk.Index)
	}
	if chunk.BboxX != 10.0 {
		t.Errorf("want BboxX=10.0, got %f", chunk.BboxX)
	}
}

func TestParseChunkID_PartialChunk(t *testing.T) {
	// Missing bbox — should not panic
	id, chunk := parseChunkID("doc||p1||c0")
	if id != "doc" {
		t.Fatalf("want id=doc, got %q", id)
	}
	if chunk == nil {
		t.Fatal("want non-nil chunk even for partial")
	}
	if chunk.Page != 1 {
		t.Errorf("want Page=1, got %d", chunk.Page)
	}
}

func TestParseChunkID_IDWithNoSeparator(t *testing.T) {
	// Regular UUID-style ID
	id, chunk := parseChunkID("550e8400-e29b-41d4-a716-446655440000")
	if id != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("UUID should pass through unchanged, got %q", id)
	}
	if chunk != nil {
		t.Fatal("want nil chunk for plain ID")
	}
}

// --- sanitiseText ---

func TestSanitiseText_NullBytes(t *testing.T) {
	result := sanitiseText("hello\x00world")
	if strings.Contains(result, "\x00") {
		t.Fatal("null byte should be removed")
	}
}

func TestSanitiseText_InvalidUTF8(t *testing.T) {
	result := sanitiseText("hello\xff\xfeworld")
	// Should not panic; result should be valid UTF-8
	if !isValidUTF8(result) {
		t.Fatal("result should be valid UTF-8 after sanitisation")
	}
}

func TestSanitiseText_ValidPassthrough(t *testing.T) {
	input := "hello world"
	if result := sanitiseText(input); result != input {
		t.Fatalf("clean text should pass through unchanged, got %q", result)
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
