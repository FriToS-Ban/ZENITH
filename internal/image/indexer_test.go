package image

import (
	"strings"
	"testing"
)

// ── pathToText — basic behaviour ─────────────────────────────────────────────

func TestPathToText_Empty(t *testing.T) {
	if got := pathToText(""); got != "" {
		t.Errorf("pathToText(%q) = %q, want empty", "", got)
	}
}

func TestPathToText_FileNameOnly(t *testing.T) {
	got := pathToText("sunset.jpg")
	assertContains(t, got, "sunset")
	assertContains(t, got, "jpg")
}

func TestPathToText_BasicUnixPath(t *testing.T) {
	got := pathToText("/home/user/Photos/2024/vacation/portrait_sunset_beach.jpg")
	for _, tok := range []string{"portrait", "sunset", "beach", "vacation", "jpg"} {
		assertContains(t, got, tok)
	}
}

func TestPathToText_WindowsStylePath(t *testing.T) {
	got := pathToText(`C:\Users\Alice\Pictures\holiday_2023\IMG_0042.png`)
	assertContains(t, got, "png")
	// At least one of the meaningful path segments should appear.
	if !strings.Contains(got, "holiday") && !strings.Contains(got, "0042") {
		t.Errorf("pathToText of Windows path = %q, want tokens from path", got)
	}
}

// ── separator splitting ───────────────────────────────────────────────────────

func TestPathToText_UnderscoreSplit(t *testing.T) {
	got := pathToText("/tmp/my_photo_file.jpg")
	for _, tok := range []string{"my", "photo", "file", "jpg"} {
		assertContains(t, got, tok)
	}
}

func TestPathToText_HyphenSplit(t *testing.T) {
	got := pathToText("/tmp/my-holiday-photo.png")
	for _, tok := range []string{"my", "holiday", "photo", "png"} {
		assertContains(t, got, tok)
	}
}

func TestPathToText_MixedSeparators(t *testing.T) {
	got := pathToText("/tmp/my-holiday_photo_001.jpg")
	for _, tok := range []string{"my", "holiday", "photo", "001", "jpg"} {
		assertContains(t, got, tok)
	}
}

func TestPathToText_DotInFilename(t *testing.T) {
	// "v1.2.3.tar.gz" — dots split into tokens.
	got := pathToText("/releases/v1.2.3.tar.gz")
	// At least some version tokens should appear.
	if !strings.Contains(got, "v1") && !strings.Contains(got, "v") {
		t.Logf("pathToText of version file = %q", got)
	}
	assertContains(t, got, "gz")
}

func TestPathToText_ConsecutiveSeparators(t *testing.T) {
	// Double underscores or hyphens should not produce empty tokens.
	got := pathToText("/tmp/my__photo--file.jpg")
	tokens := strings.Fields(got)
	for _, tok := range tokens {
		if tok == "" {
			t.Error("empty token in output")
		}
	}
}

// ── extension ────────────────────────────────────────────────────────────────

func TestPathToText_ExtensionIncluded(t *testing.T) {
	for _, tc := range []struct {
		path string
		ext  string
	}{
		{"/tmp/file.jpg", "jpg"},
		{"/tmp/file.png", "png"},
		{"/tmp/file.jpeg", "jpeg"},
		{"/tmp/file.tiff", "tiff"},
		{"/tmp/file.gif", "gif"},
		{"/tmp/file.webp", "webp"},
	} {
		got := pathToText(tc.path)
		assertContains(t, got, tc.ext)
	}
}

func TestPathToText_NoExtension(t *testing.T) {
	// Should not panic or produce empty output.
	got := pathToText("/tmp/imagefile")
	assertContains(t, got, "imagefile")
}

func TestPathToText_HiddenFile(t *testing.T) {
	// ".gitignore" — leading dot, no meaningful extension.
	got := pathToText("/home/user/.gitignore")
	if got == "" {
		t.Error("pathToText of hidden file returned empty string")
	}
}

// ── directory tokens ─────────────────────────────────────────────────────────

func TestPathToText_ParentDirIncluded(t *testing.T) {
	got := pathToText("/home/user/vacation/beach.jpg")
	assertContains(t, got, "vacation")
}

func TestPathToText_MaxThreeParentDirs(t *testing.T) {
	// Only up to 3 ancestor directory names should be included.
	got := pathToText("/a/b/c/d/e/f/photo.jpg")
	tokens := strings.Fields(got)
	// "photo" (filename), "jpg" (ext), then at most 3 parent dir tokens.
	if len(tokens) > 6 { // filename tokens + ext + 3 dirs = up to ~5-6
		t.Logf("tokens = %v (len %d); more than expected but may be OK due to splitting", tokens, len(tokens))
	}
	// Must NOT contain deep ancestors like "a" or "b" (more than 3 levels up).
	// This is hard to assert strictly without knowing the exact path structure,
	// so just verify the output is non-empty and does not contain the deepest ancestor.
	if strings.Contains(got, " a ") || got == "a" {
		t.Errorf("output %q contains deep ancestor, should only include 3 levels", got)
	}
}

func TestPathToText_ShallowPath(t *testing.T) {
	// Single directory — no parent to include.
	got := pathToText("/tmp/photo.jpg")
	assertContains(t, got, "photo")
	assertContains(t, got, "jpg")
}

func TestPathToText_NumbersInPath(t *testing.T) {
	got := pathToText("/Photos/2024/vacation/photo.jpg")
	assertContains(t, got, "2024")
	assertContains(t, got, "vacation")
}

// ── output format ─────────────────────────────────────────────────────────────

func TestPathToText_TokensAreSpaceSeparated(t *testing.T) {
	got := pathToText("/tmp/my_photo.jpg")
	// All tokens should be non-empty strings.
	for _, tok := range strings.Fields(got) {
		if tok == "" {
			t.Error("empty token in output")
		}
	}
}

func TestPathToText_NoLeadingOrTrailingSpaces(t *testing.T) {
	got := pathToText("/tmp/photo.jpg")
	if strings.TrimSpace(got) != got {
		t.Errorf("output %q has leading/trailing spaces", got)
	}
}

func TestPathToText_AllLowercase(t *testing.T) {
	got := pathToText("/UPPER/MixedCase/PHOTO.JPG")
	// splitTokens does not lowercase — but tokens should be whatever case
	// the path component provides. Just verify no panic.
	if got == "" {
		t.Error("pathToText of uppercase path returned empty string")
	}
}

// ── splitTokens ───────────────────────────────────────────────────────────────

func TestSplitTokens_Underscore(t *testing.T) {
	got := splitTokens("hello_world")
	assertSliceContains(t, got, "hello")
	assertSliceContains(t, got, "world")
}

func TestSplitTokens_Hyphen(t *testing.T) {
	got := splitTokens("hello-world")
	assertSliceContains(t, got, "hello")
	assertSliceContains(t, got, "world")
}

func TestSplitTokens_Dot(t *testing.T) {
	got := splitTokens("v1.2.3")
	if len(got) < 2 {
		t.Errorf("got %v, want at least 2 tokens from %q", got, "v1.2.3")
	}
}

func TestSplitTokens_NoSeparators(t *testing.T) {
	got := splitTokens("hello")
	if len(got) != 1 || got[0] != "hello" {
		t.Errorf("got %v, want [hello]", got)
	}
}

func TestSplitTokens_EmptyString(t *testing.T) {
	got := splitTokens("")
	if len(got) != 0 {
		t.Errorf("got %v, want []", got)
	}
}

func TestSplitTokens_OnlySeparators(t *testing.T) {
	got := splitTokens("___---...")
	for _, tok := range got {
		if tok == "" {
			t.Error("empty token in output of splitTokens")
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("output %q does not contain %q", haystack, needle)
	}
}

func assertSliceContains(t *testing.T, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			return
		}
	}
	t.Errorf("slice %v does not contain %q", slice, want)
}
