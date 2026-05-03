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
	// openRouterEndpoint is the public chat completions endpoint.
	openRouterEndpoint = "https://openrouter.ai/api/v1/chat/completions"

	// openRouterTimeout is the per-call timeout. We set this with
	// context.WithTimeout in addition to http.Client.Timeout so a slow remote
	// cannot block the calling request handler past the spec'd window.
	openRouterTimeout = 30 * time.Second

	// openRouterMaxBody is the response body cap (1 MiB) — a protection
	// against runaway provider responses that could exhaust process memory.
	openRouterMaxBody = 1 << 20

	// openRouterReferer / openRouterTitle are non-secret attribution headers
	// the OpenRouter docs recommend for tracing. They are not authentication.
	openRouterReferer = "https://github.com/Wayne997035/wayneblacktea"
	openRouterTitle   = "wayneblacktea"
)

// OpenRouterClient is a JSONClient that sends chat completions to OpenRouter.
//
// Construction:
//   - When Models has more than one entry, the request body uses OpenRouter's
//     `models` array form (model fallback).
//   - When Models has a single entry (or is empty and Model is set), the
//     body uses the plain `model` field.
//
// SECURITY:
//   - Bearer token is injected only via Authorization header.
//   - Response is read through io.LimitReader.
//   - Sanitised errors never include the bearer token.
type OpenRouterClient struct {
	apiKey   string
	model    string
	models   []string
	http     *http.Client
	endpoint string
}

// OpenRouterConfig holds construction inputs read from env. Empty Models is
// fine; the client falls back to Model. Empty Model AND Models is rejected by
// NewOpenRouterClient because the upstream API requires one of them.
type OpenRouterConfig struct {
	APIKey string
	Model  string
	Models []string
}

// NewOpenRouterClient builds an OpenRouterClient from config. Returns
// (nil, nil) when APIKey is empty so the caller can `if c == nil { skip }`
// without a nil-pointer panic — this is the chain's "skip missing-key"
// contract (NOT a retry attempt).
func NewOpenRouterClient(cfg OpenRouterConfig) (*OpenRouterClient, error) {
	if cfg.APIKey == "" {
		return nil, nil //nolint:nilnil // documented "missing key, skip" sentinel
	}
	if cfg.Model == "" && len(cfg.Models) == 0 {
		return nil, errors.New("openrouter: OPENROUTER_MODEL or OPENROUTER_MODELS must be set")
	}
	return &OpenRouterClient{
		apiKey:   cfg.APIKey,
		model:    cfg.Model,
		models:   cfg.Models,
		http:     &http.Client{Timeout: openRouterTimeout},
		endpoint: openRouterEndpoint,
	}, nil
}

// setEndpoint swaps the request URL — used only by tests with a
// httptest.Server. Package-private.
func (c *OpenRouterClient) setEndpoint(u string) {
	if u != "" {
		c.endpoint = u
	}
}

// Name implements JSONClient.
func (c *OpenRouterClient) Name() string { return "openrouter" }

// CompleteJSON sends a single chat completion to OpenRouter and returns the
// model's text output (choices[0].message.content). On retryable failure it
// returns *Retryable so the chain layer falls through.
func (c *OpenRouterClient) CompleteJSON(ctx context.Context, req JSONRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, openRouterTimeout)
	defer cancel()

	body, err := c.buildBody(req)
	if err != nil {
		return "", &Retryable{Provider: c.Name(), Reason: "marshal", Err: err}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", &Retryable{Provider: c.Name(), Reason: "build_request", Err: err}
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("HTTP-Referer", openRouterReferer)
	httpReq.Header.Set("X-OpenRouter-Title", openRouterTitle)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", &Retryable{Provider: c.Name(), Reason: classifyTransport(err), Err: sanitiseTransportErr(err)}
	}
	defer func() { _ = resp.Body.Close() }()

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, openRouterMaxBody))
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
		Model string `json:"model"`
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

// buildBody assembles the JSON request body. When more than one model is
// configured we use OpenRouter's `models` array form (model fallback); a
// single configured model uses the plain `model` field.
func (c *OpenRouterClient) buildBody(req JSONRequest) ([]byte, error) {
	payload := map[string]any{
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
	switch {
	case len(c.models) > 1:
		payload["models"] = c.models
	case len(c.models) == 1:
		payload["model"] = c.models[0]
	default:
		payload["model"] = c.model
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal openrouter request: %w", err)
	}
	return body, nil
}

// classifyTransport maps a net/http transport error to a short reason label.
// We intentionally check for net.Error.Timeout() via the common interface.
func classifyTransport(err error) string {
	type timeoutErr interface{ Timeout() bool }
	var te timeoutErr
	if errors.As(err, &te) && te.Timeout() {
		return reasonTimeout
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return reasonTimeout
	}
	if errors.Is(err, context.Canceled) {
		return reasonCancelled
	}
	return reasonNetwork
}

// sanitiseTransportErr strips bearer tokens / Authorization markers from a
// transport error message. The Go stdlib already redacts URL credentials in
// url.Error; we add belt-and-braces filtering for "Bearer " substrings.
func sanitiseTransportErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "Bearer ") {
		return errors.New(redactBearer(msg))
	}
	return err
}

// redactBearer replaces "Bearer <token>" with "Bearer [REDACTED]" wherever
// the marker appears. Defensive; should rarely fire because Go's http.Client
// never includes Authorization in transport error text.
//
// Implementation note: we walk the string forward, skipping past each
// processed redaction so the loop always advances even if the inserted
// "[REDACTED]" sentinel happens to start with "Bearer " (it does not, but
// we do not rely on that invariant).
func redactBearer(s string) string {
	const marker = "Bearer "
	const sentinel = "[REDACTED]"
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		idx := strings.Index(s[i:], marker)
		if idx < 0 {
			b.WriteString(s[i:])
			break
		}
		// Write everything up to and including the marker.
		b.WriteString(s[i : i+idx+len(marker)])
		// Skip the token until whitespace / end-of-string.
		j := i + idx + len(marker)
		for j < len(s) && s[j] != ' ' && s[j] != '\n' && s[j] != '\t' {
			j++
		}
		b.WriteString(sentinel)
		i = j
	}
	return b.String()
}

// truncate returns at most n bytes of b as a string, suffixed with "..." if
// truncated. Used for snippet inclusion in retryable error reasons.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
