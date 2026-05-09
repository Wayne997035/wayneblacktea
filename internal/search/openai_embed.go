package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	openAIEmbedTimeout = 30 * time.Second
	openAIEmbedMaxBody = 1 << 20
)

// OpenAICompatibleEmbeddingClient calls any OpenAI-compatible /v1/embeddings
// endpoint (Ollama, vLLM, LM Studio, OpenAI, etc.) to generate dense vectors.
//
// API key is optional — Ollama and most self-hosted endpoints do not require one.
//
// SECURITY:
//   - Bearer token injected only via Authorization header (never in URL).
//   - Response body capped at 1 MiB via io.LimitReader.
//   - Credential patterns in text are scrubbed by credentialRe before sending.
type OpenAICompatibleEmbeddingClient struct {
	apiKey   string // optional
	model    string
	endpoint string // e.g. http://localhost:11434/v1/embeddings
	http     *http.Client
}

// NewOpenAICompatibleEmbeddingClient builds a client. Returns nil when BaseURL
// is empty (graceful skip — callers treat nil as "no embedding available").
func NewOpenAICompatibleEmbeddingClient(baseURL, model, apiKey string) *OpenAICompatibleEmbeddingClient {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil
	}
	if strings.TrimSpace(model) == "" {
		model = "text-embedding-3-small" // sensible default for openai.com
	}
	return &OpenAICompatibleEmbeddingClient{
		apiKey:   apiKey,
		model:    strings.TrimSpace(model),
		endpoint: strings.TrimRight(baseURL, "/") + "/v1/embeddings",
		http:     &http.Client{Timeout: openAIEmbedTimeout},
	}
}

// Embed calls POST /v1/embeddings and returns the first embedding vector.
// Returns (nil, nil) when text is empty — callers treat nil as "skip write".
// Credential patterns are scrubbed from text before sending.
func (c *OpenAICompatibleEmbeddingClient) Embed(ctx context.Context, text string) ([]float32, error) {
	if text == "" {
		return nil, nil
	}
	text = credentialRe.ReplaceAllString(text, "[REDACTED]")

	body, err := json.Marshal(map[string]any{
		"model": c.model,
		"input": text,
	})
	if err != nil {
		return nil, fmt.Errorf("openai embed marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai embed build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embed call: %w", sanitizeURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, openAIEmbedMaxBody))
	if err != nil {
		return nil, fmt.Errorf("openai embed read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai embed API returned %d: %s", resp.StatusCode, raw)
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("openai embed parse response: %w", err)
	}
	if len(result.Data) == 0 || len(result.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("openai embed: empty embedding in response")
	}
	return result.Data[0].Embedding, nil
}
