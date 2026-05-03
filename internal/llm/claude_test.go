package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// makeClaudeResponse wraps the given text in the Anthropic Messages envelope.
func makeClaudeResponse(text string) string {
	resp := map[string]any{
		"id":    "msg_test",
		"type":  "message",
		"role":  "assistant",
		"model": "claude-haiku-4-5",
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage":         map[string]any{"input_tokens": 1, "output_tokens": 1},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// newTestClaudeClient builds a ClaudeClient via the SDK pointed at a
// httptest.Server (matches the pattern in internal/ai/*_test.go).
func newTestClaudeClient(srvURL string) *ClaudeClient {
	c := anthropic.NewClient(
		option.WithAPIKey("test-key"),
		option.WithBaseURL(srvURL),
	)
	return NewClaudeClientWithSDK(&c, "claude-haiku-4-5")
}

// TestClaude_ParsesText verifies the first content block's text is returned.
func TestClaude_ParsesText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(makeClaudeResponse(`{"is_decision":true}`)))
	}))
	defer srv.Close()
	c := newTestClaudeClient(srv.URL)
	out, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t", User: "x"})
	if err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	if out != `{"is_decision":true}` {
		t.Errorf("out = %q", out)
	}
}

// TestClaude_RetryableOn5xx verifies that an HTTP 500 from the upstream is
// classified as Retryable so the chain falls through.
func TestClaude_RetryableOn5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"type":"error"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := newTestClaudeClient(srv.URL)
	_, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t", User: "x"})
	var retry *Retryable
	if !errors.As(err, &retry) {
		t.Fatalf("err type = %T", err)
	}
	if retry.Reason != "http_5xx" {
		t.Errorf("reason = %q, want http_5xx", retry.Reason)
	}
}

// TestClaude_RetryableOn429 verifies 429 → http_429.
func TestClaude_RetryableOn429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"type":"error"}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := newTestClaudeClient(srv.URL)
	_, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t", User: "x"})
	var retry *Retryable
	if !errors.As(err, &retry) {
		t.Fatalf("err type = %T", err)
	}
	if retry.Reason != "http_429" {
		t.Errorf("reason = %q, want http_429", retry.Reason)
	}
}

// TestClaude_EmptyContentRetryable verifies an empty content array is
// classified as empty_content.
func TestClaude_EmptyContentRetryable(t *testing.T) {
	emptyResp, _ := json.Marshal(map[string]any{
		"id":          "msg",
		"type":        "message",
		"role":        "assistant",
		"model":       "claude-haiku-4-5",
		"content":     []map[string]any{},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 1, "output_tokens": 0},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(emptyResp)
	}))
	defer srv.Close()
	c := newTestClaudeClient(srv.URL)
	_, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t", User: "x"})
	var retry *Retryable
	if !errors.As(err, &retry) {
		t.Fatalf("err type = %T", err)
	}
	if retry.Reason != "empty_content" {
		t.Errorf("reason = %q, want empty_content", retry.Reason)
	}
}

// TestNewClaudeClient_NilOnMissingKey covers the missing-key sentinel.
func TestNewClaudeClient_NilOnMissingKey(t *testing.T) {
	c, err := NewClaudeClient(ClaudeConfig{})
	if err != nil {
		t.Errorf("err = %v", err)
	}
	if c != nil {
		t.Errorf("client = %v, want nil", c)
	}
}
