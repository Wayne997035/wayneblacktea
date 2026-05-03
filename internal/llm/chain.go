package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Chain wires multiple JSONClient providers in priority order.
// CompleteJSON tries each provider until one succeeds. A Retryable error
// from a provider triggers the next attempt; a non-Retryable error is also
// treated as a fall-through (we never block the user on one bad provider)
// but is logged at WARN with provider name + sanitised reason.
//
// When the chain is empty, CompleteJSON returns ErrNoProviders so callers can
// gracefully degrade (e.g. ActivityClassifier returns a zero-value result).
type Chain struct {
	providers []JSONClient
	// now is overridable for tests; production uses time.Now.
	now func() time.Time
}

// ErrNoProviders is returned by CompleteJSON when the chain has no providers
// configured. Callers should treat this the same as "all providers failed":
// log a warn and degrade gracefully.
var ErrNoProviders = errors.New("llm: no providers configured")

// ErrAllProvidersFailed wraps the final provider's error after every provider
// in the chain has been tried and rejected.
type ErrAllProvidersFailed struct {
	// Attempts records each provider's name + sanitised reason for failure.
	Attempts []FailedAttempt
}

// FailedAttempt is one provider's failure record, suitable for ERROR logging
// after the entire chain has been exhausted.
type FailedAttempt struct {
	Provider string
	Reason   string
	Err      error
}

// Error returns "all providers failed: provider1=reason1; provider2=reason2; ..."
func (e *ErrAllProvidersFailed) Error() string {
	if len(e.Attempts) == 0 {
		return "all providers failed: chain empty"
	}
	out := "all providers failed:"
	for i, a := range e.Attempts {
		if i > 0 {
			out += ";"
		}
		out += " " + a.Provider + "=" + a.Reason
	}
	return out
}

// NewChain returns a Chain over the given providers. nil entries are dropped
// silently — this lets callers do
//
//	NewChain(maybeClaude(env), maybeOpenRouter(env), maybeGroq(env))
//
// without nil-checking each constructor at the call site.
func NewChain(providers ...JSONClient) *Chain {
	c := &Chain{now: time.Now}
	for _, p := range providers {
		if p != nil {
			c.providers = append(c.providers, p)
		}
	}
	return c
}

// Len returns the number of active providers in the chain. Useful for tests
// and for the "memory-only" gate in callers.
func (c *Chain) Len() int { return len(c.providers) }

// Names returns the ordered provider names. Used by tests to assert chain
// composition.
func (c *Chain) Names() []string {
	out := make([]string, 0, len(c.providers))
	for _, p := range c.providers {
		out = append(out, p.Name())
	}
	return out
}

// Name implements JSONClient so a Chain itself can be used wherever a single
// provider is expected. It returns "chain" because the underlying provider
// changes per call.
func (c *Chain) Name() string { return "chain" }

// CompleteJSON tries each provider in order. On Retryable failure (timeout,
// network, 429, 5xx, empty, invalid JSON) it logs WARN and continues. On the
// first success it logs INFO and returns. If every provider fails, it logs
// ERROR with the full attempt list and returns ErrAllProvidersFailed.
//
// Missing-key providers MUST be filtered out by the constructor (BuildChainFromEnv
// or the caller). They are not retry attempts; they never reach this function.
func (c *Chain) CompleteJSON(ctx context.Context, req JSONRequest) (string, error) {
	if len(c.providers) == 0 {
		return "", ErrNoProviders
	}
	attempts := make([]FailedAttempt, 0, len(c.providers))
	for _, p := range c.providers {
		start := c.now()
		out, err := p.CompleteJSON(ctx, req)
		latency := c.now().Sub(start)
		if err == nil {
			slog.Info("llm: provider ok",
				"task", req.Task,
				"provider", p.Name(),
				"latency_ms", latency.Milliseconds(),
			)
			return out, nil
		}
		reason := classifyChainErr(p.Name(), err)
		attempts = append(attempts, FailedAttempt{Provider: p.Name(), Reason: reason, Err: err})
		slog.Warn("llm: provider failed, falling through",
			"task", req.Task,
			"provider", p.Name(),
			"reason", reason,
			"latency_ms", latency.Milliseconds(),
		)
		if ctxErr := ctx.Err(); ctxErr != nil {
			// Caller cancelled / deadline exceeded — stop trying to be helpful.
			return "", fmt.Errorf("llm chain aborted: %w", ctxErr)
		}
	}
	finalErr := &ErrAllProvidersFailed{Attempts: attempts}
	slog.Error("llm: all providers failed",
		"task", req.Task,
		"attempts", len(attempts),
		"detail", finalErr.Error(),
	)
	return "", finalErr
}

// classifyChainErr returns a short reason label suitable for the chain log.
// It prefers the Retryable.Reason when the provider tagged it, else a generic
// "provider_error" label. The provider's full Error() is logged separately.
func classifyChainErr(_ string, err error) string {
	var retry *Retryable
	if errors.As(err, &retry) {
		if retry.Reason != "" {
			return retry.Reason
		}
		return "retryable"
	}
	return reasonProviderErr
}
