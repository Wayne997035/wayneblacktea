package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	openAICompatibleTimeout = 30 * time.Second
	openAICompatibleMaxBody = 1 << 20
)

// OpenAICompatibleClient is a JSONClient targeting any OpenAI-compatible
// chat-completions endpoint (Ollama, vLLM, LM Studio, DeepSeek, OpenAI, etc.).
// API key is optional — Ollama and some self-hosted endpoints do not require one.
//
// SECURITY:
//   - Bearer token injected only via Authorization header (never in URL).
//   - Response body capped at 1 MiB via io.LimitReader.
//   - Transport errors are sanitised by sanitiseTransportErr.
type OpenAICompatibleClient struct {
	apiKey   string // optional
	model    string
	http     *http.Client
	endpoint string // e.g. http://localhost:11434/v1/chat/completions
}

// OpenAICompatibleConfig holds construction inputs read from env.
type OpenAICompatibleConfig struct {
	// BaseURL is the server root, e.g. "http://localhost:11434".
	// The client appends "/v1/chat/completions" automatically.
	BaseURL string
	// Model is required when BaseURL is non-empty.
	Model string
	// APIKey is optional. When empty no Authorization header is sent.
	APIKey string
}

// NewOpenAICompatibleClient builds a client from config. Returns (nil, nil)
// when BaseURL is empty so the caller can `if c == nil { skip }` — same
// "missing config, skip" contract as NewClaudeClient and NewGroqClient.
func NewOpenAICompatibleClient(cfg OpenAICompatibleConfig) (*OpenAICompatibleClient, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		return nil, nil //nolint:nilnil // documented "missing config, skip" sentinel
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, errors.New("openai-compatible: OPENAI_COMPATIBLE_MODEL is required when OPENAI_COMPATIBLE_BASE_URL is set")
	}

	// Validate scheme and IP range BEFORE constructing the endpoint.
	// Only scheme + constructor-blocked IPs (169.254/16, link-local) are
	// rejected here; loopback and RFC-1918 ranges are intentionally allowed at
	// construction time so Ollama/vLLM on localhost or a private network work.
	// DNS-rebinding protection is enforced at connect time by SafeDial.
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("openai-compatible: BASE_URL must be http or https, got %q", baseURL)
	}
	if host := parsed.Hostname(); host != "" {
		if ip := net.ParseIP(host); ip != nil {
			if blocked, reason := httpguard.IsConstructorBlockedIP(ip); blocked {
				return nil, fmt.Errorf("openai-compatible: BASE_URL blocked address: %s", reason)
			}
		}
	}

	safeClient := httpguard.NewSafeHTTPClient()
	safeClient.Timeout = openAICompatibleTimeout

	endpoint := strings.TrimRight(baseURL, "/") + "/v1/chat/completions"
	return &OpenAICompatibleClient{
		apiKey:   cfg.APIKey,
		model:    model,
		http:     safeClient,
		endpoint: endpoint,
	}, nil
}

// Name implements JSONClient.
func (c *OpenAICompatibleClient) Name() string { return "openai-compatible" }

// CompleteJSON sends a chat completion request and returns choices[0] content.
func (c *OpenAICompatibleClient) CompleteJSON(ctx context.Context, req JSONRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, openAICompatibleTimeout)
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
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", &Retryable{Provider: c.Name(), Reason: classifyTransport(err), Err: sanitiseTransportErr(err)}
	}
	defer func() { _ = resp.Body.Close() }()

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, openAICompatibleMaxBody))
	if readErr != nil {
		return "", &Retryable{Provider: c.Name(), Reason: "read_body", Err: readErr}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return "", &Retryable{
			Provider: c.Name(), Reason: reasonHTTP429,
			Err: fmt.Errorf("status 429: %s", redactBearer(truncate(raw, 256))),
		}
	}
	if resp.StatusCode >= 500 {
		return "", &Retryable{
			Provider: c.Name(), Reason: reasonHTTP5xx,
			Err: fmt.Errorf("status %d: %s", resp.StatusCode, redactBearer(truncate(raw, 256))),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return "", &Retryable{
			Provider: c.Name(),
			Reason:   fmt.Sprintf("http_%d", resp.StatusCode),
			Err:      fmt.Errorf("status %d: %s", resp.StatusCode, redactBearer(truncate(raw, 256))),
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

// Compile-time assertion that OpenAICompatibleClient satisfies JSONClient.
var _ JSONClient = (*OpenAICompatibleClient)(nil)
