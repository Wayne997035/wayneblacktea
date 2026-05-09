package search

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestNewEmbeddingClientFromEnv covers the env-based factory for all branches.
func TestNewEmbeddingClientFromEnv(t *testing.T) {
	t.Run("EMBEDDING_PROVIDER unset, GEMINI_API_KEY unset → nil", func(t *testing.T) {
		t.Setenv("EMBEDDING_PROVIDER", "")
		t.Setenv("GEMINI_API_KEY", "")
		c := NewEmbeddingClientFromEnv()
		if c != nil {
			t.Errorf("expected nil when no provider configured, got %T", c)
		}
	})

	t.Run("EMBEDDING_PROVIDER unset, GEMINI_API_KEY set → non-nil Gemini client", func(t *testing.T) {
		t.Setenv("EMBEDDING_PROVIDER", "")
		t.Setenv("GEMINI_API_KEY", "fake-gemini-key")
		c := NewEmbeddingClientFromEnv()
		if c == nil {
			t.Error("expected non-nil Gemini client when GEMINI_API_KEY is set, got nil")
		}
	})

	t.Run("EMBEDDING_PROVIDER=openai-compatible, BASE_URL set → non-nil client", func(t *testing.T) {
		// Use a non-IP hostname so constructor URL validation passes.
		t.Setenv("EMBEDDING_PROVIDER", "openai-compatible")
		t.Setenv("EMBEDDING_BASE_URL", "http://public-api.test:11434")
		t.Setenv("EMBEDDING_MODEL", "nomic-embed-text")
		t.Setenv("EMBEDDING_API_KEY", "")
		c := NewEmbeddingClientFromEnv()
		if c == nil {
			t.Error("expected non-nil openai-compatible client when EMBEDDING_BASE_URL is set, got nil")
		}
	})

	t.Run("EMBEDDING_PROVIDER=openai-compatible, BASE_URL empty → nil", func(t *testing.T) {
		t.Setenv("EMBEDDING_PROVIDER", "openai-compatible")
		t.Setenv("EMBEDDING_BASE_URL", "")
		t.Setenv("EMBEDDING_MODEL", "")
		t.Setenv("EMBEDDING_API_KEY", "")
		c := NewEmbeddingClientFromEnv()
		if c != nil {
			t.Errorf("expected nil when BASE_URL is empty, got %T", c)
		}
	})
}

func TestEmbeddingClient_NoAPIKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	c := NewEmbeddingClient()

	vec, err := c.Embed(context.Background(), "some text")
	if err != nil {
		t.Errorf("expected no error when API key is absent, got: %v", err)
	}
	if vec != nil {
		t.Errorf("expected nil vector when API key is absent, got: %v", vec)
	}
}

func TestEmbeddingClient_Success(t *testing.T) {
	wantVec := []float32{0.1, 0.2, 0.3}
	c := newTestEmbedClient(func(_ *http.Request) (*http.Response, error) {
		body, err := json.Marshal(map[string]any{
			"embedding": map[string]any{
				"values": wantVec,
			},
		})
		if err != nil {
			return nil, err
		}
		return jsonResponse(http.StatusOK, body), nil
	})

	vec, err := c.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != len(wantVec) {
		t.Fatalf("expected %d values, got %d", len(wantVec), len(vec))
	}
	for i, v := range vec {
		if v != wantVec[i] {
			t.Errorf("vec[%d]: got %f, want %f", i, v, wantVec[i])
		}
	}
}

func TestEmbeddingClient_APIError(t *testing.T) {
	c := newTestEmbedClient(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusUnauthorized, []byte(`{"error":"invalid API key"}`)), nil
	})
	vec, err := c.Embed(context.Background(), "text")
	if err == nil {
		t.Error("expected error for 401 response, got nil")
	}
	if vec != nil {
		t.Errorf("expected nil vector on error, got %v", vec)
	}
}

func TestEmbeddingClient_MalformedResponse(t *testing.T) {
	c := newTestEmbedClient(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, []byte(`{not valid json`)), nil
	})
	vec, err := c.Embed(context.Background(), "text")
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
	if vec != nil {
		t.Errorf("expected nil vector on error, got %v", vec)
	}
}

func newTestEmbedClient(roundTrip func(*http.Request) (*http.Response, error)) *EmbeddingClient {
	testClient := &http.Client{
		Timeout:   5 * time.Second,
		Transport: roundTripFunc(roundTrip),
	}
	return &EmbeddingClient{
		apiKey: "test-api-key",
		client: testClient,
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}
