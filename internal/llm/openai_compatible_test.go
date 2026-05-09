package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// makeOAICompatResponse wraps content in the choices[0].message envelope.
func makeOAICompatResponse(content string) string {
	resp := map[string]any{
		"choices": []map[string]any{
			{"message": map[string]string{"content": content}},
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// newTestOAICompatClient builds an OpenAICompatibleClient pointing at the given
// httptest.Server URL, bypassing URL validation for localhost test servers.
func newTestOAICompatClient(t *testing.T, srvURL, model, apiKey string) *OpenAICompatibleClient {
	t.Helper()
	return &OpenAICompatibleClient{
		apiKey:   apiKey,
		model:    model,
		http:     &http.Client{Timeout: openAICompatibleTimeout},
		endpoint: srvURL + "/v1/chat/completions",
	}
}

// TestOpenAICompatible_EmptyBaseURL verifies the "missing config, skip" sentinel:
// empty BaseURL returns (nil, nil) without error.
func TestOpenAICompatible_EmptyBaseURL(t *testing.T) {
	c, err := NewOpenAICompatibleClient(OpenAICompatibleConfig{
		BaseURL: "",
		Model:   "llama3.2",
		APIKey:  "key",
	})
	if err != nil {
		t.Errorf("expected nil error for empty BaseURL, got: %v", err)
	}
	if c != nil {
		t.Errorf("expected nil client for empty BaseURL, got non-nil")
	}
}

// TestOpenAICompatible_BlockedIPLiteral verifies that a cloud-metadata IP
// (169.254.169.254) is rejected at construction time.
func TestOpenAICompatible_BlockedIPLiteral(t *testing.T) {
	c, err := NewOpenAICompatibleClient(OpenAICompatibleConfig{
		BaseURL: "http://169.254.169.254/",
		Model:   "llama3.2",
		APIKey:  "key",
	})
	if err == nil {
		t.Fatal("expected error for link-local metadata IP, got nil")
	}
	if c != nil {
		t.Errorf("expected nil client on error, got non-nil")
	}
	if errMsg := err.Error(); errMsg == "" {
		t.Error("error message is empty")
	}
	// Ensure the word "blocked" appears so acceptance criteria #3 passes.
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error %q does not contain 'blocked'", err.Error())
	}
}

// TestOpenAICompatible_BlockedPrivateIP verifies that 192.168.x.x is NOT
// blocked at constructor time (only at SafeDial time) so that Ollama on a LAN
// can be configured without failing at startup.
// If the security tradeoff changes this test documents the expected behaviour.
func TestOpenAICompatible_BlockedPrivateIP(t *testing.T) {
	// 192.168.1.1 is RFC-1918 but NOT in the constructor-blocked ranges.
	// Constructor should succeed; SafeDial would block at connect time.
	c, err := NewOpenAICompatibleClient(OpenAICompatibleConfig{
		BaseURL: "http://192.168.1.1/",
		Model:   "llama3.2",
		APIKey:  "key",
	})
	// Constructor should succeed (private LAN is allowed at construction time).
	if err != nil {
		t.Errorf("unexpected error for private LAN IP at constructor time: %v", err)
	}
	if c == nil {
		t.Error("expected non-nil client for private LAN IP at constructor time")
	}
}

// TestOpenAICompatible_BearerAuthHeader verifies the API key is sent only via
// Authorization header (never in URL query).
func TestOpenAICompatible_BearerAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		// API key must NOT be in URL query string.
		if r.URL.RawQuery != "" {
			t.Errorf("URL has query string %q — API keys must be in headers only", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(makeOAICompatResponse("hello")))
	}))
	defer srv.Close()

	c := newTestOAICompatClient(t, srv.URL, "llama3.2", "test-key-abc")
	_, err := c.CompleteJSON(context.Background(), JSONRequest{
		Task: "test", System: "sys", User: "user", MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	if gotAuth != "Bearer test-key-abc" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key-abc")
	}
}

// TestOpenAICompatible_Success verifies a happy-path response is parsed correctly.
func TestOpenAICompatible_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(makeOAICompatResponse("hello")))
	}))
	defer srv.Close()

	c := newTestOAICompatClient(t, srv.URL, "llama3.2", "key")
	out, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t"})
	if err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	if out != "hello" {
		t.Errorf("out = %q, want %q", out, "hello")
	}
}

// TestOpenAICompatible_429Retryable verifies that a 429 response produces a *Retryable.
func TestOpenAICompatible_429Retryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := newTestOAICompatClient(t, srv.URL, "llama3.2", "key")
	_, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t"})
	if err == nil {
		t.Fatal("expected error for 429, got nil")
	}
	var retry *Retryable
	if !errors.As(err, &retry) {
		t.Fatalf("err type = %T, want *Retryable", err)
	}
	if retry.Reason != reasonHTTP429 {
		t.Errorf("reason = %q, want %q", retry.Reason, reasonHTTP429)
	}
}

// TestOpenAICompatible_EmptyChoices verifies that an empty choices array
// produces a *Retryable so the chain falls through.
func TestOpenAICompatible_EmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	c := newTestOAICompatClient(t, srv.URL, "llama3.2", "key")
	_, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t"})
	if err == nil {
		t.Fatal("expected error for empty choices, got nil")
	}
	var retry *Retryable
	if !errors.As(err, &retry) {
		t.Fatalf("err type = %T, want *Retryable", err)
	}
	if retry.Reason != reasonEmptyContent {
		t.Errorf("reason = %q, want %q", retry.Reason, reasonEmptyContent)
	}
}
