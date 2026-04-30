package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
)

// WbtSessionCookie is the name of the httpOnly browser session cookie.
// Must stay in sync with handler.WbtSessionCookie — they share the same value.
const WbtSessionCookie = "wbt_session"

// sessionTokenWindow is the maximum age of a wbt_session token we will accept.
const sessionTokenWindow = 24 * time.Hour

// APIKeyMiddleware returns an Echo middleware that validates the caller's identity.
// It accepts two authentication paths in priority order:
//  1. X-API-Key header — used by MCP clients, CLI tools, and curl.
//  2. wbt_session cookie — used by the React SPA (httpOnly, Secure, SameSite=Strict).
//
// The expected key is passed at construction time so it is read from env once at startup.
func APIKeyMiddleware(apiKey string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// Path 1: X-API-Key header (MCP / CLI / curl).
			if got := c.Request().Header.Get("X-API-Key"); got != "" {
				if subtle.ConstantTimeCompare([]byte(got), []byte(apiKey)) == 1 {
					return next(c)
				}
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			}

			// Path 2: wbt_session cookie (browser SPA).
			cookie, err := c.Cookie(WbtSessionCookie)
			if err == nil && validateSessionToken(apiKey, cookie.Value) {
				return next(c)
			}

			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		}
	}
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
