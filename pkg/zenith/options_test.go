package zenith_test

import (
	"testing"

	"github.com/shramanb113/ZENITH/internal/embedding"
	"github.com/shramanb113/ZENITH/pkg/zenith"
)

// Each option is tested by passing it to Open and checking whether an
// error is returned — that's the only observable effect from outside the package.

func TestWithLimit_Positive(t *testing.T) {
	db, err := zenith.Open(":memory:", zenith.WithLimit(5))
	if err != nil {
		t.Fatalf("WithLimit(5) should be valid: %v", err)
	}
	db.Close()
}

func TestWithLimit_Zero(t *testing.T) {
	_, err := zenith.Open(":memory:", zenith.WithLimit(0))
	if err == nil {
		t.Fatal("WithLimit(0) should return an error")
	}
}

func TestWithLimit_Negative(t *testing.T) {
	_, err := zenith.Open(":memory:", zenith.WithLimit(-1))
	if err == nil {
		t.Fatal("WithLimit(-1) should return an error")
	}
}

func TestWithCacheSize_Positive(t *testing.T) {
	db, err := zenith.Open(":memory:", zenith.WithCacheSize(100))
	if err != nil {
		t.Fatalf("WithCacheSize(100) should be valid: %v", err)
	}
	db.Close()
}

func TestWithCacheSize_Zero(t *testing.T) {
	// 0 disables the cache — valid
	db, err := zenith.Open(":memory:", zenith.WithCacheSize(0))
	if err != nil {
		t.Fatalf("WithCacheSize(0) should be valid: %v", err)
	}
	db.Close()
}

func TestWithCacheSize_Negative(t *testing.T) {
	_, err := zenith.Open(":memory:", zenith.WithCacheSize(-1))
	if err == nil {
		t.Fatal("WithCacheSize(-1) should return an error")
	}
}

func TestWithFuzzyDistance_Valid(t *testing.T) {
	for _, d := range []int{0, 1, 2, 3, 4, 5} {
		db, err := zenith.Open(":memory:", zenith.WithFuzzyDistance(d))
		if err != nil {
			t.Fatalf("WithFuzzyDistance(%d) should be valid: %v", d, err)
		}
		db.Close()
	}
}

func TestWithFuzzyDistance_Negative(t *testing.T) {
	_, err := zenith.Open(":memory:", zenith.WithFuzzyDistance(-1))
	if err == nil {
		t.Fatal("WithFuzzyDistance(-1) should return an error")
	}
}

func TestWithFuzzyDistance_AboveMax_Clamped(t *testing.T) {
	// Values above 5 are clamped, not rejected
	db, err := zenith.Open(":memory:", zenith.WithFuzzyDistance(100))
	if err != nil {
		t.Fatalf("WithFuzzyDistance(100) should be clamped, not error: %v", err)
	}
	db.Close()
}

func TestWithBM25Only(t *testing.T) {
	db, err := zenith.Open(":memory:", zenith.WithBM25Only())
	if err != nil {
		t.Fatalf("WithBM25Only() should be valid: %v", err)
	}
	db.Close()
}

func TestWithEmbedder_Nil(t *testing.T) {
	_, err := zenith.Open(":memory:", zenith.WithEmbedder(nil))
	if err == nil {
		t.Fatal("WithEmbedder(nil) should return an error")
	}
}

func TestWithEmbedder_Custom(t *testing.T) {
	emb := embedding.NewDeterministicEmbedder(384)
	db, err := zenith.Open(":memory:", zenith.WithEmbedder(emb))
	if err != nil {
		t.Fatalf("WithEmbedder(valid) should be valid: %v", err)
	}
	db.Close()
}

func TestMultipleOptions(t *testing.T) {
	emb := embedding.NewDeterministicEmbedder(384)
	db, err := zenith.Open(":memory:",
		zenith.WithEmbedder(emb),
		zenith.WithLimit(20),
		zenith.WithFuzzyDistance(1),
		zenith.WithCacheSize(0),
	)
	if err != nil {
		t.Fatalf("multiple valid options should not error: %v", err)
	}
	db.Close()
}

func TestLimitSearchOption_Positive(t *testing.T) {
	// Limit as a SearchOption — just verify it doesn't break Search
	db, err := zenith.Open(":memory:", zenith.WithBM25Only())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	ctx := bgCtx()
	db.Add(ctx, "a", "hello world")
	db.Add(ctx, "b", "hello world again")

	results, err := db.Search(ctx, "hello", zenith.Limit(1))
	if err != nil {
		t.Fatalf("Search with Limit option: %v", err)
	}
	if len(results) > 1 {
		t.Fatalf("Limit(1) should cap at 1 result, got %d", len(results))
	}
}
