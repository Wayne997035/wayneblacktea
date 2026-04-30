package middleware

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	echomiddleware "github.com/labstack/echo/v4/middleware"
)

// CORSMiddleware returns an Echo CORS middleware.
// allowedOrigins is a comma-separated list of origins; "*" allows all.
// AllowCredentials is set to true so that the browser can send the
// wbt_session httpOnly cookie in cross-origin requests (e.g. local dev).
// Note: AllowCredentials=true is incompatible with AllowOrigins=["*"] in
// browsers; callers must set ALLOWED_ORIGINS to explicit origins in production.
func CORSMiddleware(allowedOrigins string) echo.MiddlewareFunc {
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
