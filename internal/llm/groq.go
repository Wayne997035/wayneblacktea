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

	"github.com/Wayne997035/wayneblacktea/internal/httpguard"
)

const (
	// groqEndpoint is the OpenAI-compatible chat completions endpoint.
	groqEndpoint = "https://api.groq.com/openai/v1/chat/completions"
	groqTimeout  = 30 * time.Second
	groqMaxBody  = 1 << 20

	// defaultGroqModel is the model used when GROQ_MODEL is unset.
	//
	// [GTD ab472814] This constant took production down for three days and it
	// is worth being explicit about why, because the next maintainer will be
	// tempted to treat a bump as the whole fix. It is not: a vendor model id
	// frozen in Go source is a dependency on someone else's deprecation
	// schedule, and it rots on THEIR calendar, not on ours.
	//
	// The previous value was llama-3.3-70b-versatile, inherited from
	// internal/discordbot/analyzer.go. Groq decommissioned it; every call
	// returned http_404 and all three LLM-backed features degraded to empty
	// while still answering HTTP 200.
	//
	// groq/compound is chosen because it is Groq's own first-party model, so
	// it is the entry on the menu least likely to be removed by a third-party
	// weights provider withdrawing. Verified against GET /v1/models and
	// exercised with this client's exact request shape (JSON mode on) before
	// being written here.
	//
	// What actually prevents a recurrence is NOT this line — it is
	// Chain.LogStartup warning about a chain with no fallback, and the
	// sustained-failure escalation in health.go. Both exist because this
	// constant is guaranteed to go stale again.
	defaultGroqModel = "groq/compound"
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
	safeClient := httpguard.NewSafeHTTPClient()
	// [GTD a3fcbeb3] SetClientBudget, not `safeClient.Timeout = …`. The
	// latter reads as "the budget is groqTimeout" and is not: it leaves the
	// Transport's ResponseHeaderTimeout at its conservative default, which is
	// what actually killed decision_draft in production at latency_ms=5011
	// while this line said 30s. groq/compound is agentic and thinks before it
	// answers, so time-to-first-header is most of the request.
	httpguard.SetClientBudget(safeClient, groqTimeout)

	return &GroqClient{
		apiKey:   cfg.APIKey,
		model:    model,
		http:     safeClient,
		endpoint: groqEndpoint,
	}, nil
}

// setEndpoint swaps the request URL — used only by tests.
func (c *GroqClient) setEndpoint(u string) {
	if u != "" {
		c.endpoint = u
		c.http = &http.Client{Timeout: groqTimeout}
	}
}

// Name implements JSONClient.
func (c *GroqClient) Name() string { return "groq" }

// Model implements modelNamer (health.go). A chain logged as [groq] was
// healthy as a route and dead as a model for three days; this is the field
// that makes those two states distinguishable. [GTD ab472814]
func (c *GroqClient) Model() string { return c.model }

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
