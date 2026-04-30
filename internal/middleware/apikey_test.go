package middleware_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apimw "github.com/Wayne997035/wayneblacktea/internal/middleware"
	"github.com/labstack/echo/v4"
)

func setupEchoWithMiddleware(configuredKey string) *echo.Echo {
	e := echo.New()
	e.Use(apimw.APIKeyMiddleware(configuredKey))
	e.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	return e
}

func doRequest(e *echo.Echo, headerKey string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/test", nil)
	if headerKey != "" {
		req.Header.Set("X-API-Key", headerKey)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestAPIKeyMiddleware_ValidKey(t *testing.T) {
	const key = "super-secret-key"
	e := setupEchoWithMiddleware(key)
	rec := doRequest(e, key)
	if rec.Code != http.StatusOK {
		t.Errorf("valid key: got status %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAPIKeyMiddleware_WrongKey(t *testing.T) {
	e := setupEchoWithMiddleware("correct-key")
	rec := doRequest(e, "wrong-key")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong key: got status %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("response body not valid JSON: %v", err)
	}
	if _, ok := body["error"]; !ok {
		t.Errorf("response body missing 'error' field, got: %v", body)
	}
}

func TestAPIKeyMiddleware_EmptyKey(t *testing.T) {
	// Header not set at all — doRequest skips Set when headerKey is "".
	e := setupEchoWithMiddleware("real-key")
	rec := doRequest(e, "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("empty header: got status %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("response body not valid JSON: %v", err)
	}
	if _, ok := body["error"]; !ok {
		t.Errorf("response body missing 'error' field, got: %v", body)
	}
}

// TestAPIKeyMiddleware_EmptyConfig verifies the behaviour when both
// the configured key and the incoming header value are empty strings.
// The middleware now checks whether X-API-Key is present before comparing;
// an absent header falls through to cookie auth which also fails → 401.
// Callers MUST configure a non-empty API key at startup.
func TestAPIKeyMiddleware_EmptyConfig(t *testing.T) {
	e := setupEchoWithMiddleware("") // configured with empty string
	// Send request with no X-API-Key header and no cookie → both paths fail.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	// No valid credential supplied → 401.
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("empty config + empty header: got status %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
