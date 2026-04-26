package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const geminiEmbedURL = "https://generativelanguage.googleapis.com/v1beta/models/gemini-embedding-001:embedContent"

// EmbeddingClient calls the Gemini embedding API to generate vector embeddings.
type EmbeddingClient struct {
	apiKey string
	client *http.Client
}

// NewEmbeddingClient returns an EmbeddingClient configured from GEMINI_API_KEY env var.
func NewEmbeddingClient() *EmbeddingClient {
	return &EmbeddingClient{
		apiKey: os.Getenv("GEMINI_API_KEY"),
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

type embedPart struct {
	Text string `json:"text"`
}

type embedContent struct {
	Parts []embedPart `json:"parts"`
}

type embedRequest struct {
	Model                string       `json:"model"`
	Content              embedContent `json:"content"`
	OutputDimensionality int          `json:"outputDimensionality,omitempty"`
}

type embedValues struct {
	Values []float32 `json:"values"`
}

type embedResponse struct {
	Embedding embedValues `json:"embedding"`
}

// Embed returns a 768-dimension embedding vector for the given text (truncated via outputDimensionality).
// Returns nil, nil if GEMINI_API_KEY is not set (graceful degradation).
func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float32, error) {
	if c.apiKey == "" {
		return nil, nil
	}

	body := embedRequest{
		Model: "models/gemini-embedding-001",
		Content: embedContent{
			Parts: []embedPart{{Text: text}},
		},
		OutputDimensionality: 768,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling embed request: %w", err)
	}

	// G107: URL is constructed from a trusted constant + API key. Gemini API spec
	// requires the key as a query parameter — using a header is not supported.
	apiURL := geminiEmbedURL + "?key=" + c.apiKey //nolint:gosec // Gemini requires key in URL
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling gemini embed API: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			// Best-effort close; response already fully read.
			_ = closeErr
		}
	}()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB limit
	if err != nil {
		return nil, fmt.Errorf("reading embed response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini embed API returned %d: %s", resp.StatusCode, string(respBytes))
	}

	var result embedResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("parsing embed response: %w", err)
	}

	return result.Embedding.Values, nil
}
