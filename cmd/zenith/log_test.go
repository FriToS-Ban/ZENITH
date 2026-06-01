package main

import (
	"testing"
)

func TestFilterLines_NoFilter(t *testing.T) {
	lines := []string{"a", "b", "c"}
	got := filterLines(lines, "")
	if len(got) != 3 {
		t.Errorf("expected 3 lines with empty filter, got %d", len(got))
	}
}

func TestFilterLines_MatchesEventType(t *testing.T) {
	lines := []string{
		"2026-06-01 12:00:00  INDEXED    /some/file.txt",
		"2026-06-01 12:01:00  SEARCH     \"hello\" → 3 results",
		"2026-06-01 12:02:00  INDEXED    /other/file.txt",
	}
	got := filterLines(lines, "SEARCH")
	if len(got) != 1 {
		t.Errorf("expected 1 SEARCH line, got %d", len(got))
	}
}

func TestFilterLines_CaseInsensitive(t *testing.T) {
	lines := []string{
		"2026-06-01 12:00:00  INDEXED    /some/file.txt",
		"2026-06-01 12:01:00  SEARCH     \"hello\" → 3 results",
	}
	got := filterLines(lines, "search")
	if len(got) != 1 {
		t.Errorf("expected 1 result for lowercase filter, got %d", len(got))
	}
}

func TestTailLines_FewerThanN(t *testing.T) {
	lines := []string{"a", "b", "c"}
	got := tailLines(lines, 10)
	if len(got) != 3 {
		t.Errorf("expected all 3 lines when n > len, got %d", len(got))
	}
}

func TestTailLines_ExactlyN(t *testing.T) {
	lines := []string{"a", "b", "c", "d", "e"}
	got := tailLines(lines, 3)
	if len(got) != 3 {
		t.Errorf("expected 3 lines, got %d", len(got))
	}
	if got[0] != "c" {
		t.Errorf("expected first tail line to be 'c', got %q", got[0])
	}
}

func TestRunLog_NoFile(t *testing.T) {
	err := runLog("/nonexistent/path/zenith.log")
	if err != nil {
		t.Errorf("expected nil error for missing log, got: %v", err)
	}
}
