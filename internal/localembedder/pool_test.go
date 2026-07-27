package localembedder

import (
	"math"
	"testing"
)

// ── meanPool ─────────────────────────────────────────────────────────────────

func TestMeanPool_EqualWeights(t *testing.T) {
	hidden := []float32{1, 2, 3, 4, 5, 6} // [[1,2],[3,4],[5,6]]
	mask := []int64{1, 1, 1}
	got := meanPool(hidden, mask, 3, 2)
	assertFloatSlice(t, got, []float32{3, 4}, 1e-5)
}

func TestMeanPool_IgnoresPadding(t *testing.T) {
	hidden := []float32{1, 2, 3, 4, 99, 99}
	mask := []int64{1, 1, 0}
	got := meanPool(hidden, mask, 3, 2)
	assertFloatSlice(t, got, []float32{2, 3}, 1e-5)
}

func TestMeanPool_SingleRealToken(t *testing.T) {
	hidden := []float32{7, 8, 9, 99, 99, 99}
	mask := []int64{1, 0, 0}
	got := meanPool(hidden, mask, 3, 2)
	// Only first token is real — result equals that token's vector.
	assertFloatSlice(t, got, []float32{7, 8}, 1e-5)
}

func TestMeanPool_AllPadding_ReturnsZeroVector(t *testing.T) {
	hidden := []float32{1, 2, 3, 4}
	mask := []int64{0, 0}
	got := meanPool(hidden, mask, 2, 2)
	for i, v := range got {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Errorf("got[%d]=%v, want 0 (no NaN/Inf on all-padding)", i, v)
		}
		if v != 0 {
			t.Errorf("got[%d]=%v, want 0 for all-padding input", i, v)
		}
	}
}

func TestMeanPool_LastTokenOnly(t *testing.T) {
	hidden := []float32{99, 99, 99, 99, 5, 6}
	mask := []int64{0, 0, 1}
	got := meanPool(hidden, mask, 3, 2)
	assertFloatSlice(t, got, []float32{5, 6}, 1e-5)
}

func TestMeanPool_HighDimensionality(t *testing.T) {
	// Simulate 384-dim hidden state with 4 tokens, 3 real.
	const dim = 384
	const seqLen = 4
	hidden := make([]float32, seqLen*dim)
	mask := []int64{1, 1, 1, 0}
	// Fill real tokens with known values.
	for j := 0; j < dim; j++ {
		hidden[0*dim+j] = 1.0
		hidden[1*dim+j] = 2.0
		hidden[2*dim+j] = 3.0
		hidden[3*dim+j] = 99.0 // padding, should be ignored
	}
	got := meanPool(hidden, mask, seqLen, dim)
	if len(got) != dim {
		t.Fatalf("output length %d, want %d", len(got), dim)
	}
	for i, v := range got {
		if math.Abs(float64(v)-2.0) > 1e-4 {
			t.Errorf("got[%d]=%f, want 2.0 (mean of 1,2,3)", i, v)
			break
		}
	}
}

func TestMeanPool_SingleToken_SingleDim(t *testing.T) {
	hidden := []float32{42}
	mask := []int64{1}
	got := meanPool(hidden, mask, 1, 1)
	if math.Abs(float64(got[0])-42.0) > 1e-5 {
		t.Errorf("got %f, want 42.0", got[0])
	}
}

func TestMeanPool_PreservesNegativeValues(t *testing.T) {
	hidden := []float32{-3, -4, -3, -4}
	mask := []int64{1, 1}
	got := meanPool(hidden, mask, 2, 2)
	assertFloatSlice(t, got, []float32{-3, -4}, 1e-5)
}

// ── l2Normalize ──────────────────────────────────────────────────────────────

func TestL2Normalize_Basic(t *testing.T) {
	got := l2Normalize([]float32{3, 4})
	assertFloatSlice(t, got, []float32{0.6, 0.8}, 1e-5)
}

func TestL2Normalize_UnitVector(t *testing.T) {
	// Already unit — should return same values.
	got := l2Normalize([]float32{1, 0, 0})
	assertFloatSlice(t, got, []float32{1, 0, 0}, 1e-5)
}

func TestL2Normalize_ZeroVector_NoNaNOrInf(t *testing.T) {
	got := l2Normalize([]float32{0, 0, 0})
	for i, v := range got {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Errorf("got[%d]=%v, want 0 (no NaN/Inf)", i, v)
		}
	}
}

func TestL2Normalize_NegativeValues(t *testing.T) {
	got := l2Normalize([]float32{-3, 4})
	// magnitude = 5; result = [-0.6, 0.8]
	assertFloatSlice(t, got, []float32{-0.6, 0.8}, 1e-5)
}

func TestL2Normalize_SmallValues(t *testing.T) {
	got := l2Normalize([]float32{0.001, 0.001, 0.001})
	// All equal — after normalisation each component = 1/sqrt(3).
	want := float32(1.0 / math.Sqrt(3))
	for i, v := range got {
		if math.Abs(float64(v)-float64(want)) > 1e-5 {
			t.Errorf("got[%d]=%f, want %f", i, v, want)
		}
	}
}

func TestL2Normalize_ResultIsUnitLength(t *testing.T) {
	for _, vec := range [][]float32{
		{3, 4},
		{1, 2, 3},
		{-1, 0, 1, 0},
		{0.5, 0.5, 0.5, 0.5},
		{100, 200, 300, 400, 500},
	} {
		result := l2Normalize(vec)
		var norm float64
		for _, v := range result {
			norm += float64(v) * float64(v)
		}
		norm = math.Sqrt(norm)
		if math.Abs(norm-1.0) > 1e-5 {
			t.Errorf("norm=%f after l2Normalize of %v, want 1.0", norm, vec)
		}
	}
}

func TestL2Normalize_SingleElement(t *testing.T) {
	got := l2Normalize([]float32{5})
	if math.Abs(float64(got[0])-1.0) > 1e-5 {
		t.Errorf("got %f, want 1.0", got[0])
	}
}

func TestL2Normalize_ModifiesInPlace(t *testing.T) {
	vec := []float32{3, 4}
	result := l2Normalize(vec)
	// Result should be the same slice (modified in place).
	if &result[0] != &vec[0] {
		t.Error("l2Normalize should modify the slice in place and return it")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func assertFloatSlice(t *testing.T, got, want []float32, tol float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("length: got %d, want %d", len(got), len(want))
		return
	}
	for i := range want {
		if math.Abs(float64(got[i]-want[i])) > tol {
			t.Errorf("dim %d: got %f, want %f (tol %g)", i, got[i], want[i], tol)
		}
	}
}
