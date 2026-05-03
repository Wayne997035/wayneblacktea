package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// makeGroqResponse mirrors the Groq chat completion envelope.
func makeGroqResponse(content string) string {
	resp := map[string]any{
		"choices": []map[string]any{
			{"message": map[string]string{"content": content}},
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

func newTestGroqClient(t *testing.T, srvURL, model string) *GroqClient {
	t.Helper()
	c, err := NewGroqClient(GroqConfig{APIKey: "test-key", Model: model})
	if err != nil {
		t.Fatalf("NewGroqClient: %v", err)
	}
	c.setEndpoint(srvURL)
	return c
}

// TestGroq_BearerAuth verifies API key sent in Authorization header only.
func TestGroq_BearerAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(makeGroqResponse(`{"ok":true}`)))
	}))
	defer srv.Close()
	c := newTestGroqClient(t, srv.URL, "")
	if _, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t"}); err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("auth = %q", gotAuth)
	}
}

// TestGroq_ParsesContent verifies content extraction.
func TestGroq_ParsesContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(makeGroqResponse(`{"x":1}`)))
	}))
	defer srv.Close()
	c := newTestGroqClient(t, srv.URL, "")
	out, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t"})
	if err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	if out != `{"x":1}` {
		t.Errorf("out = %q", out)
	}
}

// TestGroq_RetryableErrors covers 429 / 5xx classification.
func TestGroq_RetryableErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		status     int
		wantReason string
	}{
		{"429", http.StatusTooManyRequests, "http_429"},
		{"500", http.StatusInternalServerError, "http_5xx"},
		{"503", http.StatusServiceUnavailable, "http_5xx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "boom", tc.status)
			}))
			defer srv.Close()
			c := newTestGroqClient(t, srv.URL, "")
			_, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t"})
			var retry *Retryable
			if !errors.As(err, &retry) {
				t.Fatalf("err type = %T", err)
			}
			if retry.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", retry.Reason, tc.wantReason)
			}
		})
	}
}

// TestGroq_DefaultModel verifies that an empty Model defaults to the legacy
// llama-3.3-70b-versatile so existing /analyze users see no behaviour change.
func TestGroq_DefaultModel(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dec := json.NewDecoder(r.Body)
		_ = dec.Decode(&got)
		_, _ = w.Write([]byte(makeGroqResponse(`{"ok":true}`)))
	}))
	defer srv.Close()
	c := newTestGroqClient(t, srv.URL, "")
	if _, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "t"}); err != nil {
		t.Fatalf("CompleteJSON: %v", err)
	}
	if got["model"] != "llama-3.3-70b-versatile" {
		t.Errorf("default model = %v, want llama-3.3-70b-versatile", got["model"])
	}
}

// TestNewGroqClient_NilOnMissingKey covers the missing-key sentinel.
func TestNewGroqClient_NilOnMissingKey(t *testing.T) {
	c, err := NewGroqClient(GroqConfig{})
	if err != nil {
		t.Errorf("err = %v", err)
	}
	if c != nil {
		t.Errorf("client = %v, want nil", c)
	}
}
