// Package llm provides a provider-neutral abstraction for JSON text-generation
// LLM tasks (e.g. activity classification, content analysis, concept review).
//
// The package intentionally returns raw model text — feature-specific packages
// own their own prompts and result validation. The provider layer only sends
// messages and returns the model's textual response, plus a typed retry hint
// so the chain layer can decide whether to fall through to the next provider.
//
// SECURITY: provider implementations MUST
//   - keep API keys in headers only (never in URL query strings),
//   - apply io.LimitReader (1 MiB) to every response body,
//   - apply context.WithTimeout per call (not just http.Client.Timeout),
//   - never log Authorization headers, prompt bodies, or response bodies.
package llm

import "context"

// JSONRequest is the provider-neutral input to a single JSON completion call.
// Task is a short string identifier ("classify", "analyze", "review") used
// only for log labelling — it is not sent to the model.
type JSONRequest struct {
	Task        string
	System      string
	User        string
	MaxTokens   int
	Temperature float64
	JSONMode    bool
}

// JSONClient is the contract every provider implements. CompleteJSON returns
// the model's raw text on success; the caller decodes/validates the JSON.
//
// Errors SHOULD be wrapped in a Retryable when the chain layer should fall
// through to the next provider (timeout, network, 429, 5xx, empty content,
// invalid JSON). Permanent errors (e.g. 401 unauthorised on a misconfigured
// key) MAY be returned plain — the chain still falls through but the failure
// is logged at WARN per provider, ERROR only when the whole chain fails.
type JSONClient interface {
	// Name returns a stable provider identifier ("openrouter", "claude",
	// "groq") for log labelling. It MUST NOT include keys or secrets.
	Name() string
	CompleteJSON(ctx context.Context, req JSONRequest) (string, error)
}

// Reason labels used by Retryable.Reason. Centralised so the chain log
// vocabulary is stable and grep-able.
const (
	reasonTimeout      = "timeout"
	reasonCancelled    = "cancelled"
	reasonNetwork      = "network"
	reasonHTTP429      = "http_429"
	reasonHTTP5xx      = "http_5xx"
	reasonInvalidJSON  = "invalid_json"
	reasonEmptyContent = "empty_content"
	reasonProviderErr  = "provider_error"
)

// Retryable wraps an error that the chain layer SHOULD treat as a signal to
// fall through to the next provider. It is intentionally minimal — chain.go
// uses errors.As to detect it, never unwraps further than needed.
type Retryable struct {
	// Provider is the provider that produced this error (e.g. "openrouter").
	Provider string
	// Reason is a short, sanitised human-readable label used in logs.
	// Examples: "timeout", "network", "http_429", "http_5xx", "empty_content",
	// "invalid_json".
	Reason string
	// Err is the underlying transport / decode error. Sanitised by the
	// provider layer — must not contain Authorization headers, full URLs
	// with query secrets, or full response bodies.
	Err error
}

// Error returns a sanitised description suitable for slog warn/error.
// Format: "<provider>: <reason>: <wrapped error>".
func (r *Retryable) Error() string {
	if r.Err == nil {
		return r.Provider + ": " + r.Reason
	}
	return r.Provider + ": " + r.Reason + ": " + r.Err.Error()
}

// Unwrap exposes the underlying error for errors.Is / errors.As callers.
func (r *Retryable) Unwrap() error { return r.Err }
