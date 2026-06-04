package localembedder

import (
	"math"
	"testing"
)

func TestMeanPool_EqualWeights(t *testing.T) {
	hidden := []float32{1, 2, 3, 4, 5, 6} // [[1,2],[3,4],[5,6]]
	mask := []int64{1, 1, 1}
	got := meanPool(hidden, mask, 3, 2)
	want := []float32{3, 4}
	for i, v := range want {
		if math.Abs(float64(got[i]-v)) > 1e-5 {
			t.Errorf("dim %d: got %f, want %f", i, got[i], v)
		}
	}
}

func TestMeanPool_IgnoresPadding(t *testing.T) {
	hidden := []float32{1, 2, 3, 4, 99, 99}
	mask := []int64{1, 1, 0}
	got := meanPool(hidden, mask, 3, 2)
	want := []float32{2, 3}
	for i, v := range want {
		if math.Abs(float64(got[i]-v)) > 1e-5 {
			t.Errorf("dim %d: got %f, want %f", i, got[i], v)
		}
	}
}

func TestL2Normalize(t *testing.T) {
	vec := []float32{3, 4}
	got := l2Normalize(vec)
	if math.Abs(float64(got[0])-0.6) > 1e-5 {
		t.Errorf("dim 0: got %f, want 0.6", got[0])
	}
	if math.Abs(float64(got[1])-0.8) > 1e-5 {
		t.Errorf("dim 1: got %f, want 0.8", got[1])
	}
}

func TestL2Normalize_ZeroVector(t *testing.T) {
	vec := []float32{0, 0, 0}
	got := l2Normalize(vec)
	for _, v := range got {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Error("zero vector normalization produced NaN/Inf")
		}
	}
}
