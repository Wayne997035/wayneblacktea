package middleware

import (
	"net/http"

	"github.com/Wayne997035/wayneblacktea/internal/auth"
	"github.com/labstack/echo/v4"
)

// WbtSessionCookie is the name of the httpOnly browser session cookie.
// Must stay in sync with handler.WbtSessionCookie — they share the same value.
const WbtSessionCookie = "wbt_session"

// APIKeyMiddleware returns an Echo middleware that validates the caller's identity.
// It accepts two authentication paths in priority order:
//  1. X-API-Key header — used by MCP clients, CLI tools, and curl.
//  2. wbt_session cookie — used by the React SPA (httpOnly, Secure, SameSite=Strict).
//
// On success, an auth.Authorized capability token is stored in the Echo context
// under auth.ContextKey. Downstream handlers may retrieve it via auth.FromContext.
// The expected key is passed at construction time so it is read from env once at startup.
func APIKeyMiddleware(apiKey string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			rawKey := c.Request().Header.Get("X-API-Key")

			var cookieToken string
			cookie, err := c.Cookie(WbtSessionCookie)
			if err == nil {
				cookieToken = cookie.Value
			}

			authorized, authErr := auth.Authenticate(apiKey, rawKey, cookieToken)
			if authErr != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			}

			c.Set(auth.ContextKey, authorized)
			return next(c)
		}
	}
}
