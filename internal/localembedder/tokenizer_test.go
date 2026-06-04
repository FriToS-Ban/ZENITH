package localembedder

import (
	_ "embed"
	"strings"
	"testing"
)

//go:embed assets/vocab.txt
var testVocab []byte

func TestTokenizer_CLSAndSEP(t *testing.T) {
	tok, err := newTokenizerFromBytes(testVocab)
	if err != nil {
		t.Fatalf("newTokenizer: %v", err)
	}
	ids, mask, typeIDs := tok.tokenize("hello world", 256)

	if len(ids) != 256 {
		t.Fatalf("ids length: got %d, want 256", len(ids))
	}
	if ids[0] != tokenCLS {
		t.Errorf("ids[0]: got %d, want %d ([CLS])", ids[0], tokenCLS)
	}
	if mask[0] != 1 {
		t.Errorf("mask[0]: got %d, want 1", mask[0])
	}

	sepIdx := -1
	for i, id := range ids {
		if id == tokenSEP {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		t.Fatal("[SEP] not found in output")
	}
	for i := sepIdx + 1; i < 256; i++ {
		if ids[i] != tokenPAD {
			t.Errorf("ids[%d] after [SEP]: got %d, want 0 ([PAD])", i, ids[i])
		}
		if mask[i] != 0 {
			t.Errorf("mask[%d] after [SEP]: got %d, want 0", i, mask[i])
		}
	}
	for _, v := range typeIDs {
		if v != 0 {
			t.Error("all typeIDs must be 0")
		}
	}
}

func TestTokenizer_Truncation(t *testing.T) {
	tok, err := newTokenizerFromBytes(testVocab)
	if err != nil {
		t.Fatalf("newTokenizer: %v", err)
	}
	long := strings.Repeat("hello world ", 200)
	ids, _, _ := tok.tokenize(long, 256)
	if len(ids) != 256 {
		t.Fatalf("truncation: got len %d, want 256", len(ids))
	}
	if ids[0] != tokenCLS {
		t.Error("truncated: first token must be [CLS]")
	}
	if ids[255] != tokenSEP {
		t.Errorf("truncated: last token must be [SEP] (102), got %d", ids[255])
	}
}

func TestTokenizer_EmptyString(t *testing.T) {
	tok, _ := newTokenizerFromBytes(testVocab)
	ids, mask, _ := tok.tokenize("", 256)
	if ids[0] != tokenCLS || ids[1] != tokenSEP {
		t.Error("empty string must produce [CLS][SEP] then padding")
	}
	if mask[0] != 1 || mask[1] != 1 {
		t.Error("mask must be 1 for [CLS] and [SEP]")
	}
	if mask[2] != 0 {
		t.Error("mask must be 0 for padding after [SEP]")
	}
}
