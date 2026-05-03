package llm

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	// defaultClaudeModel matches the existing internal/ai default
	// (claude-haiku-4-5) so the chain has parity with the legacy
	// classifier/reviewer/analyzer call sites.
	defaultClaudeModel = "claude-haiku-4-5"
	claudeTimeout      = 30 * time.Second
)

// ClaudeClient is a JSONClient that wraps the Anthropic SDK's Messages API.
// We do NOT duplicate model defaults from internal/ai — feature packages
// (ActivityClassifier, ConceptReviewer) keep their own MaxTokens / model
// constants and pass them through JSONRequest.
type ClaudeClient struct {
	client *anthropic.Client
	model  string
}

// ClaudeConfig holds construction inputs read from env. Empty Model defaults
// to defaultClaudeModel.
type ClaudeConfig struct {
	APIKey string
	Model  string
}

// NewClaudeClient builds a ClaudeClient. Returns (nil, nil) when APIKey is
// empty so the caller can `if c == nil { skip }`.
func NewClaudeClient(cfg ClaudeConfig) (*ClaudeClient, error) {
	if cfg.APIKey == "" {
		return nil, nil //nolint:nilnil // documented "missing key, skip" sentinel
	}
	model := cfg.Model
	if model == "" {
		model = defaultClaudeModel
	}
	c := anthropic.NewClient(option.WithAPIKey(cfg.APIKey))
	return &ClaudeClient{client: &c, model: model}, nil
}

// NewClaudeClientWithSDK is intended for tests with a pre-configured client
// (e.g. option.WithBaseURL pointing at a httptest.Server). It bypasses the
// API-key check.
func NewClaudeClientWithSDK(client *anthropic.Client, model string) *ClaudeClient {
	if model == "" {
		model = defaultClaudeModel
	}
	return &ClaudeClient{client: client, model: model}
}

// Name implements JSONClient.
func (c *ClaudeClient) Name() string { return "claude" }

// CompleteJSON sends a single Messages.New call and returns the first text
// block. Errors are wrapped in *Retryable so the chain falls through.
func (c *ClaudeClient) CompleteJSON(ctx context.Context, req JSONRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, claudeTimeout)
	defer cancel()

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	resp, err := c.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: int64(maxTokens),
		System: []anthropic.TextBlockParam{
			{Text: req.System},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(req.User)),
		},
	})
	if err != nil {
		// Use Anthropic SDK's own error-classification surface. Most
		// transport / 5xx errors come back as plain errors; we tag the
		// reason heuristically based on the error string and treat all of
		// them as retryable so the chain falls through.
		return "", &Retryable{Provider: c.Name(), Reason: classifyClaudeErr(err), Err: sanitiseTransportErr(err)}
	}
	if len(resp.Content) == 0 {
		return "", &Retryable{Provider: c.Name(), Reason: reasonEmptyContent, Err: errors.New("no content blocks")}
	}
	text := resp.Content[0].Text
	if text == "" {
		return "", &Retryable{Provider: c.Name(), Reason: reasonEmptyContent, Err: errors.New("empty text block")}
	}
	return text, nil
}

// classifyClaudeErr maps an Anthropic SDK error to a chain reason label.
// We avoid importing the SDK's typed error tree to keep this layer thin;
// the error string already includes "429" / "5xx" markers when relevant.
func classifyClaudeErr(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return reasonTimeout
	}
	if errors.Is(err, context.Canceled) {
		return reasonCancelled
	}
	type timeoutErr interface{ Timeout() bool }
	var te timeoutErr
	if errors.As(err, &te) && te.Timeout() {
		return reasonTimeout
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "429"):
		return reasonHTTP429
	case strings.Contains(msg, "500"),
		strings.Contains(msg, "502"),
		strings.Contains(msg, "503"),
		strings.Contains(msg, "504"):
		return reasonHTTP5xx
	}
	return reasonProviderErr
}

// Compile-time assertion that ClaudeClient satisfies JSONClient.
var _ JSONClient = (*ClaudeClient)(nil)
