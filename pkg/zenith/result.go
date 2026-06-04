package zenith

// Result is a single search result returned by Search.
type Result struct {
	ID     string  // original document ID passed to Add
	Score  float64 // normalised to [0.0, 1.0]; never NaN, never Inf
	Chunks []Chunk // non-nil only for chunked documents (PDF pages)
}

// Chunk holds page and position metadata for a result from a chunked document.
type Chunk struct {
	Page            int
	Index           int
	BboxX, BboxY    float32
	BboxW, BboxH    float32
}
