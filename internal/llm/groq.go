package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// groqEndpoint is the OpenAI-compatible chat completions endpoint.
	groqEndpoint = "https://api.groq.com/openai/v1/chat/completions"
	groqTimeout  = 30 * time.Second
	groqMaxBody  = 1 << 20

	// defaultGroqModel matches the legacy hard-coded value in
	// internal/discordbot/analyzer.go so behaviour is unchanged when only
	// GROQ_API_KEY is set (Phase 0 → Phase 4 migration is transparent).
	defaultGroqModel = "llama-3.3-70b-versatile"
)

// GroqClient is a JSONClient that targets Groq's OpenAI-compatible
// chat-completions endpoint. It subsumes the raw HTTP path that previously
// lived in internal/discordbot/analyzer.go.
type GroqClient struct {
	apiKey   string
	model    string
	http     *http.Client
	endpoint string
}

// GroqConfig holds construction inputs read from env. Empty Model defaults to
// defaultGroqModel.
type GroqConfig struct {
	APIKey string
	Model  string
}

// NewGroqClient builds a GroqClient. Returns (nil, nil) when APIKey is empty
// so the caller can `if c == nil { skip }` without nil-checking.
func NewGroqClient(cfg GroqConfig) (*GroqClient, error) {
	if cfg.APIKey == "" {
		return nil, nil //nolint:nilnil // documented "missing key, skip" sentinel
	}
	model := cfg.Model
	if model == "" {
		model = defaultGroqModel
	}
	return &GroqClient{
		apiKey:   cfg.APIKey,
		model:    model,
		http:     &http.Client{Timeout: groqTimeout},
		endpoint: groqEndpoint,
	}, nil
}

// setEndpoint swaps the request URL — used only by tests.
func (c *GroqClient) setEndpoint(u string) {
	if u != "" {
		c.endpoint = u
	}
}

// Name implements JSONClient.
func (c *GroqClient) Name() string { return "groq" }

// CompleteJSON sends a chat completion request and returns the choices[0]
// content. Mirrors OpenRouter's retry classification.
func (c *GroqClient) CompleteJSON(ctx context.Context, req JSONRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, groqTimeout)
	defer cancel()

	payload := map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": req.System},
			{"role": "user", "content": req.User},
		},
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
	}
	if req.JSONMode {
		payload["response_format"] = map[string]string{"type": "json_object"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", &Retryable{Provider: c.Name(), Reason: "marshal", Err: err}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", &Retryable{Provider: c.Name(), Reason: "build_request", Err: err}
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", &Retryable{Provider: c.Name(), Reason: classifyTransport(err), Err: sanitiseTransportErr(err)}
	}
	defer func() { _ = resp.Body.Close() }()

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, groqMaxBody))
	if readErr != nil {
		return "", &Retryable{Provider: c.Name(), Reason: "read_body", Err: readErr}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", &Retryable{
			Provider: c.Name(), Reason: reasonHTTP429,
			Err: fmt.Errorf("status 429: %s", truncate(raw, 256)),
		}
	}
	if resp.StatusCode >= 500 {
		return "", &Retryable{
			Provider: c.Name(), Reason: reasonHTTP5xx,
			Err: fmt.Errorf("status %d: %s", resp.StatusCode, truncate(raw, 256)),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return "", &Retryable{
			Provider: c.Name(),
			Reason:   fmt.Sprintf("http_%d", resp.StatusCode),
			Err:      fmt.Errorf("status %d: %s", resp.StatusCode, truncate(raw, 256)),
		}
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", &Retryable{Provider: c.Name(), Reason: reasonInvalidJSON, Err: err}
	}
	if len(parsed.Choices) == 0 {
		return "", &Retryable{Provider: c.Name(), Reason: reasonEmptyContent, Err: errors.New("no choices returned")}
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		return "", &Retryable{Provider: c.Name(), Reason: reasonEmptyContent, Err: errors.New("empty message content")}
	}
	return content, nil
}
