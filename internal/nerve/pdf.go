package nerve

// Chunk holds extracted content from one PDF segment — text or an image caption.
type Chunk struct {
	Text       string
	Page       int
	ChunkIndex int
	SourceType string // "text" or "image_caption"
	Embedding  []float32
	BboxX      float32
	BboxY      float32
	BboxW      float32
	BboxH      float32
}
