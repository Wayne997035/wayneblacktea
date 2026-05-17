// Package auth provides the compile-time capability token for authenticated requests.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
)

// sessionTokenWindow is the maximum age of a wbt_session token we will accept.
const sessionTokenWindow = 24 * time.Hour

// Authorized is an opaque capability token proving a request passed auth.
// The unexported field prevents external construction — only Authenticate()
// can produce one, so any handler that receives Authorized is guaranteed
// to have gone through the auth middleware.
//
// Compile-time guarantee: auth.Authorized{} OUTSIDE this package is a compile
// error because the unnamed field _ is unexported. External callers MUST obtain
// an Authorized value via Authenticate() or FromContext().
type Authorized struct {
	_ struct{}
}

// ContextKey is the Echo context key used to store/retrieve the Authorized token.
const ContextKey = "wbt_authorized"

// ErrUnauthorized is returned by Authenticate when no valid credential is found.
var ErrUnauthorized = errors.New("unauthorized")

// Authenticate validates the provided credentials and returns an Authorized token.
//
// apiKey is the expected API key (configured at startup).
// rawKey is the value from the X-API-Key header (may be empty).
// token is the wbt_session cookie value (may be empty).
//
// Returns Authorized{} on success, ErrUnauthorized on failure.
func Authenticate(apiKey, rawKey, token string) (Authorized, error) {
	// Path 1: X-API-Key header (MCP / CLI / curl).
	if rawKey != "" {
		if subtle.ConstantTimeCompare([]byte(rawKey), []byte(apiKey)) == 1 {
			return Authorized{}, nil
		}
		return Authorized{}, ErrUnauthorized
	}

	// Path 2: wbt_session cookie (browser SPA).
	if token != "" && validateSessionToken(apiKey, token) {
		return Authorized{}, nil
	}

	return Authorized{}, ErrUnauthorized
}

// FromContext retrieves the Authorized token from an Echo context.
// Returns (Authorized{}, false) if the context does not carry a valid token —
// this provides runtime type-safe enforcement: even if a malicious caller
// stores a non-Authorized value at ContextKey, the type assertion fails safely.
func FromContext(c echo.Context) (Authorized, bool) {
	v := c.Get(ContextKey)
	if v == nil {
		return Authorized{}, false
	}
	a, ok := v.(Authorized)
	return a, ok
}

// validateSessionToken checks that the cookie value is a valid, non-expired
// HMAC-signed token produced by IssueSession.
func validateSessionToken(apiKey, token string) bool {
	dotIdx := -1
	for i, ch := range token {
		if ch == '.' {
			dotIdx = i
			break
		}
	}
	if dotIdx <= 0 || dotIdx == len(token)-1 {
		return false
	}
	ts := token[:dotIdx]
	unixSec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if time.Since(time.Unix(unixSec, 0)) > sessionTokenWindow {
		return false
	}
	expected := buildTokenValue(apiKey, ts)
	return hmac.Equal([]byte(token), []byte(expected))
}

// buildTokenValue constructs the signed token string: "<ts>.<hmac>".
func buildTokenValue(apiKey, ts string) string {
	mac := hmac.New(sha256.New, []byte(apiKey))
	_, _ = fmt.Fprint(mac, ts)
	sig := hex.EncodeToString(mac.Sum(nil))
	return ts + "." + sig
}
