package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/httpguard"
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

// NewOpenAICompatibleEmbeddingClient builds a client.
// Returns (nil, nil) when BaseURL is empty (graceful skip — callers treat nil
// as "no embedding available").
// Returns (nil, error) when BaseURL is non-empty but fails scheme or IP validation.
func NewOpenAICompatibleEmbeddingClient(baseURL, model, apiKey string) (*OpenAICompatibleEmbeddingClient, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, nil //nolint:nilnil // documented "missing config, skip" sentinel
	}

	// Validate scheme and IP range BEFORE constructing the endpoint.
	// Only scheme + constructor-blocked IPs (169.254/16, link-local) are
	// rejected here; loopback and RFC-1918 ranges are intentionally allowed at
	// construction time so Ollama/vLLM on localhost or a private network work.
	// DNS-rebinding protection is enforced at connect time by SafeDial.
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("openai-compatible-embed: BASE_URL must be http or https, got %q", baseURL)
	}
	if host := parsed.Hostname(); host != "" {
		if ip := net.ParseIP(host); ip != nil {
			if blocked, reason := httpguard.IsConstructorBlockedIP(ip); blocked {
				return nil, fmt.Errorf("openai-compatible-embed: BASE_URL blocked address: %s", reason)
			}
		}
	}

	if strings.TrimSpace(model) == "" {
		model = "text-embedding-3-small" // sensible default for openai.com
	}

	safeClient := httpguard.NewSafeHTTPClient()
	safeClient.Timeout = openAIEmbedTimeout

	return &OpenAICompatibleEmbeddingClient{
		apiKey:   apiKey,
		model:    strings.TrimSpace(model),
		endpoint: strings.TrimRight(baseURL, "/") + "/v1/embeddings",
		http:     safeClient,
	}, nil
}

// truncateBytes truncates a byte slice to at most n bytes, appending "…" when
// truncation occurs. Used to limit error message length for non-OK responses.
func truncateBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
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
		return nil, fmt.Errorf("openai embed API returned %d: %s", resp.StatusCode,
			credentialRe.ReplaceAllString(truncateBytes(raw, 256), "[REDACTED]"))
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
