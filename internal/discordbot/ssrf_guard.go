package discordbot

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Wayne997035/wayneblacktea/internal/httpguard"
)

// ErrSSRF is a type alias for httpguard.ErrSSRF for backward compatibility.
// Existing callers that do errors.As(err, &ErrSSRF{}) continue to work.
type ErrSSRF = httpguard.ErrSSRF

// IsSafeURL delegates to httpguard.IsSafeURL.
// See httpguard for full documentation and the list of blocked ranges.
func IsSafeURL(ctx context.Context, rawURL string) (bool, error) {
	safe, err := httpguard.IsSafeURL(ctx, rawURL)
	if err != nil {
		return false, fmt.Errorf("%w", err)
	}
	return safe, nil
}

// NewSafeHTTPClient delegates to httpguard.NewSafeHTTPClient.
// Returns an *http.Client with SSRF-protected SafeDial transport and
// CheckRedirect that re-validates every redirect target.
func NewSafeHTTPClient() *http.Client {
	return httpguard.NewSafeHTTPClient()
}
