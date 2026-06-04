package image

import (
	"strings"
	"testing"
)

func TestPathToText_Basic(t *testing.T) {
	text := pathToText("/home/user/Photos/2024/vacation/portrait_sunset_beach.jpg")
	for _, tok := range []string{"portrait", "sunset", "beach", "vacation", "jpg"} {
		if !strings.Contains(text, tok) {
			t.Errorf("expected token %q in %q", tok, text)
		}
	}
}

func TestPathToText_HyphenAndUnderscore(t *testing.T) {
	text := pathToText("/tmp/my-holiday-photo_001.png")
	for _, tok := range []string{"my", "holiday", "photo", "001", "png"} {
		if !strings.Contains(text, tok) {
			t.Errorf("expected token %q in %q", tok, text)
		}
	}
}

func TestPathToText_EmptyFilename(t *testing.T) {
	_ = pathToText("") // must not panic
}
