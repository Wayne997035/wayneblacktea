package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
)

const (
	// WbtSessionCookie is the name of the httpOnly browser session cookie.
	// Kept in sync with middleware.WbtSessionCookie — they share the same value.
	WbtSessionCookie = "wbt_session"
	// sessionCookieTTL is how long the browser session cookie lives.
	sessionCookieTTL = 24 * time.Hour
)

// AuthSessionHandler issues browser session cookies so that the React SPA
// never needs to know the raw API_KEY.
type AuthSessionHandler struct {
	apiKey string
}

// NewAuthSessionHandler creates an AuthSessionHandler using the given API key
// as the HMAC signing secret.
func NewAuthSessionHandler(apiKey string) *AuthSessionHandler {
	return &AuthSessionHandler{apiKey: apiKey}
}

// IssueSession signs a short-lived session token with HMAC-SHA256(apiKey, ts)
// and sets it as an httpOnly, Secure, SameSite=Strict cookie.
// The caller must present the raw API key in the X-API-Key header; this
// prevents unauthenticated third parties from minting session cookies.
func (h *AuthSessionHandler) IssueSession(c echo.Context) error {
	if h.apiKey == "" {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "server misconfiguration"})
	}
	key := c.Request().Header.Get("X-API-Key")
	if !hmac.Equal([]byte(key), []byte(h.apiKey)) {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	token := buildAuthToken(h.apiKey, ts)

	// Secure=true only when the request arrived over HTTPS (production/Railway).
	// Local dev over plain HTTP leaves the flag false so the browser accepts it.
	secure := c.Request().Header.Get("X-Forwarded-Proto") == "https" || c.Request().TLS != nil

	cookie := &http.Cookie{
		Name:     WbtSessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionCookieTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
	c.SetCookie(cookie)
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// buildAuthToken constructs the signed token string: "<ts>.<hmac>".
func buildAuthToken(apiKey, ts string) string {
	mac := hmac.New(sha256.New, []byte(apiKey))
	_, _ = fmt.Fprint(mac, ts)
	sig := hex.EncodeToString(mac.Sum(nil))
	return ts + "." + sig
}

// validateAuthToken checks that the cookie value is a valid, non-expired
// HMAC-signed token produced by IssueSession.
func validateAuthToken(apiKey, token string) bool {
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
	if time.Since(time.Unix(unixSec, 0)) > sessionCookieTTL {
		return false
	}
	expected := buildAuthToken(apiKey, ts)
	return hmac.Equal([]byte(token), []byte(expected))
}
