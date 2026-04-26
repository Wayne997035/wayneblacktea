package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/labstack/echo/v4"
)

// APIKeyMiddleware returns an Echo middleware that validates X-API-Key header.
// The expected key is passed at construction time so it is read from env once at startup.
func APIKeyMiddleware(apiKey string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			got := c.Request().Header.Get("X-API-Key")
			if subtle.ConstantTimeCompare([]byte(got), []byte(apiKey)) != 1 {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			}
			return next(c)
		}
	}
}
