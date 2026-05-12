package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestCORSMiddleware_PanicsOnWildcardOrigin(t *testing.T) {
	assertCORSPanics(t, "*")
}

func TestCORSMiddleware_PanicsOnEmptyOrigin(t *testing.T) {
	assertCORSPanics(t, "")
}

func assertCORSPanics(t *testing.T, origins string) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("CORSMiddleware(%q) did not panic", origins)
		}
	}()
	_ = CORSMiddleware(origins)
}

func TestCORSMiddleware_AllowsListedOriginAndSetsCredentialsHeader(t *testing.T) {
	e := echo.New()
	e.Use(CORSMiddleware("https://app.example.com"))
	e.OPTIONS("/api/test", func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/api/test", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want true", got)
	}
}

func TestCORSMiddleware_RejectsUnlistedOrigin(t *testing.T) {
	e := echo.New()
	e.Use(CORSMiddleware("https://app.example.com"))
	e.OPTIONS("/api/test", func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/api/test", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want empty", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("Access-Control-Allow-Credentials = %q, want empty", got)
	}
}
