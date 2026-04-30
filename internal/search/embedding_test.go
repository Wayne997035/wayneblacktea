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
