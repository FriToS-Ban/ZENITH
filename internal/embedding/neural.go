package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type EmbedRequest struct {
	Text string `json:"text"`
}

type EmbedResponse struct {
	Embedding []float32 `json:"embedding"`
}

type BatchRequest struct {
	Texts []string `json:"texts"`
}

type BatchResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

type NeuralEmbedder struct {
	client *http.Client
	url    string
}

func NewNeuralEmbedder(url string, timeout time.Duration) *NeuralEmbedder {
	return &NeuralEmbedder{
		client: &http.Client{
			Timeout: timeout,
		},
		url: url,
	}
}

func (n *NeuralEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	reqBody, err := json.Marshal(EmbedRequest{Text: text})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url+"/embed", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nerve offline or timeout: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nerve returned error status: %d", resp.StatusCode)
	}

	var res EmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed to decode nerve response: %w", err)
	}

	return res.Embedding, nil
}

func (n *NeuralEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	reqBody, err := json.Marshal(BatchRequest{Texts: texts})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal batch request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url+"/embed_batch", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create batch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nerve offline or timeout during batch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nerve returned error status for batch: %d", resp.StatusCode)
	}

	var res BatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed to decode nerve batch response: %w", err)
	}

	return res.Embeddings, nil
}

func (n *NeuralEmbedder) Dimensions() int {
	return 384 // all-MiniLM-L6-v2 dimensions
}
