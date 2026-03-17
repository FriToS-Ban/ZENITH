package ranking

import "math"

// DotProduct calculates the dot product using float64 and 4-way loop unrolling for speed
func DotProduct(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	var dot float64
	limit := len(a) - len(a)%4

	for i := 0; i < limit; i += 4 {
		dot += float64(a[i])*float64(b[i]) +
			float64(a[i+1])*float64(b[i+1]) +
			float64(a[i+2])*float64(b[i+2]) +
			float64(a[i+3])*float64(b[i+3])
	}

	for i := limit; i < len(a); i++ {
		dot += float64(a[i]) * float64(b[i])
	}

	return dot
}

// Magnitude calculates the vector magnitude using float64 and loop unrolling
func Magnitude(a []float32) float64 {
	var selfA float64
	limit := len(a) - len(a)%4

	for i := 0; i < limit; i += 4 {
		selfA += float64(a[i])*float64(a[i]) +
			float64(a[i+1])*float64(a[i+1]) +
			float64(a[i+2])*float64(a[i+2]) +
			float64(a[i+3])*float64(a[i+3])
	}

	for i := limit; i < len(a); i++ {
		selfA += float64(a[i]) * float64(a[i])
	}

	return math.Sqrt(selfA)
}

// CosineSimilarity using precomputed magnitudes to save on repeated math.Sqrt calculations
func CosineSimilarity(a, b []float32, magA, magB float64) float32 {
	if magA == 0.0 || magB == 0.0 {
		return 0.0
	}
	dot := DotProduct(a, b)
	return float32(dot / (magA * magB))
}

// L2Distance calculates the Euclidean distance between two vectors.
func L2Distance(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return math.MaxFloat32
	}
	var sum float64
	limit := len(a) - len(a)%4
	for i := 0; i < limit; i += 4 {
		d0 := float64(a[i]) - float64(b[i])
		d1 := float64(a[i+1]) - float64(b[i+1])
		d2 := float64(a[i+2]) - float64(b[i+2])
		d3 := float64(a[i+3]) - float64(b[i+3])
		sum += d0*d0 + d1*d1 + d2*d2 + d3*d3
	}
	for i := limit; i < len(a); i++ {
		d := float64(a[i]) - float64(b[i])
		sum += d * d
	}
	return float32(math.Sqrt(sum))
}
