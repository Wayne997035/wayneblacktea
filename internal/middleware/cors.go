package middleware

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
)

// CORSMiddleware returns an Echo CORS middleware.
// allowedOrigins is a comma-separated list of explicit origins.
// AllowCredentials is set to true so that the browser can send the
// wbt_session httpOnly cookie in cross-origin requests.
//
// IMPORTANT: allowedOrigins must not be "*" or empty — wildcard origins are
// incompatible with AllowCredentials=true and browsers reject such responses.
// This function panics at startup when an unsafe value is detected to prevent
// silent misconfiguration in production.
func CORSMiddleware(allowedOrigins string) echo.MiddlewareFunc {
	if allowedOrigins == "*" || allowedOrigins == "" {
		// Wildcard origin with AllowCredentials=true is silently rejected by all
		// browsers and creates a false sense of security. Callers must set
		// ALLOWED_ORIGINS to explicit origins (e.g. https://app.example.com).
		panic("CORSMiddleware: ALLOWED_ORIGINS must not be '*' or empty when AllowCredentials=true; set ALLOWED_ORIGINS env var")
	}
	origins := strings.Split(allowedOrigins, ",")
	for i, o := range origins {
		origins[i] = strings.TrimSpace(o)
	}

	return echomiddleware.CORSWithConfig(echomiddleware.CORSConfig{
		AllowOrigins:     origins,
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowHeaders:     []string{"Content-Type", "X-API-Key"},
		AllowCredentials: true,
	})
}
