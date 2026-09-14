package llm

import (
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// [GTD ab472814] Observability for the one failure mode this package had no
// way to express: a provider that is configured, reachable, authenticated —
// and permanently dead.
//
// Measured in production on 2026-09-14: defaultGroqModel pointed at a model
// Groq had decommissioned, so every call returned http_404. The chain logged
// ERROR "all providers failed" on each call and all three callers
// (decisionProposerMiddleware, ActivityClassifier, ConceptReviewer) degraded
// to a zero value, so every HTTP request still answered 200. Three features
// were 100% down for three days and nothing distinguished that from health.
//
// The gap was never "no logging" — the ERROR line was there on every call.
// The gap is that one failed call and ten thousand consecutive failed calls
// produced the SAME line, so the signal that matters (this is sustained, not
// a blip) was the one thing the log could not say.

// sustainedFailureThreshold is the number of consecutive failures after which
// a provider is reported as sustained-dead rather than merely failing.
//
// 5 is chosen to sit above ordinary transient noise (a 429 burst or a single
// 5xx clears well under this) and far below the volume a decommissioned model
// produces — production emitted 60 consecutive failures in the three-minute
// window that was sampled. Anything in that gap works; what must NOT happen
// is escalating on the first failure, because then the escalation is as noisy
// as the thing it is supposed to stand out from.
const sustainedFailureThreshold = 5

// ProviderHealth is one provider's observed liveness, derived entirely from
// real traffic. Nothing here costs an API call: a health probe that spends
// money to ask "are you alive" would itself be a new failure surface, and the
// requests the application already makes answer the question for free.
type ProviderHealth struct {
	Provider string `json:"provider"`
	// Model is the model id this provider was constructed with. It is the
	// field that would have made the production incident obvious at a glance
	// — the chain named "groq" was healthy as a route and dead as a model.
	Model string `json:"model"`
	// ConsecutiveFailures resets to 0 on any success.
	ConsecutiveFailures int `json:"consecutive_failures"`
	// Sustained is ConsecutiveFailures >= sustainedFailureThreshold: the
	// failure has outlived anything a retry would fix.
	Sustained bool `json:"sustained"`
	// LastReason is the most recent classified failure label (e.g.
	// "http_404", "timeout"). Empty when the provider has never failed.
	LastReason    string     `json:"last_reason,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt *time.Time `json:"last_failure_at,omitempty"`
	// Calls is the total number of attempts routed to this provider.
	// A provider with Calls == 0 is configured but unexercised — which is a
	// third state, distinct from healthy and from failing, and the one a
	// reader would otherwise misread as healthy.
	Calls int `json:"calls"`
	// Failures is the lifetime failure count, which ConsecutiveFailures is
	// NOT: that one resets to 0 on every success. A provider failing four
	// calls in every five therefore never reaches the sustained threshold,
	// and without this field nothing in the response would let a reader
	// derive the failure ratio that makes such a provider visible.
	Failures int `json:"failures"`
}

// modelNamer is implemented by providers that can report the model id they
// were built with. It is deliberately NOT part of JSONClient: adding a method
// to that interface would break every test fake and every future provider for
// the sake of a log line. Providers that do not implement it report "".
type modelNamer interface{ Model() string }

func modelOf(p JSONClient) string {
	if m, ok := p.(modelNamer); ok {
		return m.Model()
	}
	return ""
}

// healthTracker holds the per-provider counters behind a mutex. Chain is used
// concurrently by HTTP handlers and the scheduler, so these counters are
// genuinely shared state, not per-request state.
// Neither map here is ever pruned, and that is safe for one reason worth
// stating rather than leaving to be re-derived: every key comes from
// p.Name() where p ranges over Chain.providers, a set fixed at construction
// and bounded by the four provider kinds BuildChainFromEnv can produce. No
// key is derived from a request, a payload or any other caller-controlled
// value, so the maps cannot grow with traffic.
//
// This is what makes the mechanical cache-invalidation axis flag the file: it
// sees mutex-guarded maps with inserts and no deletes and cannot tell a
// fixed startup registry from an unbounded cache. The axis documents that
// exact false positive. If a future change ever keys these by something a
// caller supplies — a model id, a request tag — the bound disappears and
// eviction stops being optional.
type healthTracker struct {
	mu sync.Mutex
	// byProvider is keyed by provider Name(). Two providers with the same
	// Name would share an entry; that is the same assumption the existing
	// log lines already make.
	byProvider map[string]*ProviderHealth
	// escalated records that the sustained-failure ERROR has already been
	// emitted for this provider, so crossing the threshold logs once per
	// outage rather than once per call. Without this the escalation would
	// reproduce the exact noise problem it exists to solve.
	escalated map[string]bool
}

func newHealthTracker() *healthTracker {
	return &healthTracker{
		byProvider: make(map[string]*ProviderHealth),
		escalated:  make(map[string]bool),
	}
}

// entry returns the record for name, creating it on first use. Caller MUST
// hold h.mu.
func (h *healthTracker) entry(name, model string) *ProviderHealth {
	e, ok := h.byProvider[name]
	if !ok {
		e = &ProviderHealth{Provider: name, Model: model}
		h.byProvider[name] = e
	}
	// Model can only be learned once a provider is seen; refresh it so a
	// tracker seeded before construction details were known still reports it.
	if model != "" {
		e.Model = model
	}
	return e
}

// The record* methods tolerate a nil receiver so a zero-value Chain (built by
// struct literal rather than NewChain) degrades to "no health tracking"
// instead of panicking on the request path. Nothing constructs one that way
// today; "guarded only by convention" is not a guarantee.
func (h *healthTracker) recordSuccess(name, model string, at time.Time) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.entry(name, model)
	e.Calls++
	e.ConsecutiveFailures = 0
	e.Sustained = false
	e.LastReason = ""
	ts := at
	e.LastSuccessAt = &ts
	if h.escalated[name] {
		// Recovery is worth exactly one line, for the same reason the outage
		// is: someone reading the log needs to see the outage end, not infer
		// it from the absence of further errors.
		slog.Info("llm: provider recovered", "provider", name, "model", model)
		h.escalated[name] = false
	}
}

func (h *healthTracker) recordFailure(name, model, reason string, at time.Time) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.entry(name, model)
	e.Calls++
	e.Failures++
	e.ConsecutiveFailures++
	e.LastReason = reason
	ts := at
	e.LastFailureAt = &ts
	if e.ConsecutiveFailures < sustainedFailureThreshold {
		return
	}
	e.Sustained = true
	if h.escalated[name] {
		return
	}
	h.escalated[name] = true
	slog.Error(
		"llm: provider sustained failure — this is not transient",
		"provider", name,
		"model", model,
		"consecutive_failures", e.ConsecutiveFailures,
		"reason", reason,
		"hint", "a repeating http_404 usually means the model id no longer exists at the vendor",
	)
}

// snapshot returns a copy of every tracked provider, ordered by name so the
// output is stable across calls.
func (h *healthTracker) snapshot() []ProviderHealth {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ProviderHealth, 0, len(h.byProvider))
	for _, e := range h.byProvider {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}

// Health returns the current per-provider health snapshot. Providers that
// have never been called are included with Calls == 0 so a caller can tell
// "configured but unexercised" from "configured and working".
func (c *Chain) Health() []ProviderHealth {
	// A zero-value Chain (one built by struct literal rather than NewChain)
	// has no tracker. Nothing constructs one that way today, but "guarded
	// only by convention" is not a guarantee, and the panic would land on
	// the HTTP request path.
	if c.health == nil {
		return nil
	}
	c.health.mu.Lock()
	for _, p := range c.providers {
		c.health.entry(p.Name(), modelOf(p))
	}
	c.health.mu.Unlock()
	return c.health.snapshot()
}

// Describe returns one "provider(model)" string per configured provider, in
// chain order. This is what the startup log prints instead of bare names:
// the production incident was a chain whose NAME was right and whose MODEL
// was dead, and a log line of `[groq]` cannot distinguish those.
func (c *Chain) Describe() []string {
	out := make([]string, 0, len(c.providers))
	for _, p := range c.providers {
		if m := modelOf(p); m != "" {
			out = append(out, p.Name()+"("+m+")")
			continue
		}
		out = append(out, p.Name())
	}
	return out
}

// LogStartup reports the resolved chain exactly once, at boot.
//
// It lives here rather than at the call sites because there are two entry
// points (cmd/server and internal/mcprunner) and they had drifted into two
// copies of the same log.Printf — both printing bare names, neither noticing
// that a one-provider chain has no fallback. A shared function is what stops
// the third entry point from inventing a third variant.
//
// The thin-chain WARN is the line that would have caught the production
// incident at deploy time: "fallback chain" with one link is not a fallback,
// and until now nothing said so out loud.
func (c *Chain) LogStartup() {
	switch n := c.Len(); n {
	case 0:
		slog.Warn(
			"llm: memory-only mode — no provider configured",
			"effect", "decision drafting, activity classification and concept review will silently return empty",
		)
	case 1:
		slog.Warn(
			"llm: provider chain has no fallback",
			"chain", strings.Join(c.Describe(), " -> "),
			"length", n,
			"effect", "if this single provider fails, every LLM-backed feature degrades to empty with HTTP 200",
			"fix", "set AI_FALLBACK_PROVIDERS and a second provider key",
		)
	default:
		slog.Info(
			"llm: provider chain resolved",
			"chain", strings.Join(c.Describe(), " -> "),
			"length", n,
		)
	}
}
