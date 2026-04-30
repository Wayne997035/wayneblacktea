package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/labstack/echo/v4"
)

func TestAuthSessionHandler_IssueSession(t *testing.T) {
	const apiKey = "test-api-key-123"

	e := echo.New()
	h := handler.NewAuthSessionHandler(apiKey)
	e.POST("/api/session", h.IssueSession)

	cases := []struct {
		name       string
		headerKey  string
		wantStatus int
		wantCookie bool
	}{
		{
			name:       "correct X-API-Key → 200 + cookie",
			headerKey:  apiKey,
			wantStatus: http.StatusOK,
			wantCookie: true,
		},
		{
			name:       "wrong X-API-Key → 401",
			headerKey:  "wrong-key",
			wantStatus: http.StatusUnauthorized,
			wantCookie: false,
		},
		{
			name:       "missing X-API-Key → 401",
			headerKey:  "",
			wantStatus: http.StatusUnauthorized,
			wantCookie: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/session", nil)
			if tc.headerKey != "" {
				req.Header.Set("X-API-Key", tc.headerKey)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("got status %d, want %d", rec.Code, tc.wantStatus)
			}

			if !tc.wantCookie {
				return
			}

			resp := rec.Result()
			var found *http.Cookie
			for _, c := range resp.Cookies() {
				if c.Name == handler.WbtSessionCookie {
					found = c
					break
				}
			}
			if found == nil {
				t.Fatalf("expected cookie %q, got none", handler.WbtSessionCookie)
			}
			if !found.HttpOnly {
				t.Error("cookie must be HttpOnly")
			}
			if found.SameSite != http.SameSiteStrictMode {
				t.Errorf("expected SameSite=Strict, got %v", found.SameSite)
			}
			if found.MaxAge <= 0 {
				t.Errorf("expected positive MaxAge, got %d", found.MaxAge)
			}
			// Validate the issued token is itself valid.
			if !handler.ValidateAuthTokenForTest(apiKey, found.Value) {
				t.Errorf("issued cookie value %q failed validation", found.Value)
			}
		})
	}
}

func TestAuthSessionHandler_EmptyAPIKey(t *testing.T) {
	e := echo.New()
	h := handler.NewAuthSessionHandler("")
	e.POST("/api/session", h.IssueSession)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/session", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("empty apiKey: got status %d, want 500", rec.Code)
	}
}

func TestAuthSessionHandler_SecureFlag(t *testing.T) {
	const apiKey = "test-key"

	cases := []struct {
		name           string
		forwardedProto string
		wantSecure     bool
	}{
		{"no X-Forwarded-Proto (local HTTP) → Secure=false", "", false},
		{"X-Forwarded-Proto: https → Secure=true", "https", true},
		{"X-Forwarded-Proto: http → Secure=false", "http", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			h := handler.NewAuthSessionHandler(apiKey)
			e.POST("/api/session", h.IssueSession)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/session", nil)
			req.Header.Set("X-API-Key", apiKey)
			if tc.forwardedProto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.forwardedProto)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("got status %d, want 200", rec.Code)
			}
			var found *http.Cookie
			for _, c := range rec.Result().Cookies() {
				if c.Name == handler.WbtSessionCookie {
					found = c
					break
				}
			}
			if found == nil {
				t.Fatal("cookie not set")
			}
			if found.Secure != tc.wantSecure {
				t.Errorf("Secure=%v, want %v", found.Secure, tc.wantSecure)
			}
		})
	}
}

func TestValidateAuthToken_RoundTrip(t *testing.T) {
	const apiKey = "secret-key"
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	token := handler.BuildAuthTokenForTest(apiKey, ts)

	cases := []struct {
		name      string
		apiKey    string
		token     string
		wantValid bool
	}{
		{
			name:      "correct key and fresh token → valid",
			apiKey:    apiKey,
			token:     token,
			wantValid: true,
		},
		{
			name:      "wrong apiKey → invalid",
			apiKey:    "wrong-key",
			token:     token,
			wantValid: false,
		},
		{
			name:      "empty token → invalid",
			apiKey:    apiKey,
			token:     "",
			wantValid: false,
		},
		{
			name:      "no dot separator → invalid",
			apiKey:    apiKey,
			token:     "nodothere",
			wantValid: false,
		},
		{
			name:      "dot at start → invalid",
			apiKey:    apiKey,
			token:     ".abc",
			wantValid: false,
		},
		{
			name:      "dot at end → invalid",
			apiKey:    apiKey,
			token:     "12345.",
			wantValid: false,
		},
		{
			name:      "non-numeric timestamp → invalid",
			apiKey:    apiKey,
			token:     "abc.defsig",
			wantValid: false,
		},
		{
			name:      "expired token → invalid",
			apiKey:    apiKey,
			token:     handler.BuildAuthTokenForTest(apiKey, strconv.FormatInt(time.Now().Add(-25*time.Hour).Unix(), 10)),
			wantValid: false,
		},
		{
			name:      "tampered signature → invalid",
			apiKey:    apiKey,
			token:     ts + ".deadbeef00000000000000000000000000000000000000000000000000000000",
			wantValid: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := handler.ValidateAuthTokenForTest(tc.apiKey, tc.token)
			if got != tc.wantValid {
				t.Errorf("ValidateAuthTokenForTest(%q) = %v, want %v", tc.token, got, tc.wantValid)
			}
		})
	}
}
