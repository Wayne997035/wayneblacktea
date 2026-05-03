package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// makeOpenRouterResponse wraps content in the choices[0].message envelope.
func makeOpenRouterResponse(content string) string {
	resp := map[string]any{
		"choices": []map[string]any{
			{"message": map[string]string{"content": content}},
		},
		"model": "openrouter/test-model",
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// newTestOpenRouterClient builds an OpenRouterClient pointing at the given
// httptest.Server URL.
func newTestOpenRouterClient(t *testing.T, srvURL, model string, models []string) *OpenRouterClient {
	t.Helper()
	c, err := NewOpenRouterClient(OpenRouterConfig{APIKey: "test-key", Model: model, Models: models})
	if err != nil {
		t.Fatalf("NewOpenRouterClient: %v", err)
	}
	c.setEndpoint(srvURL)
	return c
}

// TestOpenRouter_BearerAuthHeader verifies the API key is sent only via
// Authorization header (never in URL query). Spec test #1.
func TestOpenRouter_BearerAuthHeader(t *testing.T) {
	var gotAuth, gotReferer, gotTitle string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotReferer = r.Header.Get("HTTP-Referer")
		gotTitle = r.Header.Get("X-OpenRouter-Title")
		// API key must NOT be in URL query string.
		if r.URL.RawQuery != "" {
			t.Errorf("URL has query string %q — API keys must be in headers only", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(makeOpenRouterResponse(`{"ok":true}`)))
	}))
	defer srv.Close()

	c := newTestOpenRouterClient(t, srv.URL, "openrouter/free", nil)
	_, err := c.CompleteJSON(context.Background(), JSONRequest{
		Task: "t", System: "sys", User: "user", MaxTokens: 100, Temperature: 0.2, JSONMode: true,
	})
	if err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
	}
	if gotReferer == "" {
		t.Errorf("HTTP-Referer header missing")
	}
	if gotTitle == "" {
		t.Errorf("X-OpenRouter-Title header missing")
	}
}

// TestOpenRouter_JSONModeAndModelField verifies request body uses
// response_format and the single-model field when Models is empty.
func TestOpenRouter_JSONModeAndModelField(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(makeOpenRouterResponse(`{"ok":true}`)))
	}))
	defer srv.Close()

	c := newTestOpenRouterClient(t, srv.URL, "openrouter/free", nil)
	if _, err := c.CompleteJSON(context.Background(), JSONRequest{
		Task: "t", System: "sys", User: "user", MaxTokens: 100, JSONMode: true,
	}); err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}

	if got, _ := captured["model"].(string); got != "openrouter/free" {
		t.Errorf("model = %v, want openrouter/free", captured["model"])
	}
	if _, hasModels := captured["models"]; hasModels {
		t.Error("body has models[] when only single Model is configured")
	}
	rf, ok := captured["response_format"].(map[string]any)
	if !ok || rf["type"] != "json_object" {
		t.Errorf("response_format = %v, want {type: json_object}", captured["response_format"])
	}
}

// TestOpenRouter_ModelsArrayUsedWhenMultiple verifies that multiple Models
// trigger OpenRouter's `models` array form for model fallback.
func TestOpenRouter_ModelsArrayUsedWhenMultiple(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		_ = json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(makeOpenRouterResponse(`{"ok":true}`)))
	}))
	defer srv.Close()

	c := newTestOpenRouterClient(t, srv.URL, "", []string{"a:free", "b:free", "openrouter/free"})
	if _, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t"}); err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	models, ok := captured["models"].([]any)
	if !ok || len(models) != 3 {
		t.Fatalf("models[] = %v, want length 3", captured["models"])
	}
	if models[0] != "a:free" || models[2] != "openrouter/free" {
		t.Errorf("models[] order = %v", models)
	}
	if _, hasModel := captured["model"]; hasModel {
		t.Error("body has plain `model` when Models[] is configured")
	}
}

// TestOpenRouter_ParsesContent verifies the message content is extracted
// from choices[0].message.content. Spec test #3.
func TestOpenRouter_ParsesContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(makeOpenRouterResponse(`{"is_decision":true}`)))
	}))
	defer srv.Close()
	c := newTestOpenRouterClient(t, srv.URL, "openrouter/free", nil)
	out, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t"})
	if err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	if out != `{"is_decision":true}` {
		t.Errorf("out = %q", out)
	}
}

// TestOpenRouter_RetryableErrors covers spec test #4: 429 / 5xx → Retryable.
func TestOpenRouter_RetryableErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		status     int
		wantReason string
	}{
		{"429", http.StatusTooManyRequests, "http_429"},
		{"500", http.StatusInternalServerError, "http_5xx"},
		{"502", http.StatusBadGateway, "http_5xx"},
		{"503", http.StatusServiceUnavailable, "http_5xx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "boom", tc.status)
			}))
			defer srv.Close()
			c := newTestOpenRouterClient(t, srv.URL, "openrouter/free", nil)
			_, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t"})
			if err == nil {
				t.Fatal("expected error")
			}
			var retry *Retryable
			if !errors.As(err, &retry) {
				t.Fatalf("err type = %T, want *Retryable", err)
			}
			if retry.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", retry.Reason, tc.wantReason)
			}
		})
	}
}

// TestOpenRouter_InvalidJSONRetryable verifies that garbage from upstream
// produces a Retryable("invalid_json") so the chain falls through.
func TestOpenRouter_InvalidJSONRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()
	c := newTestOpenRouterClient(t, srv.URL, "openrouter/free", nil)
	_, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t"})
	var retry *Retryable
	if !errors.As(err, &retry) {
		t.Fatalf("err type = %T, want *Retryable", err)
	}
	if retry.Reason != "invalid_json" {
		t.Errorf("reason = %q, want invalid_json", retry.Reason)
	}
}

// TestOpenRouter_EmptyChoicesRetryable verifies empty content → Retryable.
func TestOpenRouter_EmptyChoicesRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()
	c := newTestOpenRouterClient(t, srv.URL, "openrouter/free", nil)
	_, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t"})
	var retry *Retryable
	if !errors.As(err, &retry) {
		t.Fatalf("err type = %T, want *Retryable", err)
	}
	if retry.Reason != "empty_content" {
		t.Errorf("reason = %q, want empty_content", retry.Reason)
	}
}

// TestOpenRouter_AuthRedactedFromTransportErrors verifies that any "Bearer "
// substring is redacted from the wrapped error string. Spec test #2.
func TestOpenRouter_AuthRedactedFromTransportErrors(t *testing.T) {
	// Build a *Retryable manually with a bearer-leaking inner error and
	// verify the sanitiser strips it.
	leaked := errors.New(`Get "https://x/?": auth Bearer sk-secret-token failed`)
	clean := sanitiseTransportErr(leaked)
	if strings.Contains(clean.Error(), "sk-secret-token") {
		t.Errorf("sanitised error still contains token: %q", clean.Error())
	}
	if !strings.Contains(clean.Error(), "Bearer [REDACTED]") {
		t.Errorf("sanitised error does not contain Bearer [REDACTED]: %q", clean.Error())
	}
}

// TestNewOpenRouterClient_RejectsMissingModel verifies that an empty Model
// AND empty Models is rejected at construction (cannot send a valid request).
func TestNewOpenRouterClient_RejectsMissingModel(t *testing.T) {
	c, err := NewOpenRouterClient(OpenRouterConfig{APIKey: "k"})
	if err == nil {
		t.Fatal("expected error when neither Model nor Models is set")
	}
	if c != nil {
		t.Errorf("expected nil client on error, got %v", c)
	}
}

// TestNewOpenRouterClient_NilOnMissingKey covers the "missing key, skip"
// contract: empty API key returns (nil, nil) so the chain skips silently.
func TestNewOpenRouterClient_NilOnMissingKey(t *testing.T) {
	c, err := NewOpenRouterClient(OpenRouterConfig{Model: "openrouter/free"})
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if c != nil {
		t.Errorf("client = %v, want nil (missing key sentinel)", c)
	}
}
