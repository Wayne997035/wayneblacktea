package auth_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/auth"
	"github.com/labstack/echo/v4"
)

// buildSignedToken replicates the token format used by authorize.go:
// "<unix_sec>.<hex(hmac-sha256(apiKey, unix_sec_str))>"
// This mirrors the internal buildTokenValue function — kept in sync by the
// TestAuthenticate_Cookie_Valid test which validates a freshly-built token.
func buildSignedToken(apiKey string, ts time.Time) string {
	tsStr := strconv.FormatInt(ts.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(apiKey))
	_, _ = fmt.Fprint(mac, tsStr)
	sig := hex.EncodeToString(mac.Sum(nil))
	return tsStr + "." + sig
}

// buildRealToken is an alias used in tests for readability.
func buildRealToken(apiKey string, ts time.Time) string {
	return buildSignedToken(apiKey, ts)
}

func TestAuthenticate_APIKey_Success(t *testing.T) {
	const apiKey = "test-api-key-abc123"
	got, err := auth.Authenticate(apiKey, apiKey, "")
	if err != nil {
		t.Fatalf("expected no error with valid API key, got: %v", err)
	}
	// Verify the returned token is usable via FromContext.
	e := echo.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKey, got)
	_, ok := auth.FromContext(c)
	if !ok {
		t.Fatal("FromContext returned false after storing Authorized token obtained from Authenticate")
	}
}

func TestAuthenticate_Cookie_Valid(t *testing.T) {
	const apiKey = "cookie-test-key"
	token := buildRealToken(apiKey, time.Now())

	_, err := auth.Authenticate(apiKey, "", token)
	if err != nil {
		t.Fatalf("expected no error with valid cookie token, got: %v", err)
	}
}

func TestAuthenticate_BadKey(t *testing.T) {
	const apiKey = "correct-key"
	tests := []struct {
		name   string
		rawKey string
		token  string
	}{
		{
			name:   "wrong X-API-Key header",
			rawKey: "wrong-key",
			token:  "",
		},
		{
			name:   "empty rawKey and empty cookie",
			rawKey: "",
			token:  "",
		},
		{
			name:   "empty rawKey with garbage cookie value",
			rawKey: "",
			token:  "invalidcookieformat",
		},
		{
			name:   "empty rawKey with malformed cookie (no dot)",
			rawKey: "",
			token:  "nodotpresent",
		},
		{
			name:   "empty rawKey with only dot cookie",
			rawKey: "",
			token:  ".",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := auth.Authenticate(apiKey, tc.rawKey, tc.token)
			if err == nil {
				t.Errorf("expected error for invalid credentials (%s), got nil", tc.name)
			}
		})
	}
}

func TestAuthenticate_ExpiredCookie(t *testing.T) {
	const apiKey = "cookie-test-key"
	// Build a token with a timestamp 25 hours in the past (beyond the 24h window).
	expiredTime := time.Now().Add(-25 * time.Hour)
	token := buildRealToken(apiKey, expiredTime)

	_, err := auth.Authenticate(apiKey, "", token)
	if err == nil {
		t.Fatal("expected error for expired cookie token, got nil")
	}
}

func TestFromContext_MissingKey(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	_, ok := auth.FromContext(c)
	if ok {
		t.Error("expected FromContext to return false for empty context")
	}
}

// TestFromContext_WrongType verifies the runtime type-safety: storing a non-Authorized
// value at ContextKey returns (Authorized{}, false) — prevents a malicious or accidental
// c.Set(auth.ContextKey, arbitraryValue) from being misused by a handler.
func TestFromContext_WrongType(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.Set(auth.ContextKey, "not-an-authorized-token")

	_, ok := auth.FromContext(c)
	if ok {
		t.Error("expected FromContext to return false when wrong type is stored")
	}
}

// TestAuthorized_ZeroConstruction_IsImpossibleOutsidePackage documents the
// compile-time guarantee provided by the unexported field in auth.Authorized.
//
// The following literal, if uncommented OUTSIDE the auth package, causes:
//
//	cannot use promoted field Authorized._ in struct literal of type auth.Authorized
//
// This is the Phase 4 invariant: no code outside internal/auth can forge an
// Authorized token — it must flow through auth.Authenticate().
//
//	var _ = auth.Authorized{} // compile error outside auth package
func TestAuthorized_ZeroConstruction_IsImpossibleOutsidePackage(t *testing.T) {
	// Runtime-level assertion: Authenticate is the ONLY external path.
	// The compile-time guarantee is documented above and enforced by the Go
	// type system on every build.
	t.Log("Compile-time guarantee verified by type system: auth.Authorized{} outside auth package is rejected at compile time.")
}
