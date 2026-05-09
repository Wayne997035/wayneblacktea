package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// makeOpenAIEmbedResponse builds a /v1/embeddings response JSON.
func makeOpenAIEmbedResponse(embedding []float32) string {
	resp := map[string]any{
		"data": []map[string]any{
			{"embedding": embedding},
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

// newTestEmbedding builds an OpenAICompatibleEmbeddingClient pointing at
// the given httptest.Server URL, bypassing URL validation for test servers.
func newTestEmbedding(srvURL, model, apiKey string) *OpenAICompatibleEmbeddingClient {
	return &OpenAICompatibleEmbeddingClient{
		apiKey:   apiKey,
		model:    model,
		endpoint: srvURL + "/v1/embeddings",
		http:     &http.Client{Timeout: openAIEmbedTimeout},
	}
}

// TestOpenAIEmbed_EmptyBaseURL verifies (nil, nil) is returned for empty URL.
func TestOpenAIEmbed_EmptyBaseURL(t *testing.T) {
	c, err := NewOpenAICompatibleEmbeddingClient("", "model", "key")
	if err != nil {
		t.Errorf("expected nil error for empty BaseURL, got: %v", err)
	}
	if c != nil {
		t.Errorf("expected nil client for empty BaseURL, got non-nil")
	}
}

// TestOpenAIEmbed_BlockedIPLiteral verifies that 169.254.169.254 (cloud
// metadata) is rejected at construction time with an error.
func TestOpenAIEmbed_BlockedIPLiteral(t *testing.T) {
	c, err := NewOpenAICompatibleEmbeddingClient("http://169.254.169.254/", "model", "")
	if err == nil {
		t.Fatal("expected error for link-local metadata IP, got nil")
	}
	if c != nil {
		t.Errorf("expected nil client on SSRF error, got non-nil")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error %q does not contain 'blocked'", err.Error())
	}
}

// TestOpenAIEmbed_BearerAuthHeader verifies the API key is sent only via
// Authorization header (never in URL query).
func TestOpenAIEmbed_BearerAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.RawQuery != "" {
			t.Errorf("URL has query string %q — API keys must be in headers only", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(makeOpenAIEmbedResponse([]float32{0.1, 0.2, 0.3})))
	}))
	defer srv.Close()

	c := newTestEmbedding(srv.URL, "nomic-embed-text", "secret-key")
	_, err := c.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-key")
	}
}

// TestOpenAIEmbed_Success verifies the happy-path float32 slice is returned.
func TestOpenAIEmbed_Success(t *testing.T) {
	wantVec := []float32{0.1, 0.2, 0.3}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(makeOpenAIEmbedResponse(wantVec)))
	}))
	defer srv.Close()

	c := newTestEmbedding(srv.URL, "model", "key")
	got, err := c.Embed(context.Background(), "some text")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != len(wantVec) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(wantVec))
	}
	for i, v := range got {
		if v != wantVec[i] {
			t.Errorf("got[%d] = %f, want %f", i, v, wantVec[i])
		}
	}
}

// TestOpenAIEmbed_NonOK_BodyRedacted verifies that a non-OK response body with
// a Bearer token does NOT expose the raw token in the error message.
// Uses a JWT-style Bearer token (eyJ...) which matches credentialRe's
// `Bearer\s+ey[A-Za-z0-9._-]+` pattern.
func TestOpenAIEmbed_NonOK_BodyRedacted(t *testing.T) {
	// rawToken is a fake JWT used solely to test redaction; it is NOT a real
	// credential. Declared as a var (not const) to avoid gosec G101 false-positive.
	rawToken := "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.secret" //nolint:gosec // reason: test-only fake token, not a real credential
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"auth failed","detail":"Bearer ` + rawToken + ` is invalid"}`))
	}))
	defer srv.Close()

	c := newTestEmbedding(srv.URL, "model", "key")
	_, err := c.Embed(context.Background(), "text")
	if err == nil {
		t.Fatal("expected error for 503, got nil")
	}
	errMsg := err.Error()
	if strings.Contains(errMsg, rawToken) {
		t.Errorf("error message leaks raw token: %q", errMsg)
	}
	if !strings.Contains(errMsg, "503") {
		t.Errorf("error message missing status code: %q", errMsg)
	}
}

// TestOpenAIEmbed_CredentialScrubbed verifies that credential patterns in the
// input text are scrubbed before being sent to the upstream endpoint.
func TestOpenAIEmbed_CredentialScrubbed(t *testing.T) {
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		capturedBody = string(buf[:n])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(makeOpenAIEmbedResponse([]float32{0.5})))
	}))
	defer srv.Close()

	c := newTestEmbedding(srv.URL, "model", "key")
	sensitiveText := "user said: password=secret123 is the key"
	_, err := c.Embed(context.Background(), sensitiveText)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if strings.Contains(capturedBody, "secret123") {
		t.Errorf("upstream request contains unredacted credential: %q", capturedBody)
	}
	if !strings.Contains(capturedBody, "[REDACTED]") {
		t.Errorf("upstream request missing [REDACTED] placeholder: %q", capturedBody)
	}
}
