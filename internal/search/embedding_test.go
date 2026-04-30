package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"embedding": map[string]any{
				"values": wantVec,
			},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			http.Error(w, "encode error", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := newTestEmbedClient(srv.URL)
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		if _, err := w.Write([]byte(`{"error":"invalid API key"}`)); err != nil {
			_ = err // best-effort write
		}
	}))
	defer srv.Close()

	c := newTestEmbedClient(srv.URL)
	vec, err := c.Embed(context.Background(), "text")
	if err == nil {
		t.Error("expected error for 401 response, got nil")
	}
	if vec != nil {
		t.Errorf("expected nil vector on error, got %v", vec)
	}
}

func TestEmbeddingClient_MalformedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{not valid json`)); err != nil {
			_ = err
		}
	}))
	defer srv.Close()

	c := newTestEmbedClient(srv.URL)
	vec, err := c.Embed(context.Background(), "text")
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
	if vec != nil {
		t.Errorf("expected nil vector on error, got %v", vec)
	}
}

// newTestEmbedClient creates an EmbeddingClient that posts to baseURL instead of the real Gemini endpoint.
// It works by pointing the apiKey to something non-empty and replacing the HTTP client with one
// that uses the httptest server URL via a custom DialContext — but since the URL is overridden at
// request construction time (geminiEmbedURL + "?key=..."), the simplest approach is to create the
// client with a custom http.Client whose transport rewrites the URL host only.
func newTestEmbedClient(baseURL string) *EmbeddingClient {
	// We parse the test server URL to get host and scheme.
	// The embedding client constructs: geminiEmbedURL + "?key=apiKey"
	// We need requests to land at baseURL instead.
	// Use a simple http.Client with a custom CheckRedirect that does nothing,
	// and override the client transport by passing a testServer.Client().
	//
	// Simplest: pass a real API key and swap the underlying HTTP client for
	// one that knows about the test server.
	testClient := &http.Client{
		Timeout:   5 * time.Second,
		Transport: newHostRewriteTransport(baseURL),
	}
	return &EmbeddingClient{
		apiKey: "test-api-key",
		client: testClient,
	}
}

// hostRewriteTransport rewrites the host of every request to the target host.
type hostRewriteTransport struct {
	targetScheme string
	targetHost   string
}

func newHostRewriteTransport(targetURL string) *hostRewriteTransport {
	// Parse scheme and host from targetURL like "http://127.0.0.1:PORT"
	scheme := "http"
	host := targetURL
	if len(targetURL) > 7 && targetURL[:7] == "http://" {
		host = targetURL[7:]
	} else if len(targetURL) > 8 && targetURL[:8] == "https://" {
		scheme = "https"
		host = targetURL[8:]
	}
	return &hostRewriteTransport{targetScheme: scheme, targetHost: host}
}

func (t *hostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	r2.URL.Scheme = t.targetScheme
	r2.URL.Host = t.targetHost
	return http.DefaultTransport.RoundTrip(r2)
}
