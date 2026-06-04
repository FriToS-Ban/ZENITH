// Package localembedder provides in-process sentence embeddings using the
// all-MiniLM-L6-v2 ONNX model. The model and onnxruntime library are embedded
// in the binary via go:embed and extracted to a temp directory on first use.
//
// Requires CGo (CGO_ENABLED=1) and a C compiler to build. Without CGo, New()
// returns an error and the caller should fall back to a deterministic embedder.
//
//go:generate go run ../../scripts/download_assets.go
package localembedder

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/shramanb113/ZENITH/internal/embedding"
)

//go:embed assets/model.onnx
var modelBytes []byte

//go:embed assets/vocab.txt
var vocabBytes []byte

// Embedder implements embedding.Embedder using in-process ONNX inference.
// All methods are safe for concurrent use.
type Embedder struct {
	tok   *tokenizer
	model *onnxModel
}

// New loads the embedded ONNX model and returns a ready Embedder.
// Requires CGo (CGO_ENABLED=1). Returns an error if CGo is unavailable or
// the onnxruntime library cannot be initialised.
func New() (*Embedder, error) {
	tok, err := newTokenizerFromBytes(vocabBytes)
	if err != nil {
		return nil, fmt.Errorf("localembedder: tokenizer: %w", err)
	}

	libPath, err := extractToTemp(ortLibBytes, ortLibFilename)
	if err != nil {
		return nil, fmt.Errorf("localembedder: extract ort lib: %w", err)
	}

	m, err := newOnnxModel(modelBytes, libPath)
	if err != nil {
		return nil, fmt.Errorf("localembedder: ort session: %w", err)
	}

	return &Embedder{tok: tok, model: m}, nil
}

// Embed returns a 384-dimensional L2-normalised vector for text.
func (e *Embedder) Embed(_ context.Context, text string) ([]float32, error) {
	ids, mask, typeIDs := e.tok.tokenize(text, maxLen)
	hidden, err := e.model.infer(ids, mask, typeIDs, 1, maxLen)
	if err != nil {
		return nil, err
	}
	vec := meanPool(hidden, mask, maxLen, hiddenSize)
	return l2Normalize(vec), nil
}

// EmbedBatch returns embeddings for all texts in a single ONNX forward pass.
func (e *Embedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	n := len(texts)
	flatIDs := make([]int64, n*maxLen)
	flatMask := make([]int64, n*maxLen)
	flatTypeIDs := make([]int64, n*maxLen)

	for i, t := range texts {
		ids, mask, typeIDs := e.tok.tokenize(t, maxLen)
		copy(flatIDs[i*maxLen:], ids)
		copy(flatMask[i*maxLen:], mask)
		copy(flatTypeIDs[i*maxLen:], typeIDs)
	}

	hidden, err := e.model.infer(flatIDs, flatMask, flatTypeIDs, n, maxLen)
	if err != nil {
		return nil, err
	}

	chunkSize := maxLen * hiddenSize
	result := make([][]float32, n)
	for i := range texts {
		chunk := hidden[i*chunkSize : (i+1)*chunkSize]
		vec := meanPool(chunk, flatMask[i*maxLen:(i+1)*maxLen], maxLen, hiddenSize)
		result[i] = l2Normalize(vec)
	}
	return result, nil
}

// Dimensions returns 384 — the output size of all-MiniLM-L6-v2.
func (e *Embedder) Dimensions() int { return hiddenSize }

// Verify implements embedding.Embedder at compile time.
var _ embedding.Embedder = (*Embedder)(nil)
