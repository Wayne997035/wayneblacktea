package llm

import (
	"errors"
	"time"
)

// [F1124-50] Deterministic router over GenerateClient candidates. The nine
// routing dimensions (13_MODEL_RUNTIME.md:49-58) map to RoutingRequest fields.
// Select never reads a clock or a random source: same input, same output.

// ErrNoCandidateSatisfiesRequest is returned when every candidate is filtered
// out by health, capability, sensitivity or cost.
var ErrNoCandidateSatisfiesRequest = errors.New("llm router: no candidate satisfies the request")

// ErrUnknownSensitivity is returned for a Sensitivity label that is neither
// "", "public" nor "private". Unknown labels fail closed. [F1124-51]
var ErrUnknownSensitivity = errors.New("llm router: unknown sensitivity label")

// TaskClass names a kind of work, e.g. "chat", "classify", "deep_reasoning".
type TaskClass string

const (
	sensitivityPublic  = "public"
	sensitivityPrivate = "private"
)

// taskClassPriority is the fixed provider priority per TaskClass, keyed by
// GenerateClient.Name(). Earlier = preferred.
//
//	chat:           groq, claude, openrouter, openai-compatible
//	classify:       groq, openrouter, openai-compatible, claude
//	deep_reasoning: claude, openrouter, openai-compatible, groq
//
// A candidate absent from a class's list sorts after the listed ones, in
// Candidates slice order. An empty or unknown TaskClass uses Candidates order.
var taskClassPriority = map[TaskClass][]string{
	"chat":           {"groq", "claude", "openrouter", "openai-compatible"},
	"classify":       {"groq", "openrouter", "openai-compatible", "claude"},
	"deep_reasoning": {"claude", "openrouter", "openai-compatible", "groq"},
}

// RoutingRequest carries the nine routing dimensions.
type RoutingRequest struct {
	TaskClass TaskClass
	// LatencyTarget is accepted but does not filter yet: no provider reports
	// latency here. 0 = no preference.
	LatencyTarget time.Duration
	// Modality is the required-capability shape: every true bool field must be
	// true on the candidate. MaxContext is ignored here (see ContextSizeTokens).
	Modality            Capabilities
	Sensitivity         string // "", "public", "private"
	RequiresToolCalling bool
	// CostBudgetMicroUSD != 0 excludes every candidate: GenerateClient exposes
	// no model string, so cost is unknown. Rates arrive with slice 5c.
	CostBudgetMicroUSD int64
	ContextSizeTokens  int
	PreferredProvider  string // "" = no preference
}

// RouterConfig configures NewRouter.
type RouterConfig struct {
	Candidates []GenerateClient
	// Healthy reports whether a provider (by Name()) may be used. Required.
	Healthy func(provider string) bool
	// PrivateProviders lists provider names allowed for Sensitivity "private".
	// nil/empty rejects all private requests.
	PrivateProviders []string
}

// Router selects one GenerateClient per request.
type Router struct {
	candidates []GenerateClient
	healthy    func(string) bool
	private    map[string]struct{}
}

// NewRouter validates cfg. A nil Healthy or empty Candidates is an error so the
// health dimension can never silently become "everything is healthy".
func NewRouter(cfg RouterConfig) (*Router, error) {
	if cfg.Healthy == nil {
		return nil, errors.New("llm router: Healthy is required")
	}
	if len(cfg.Candidates) == 0 {
		return nil, errors.New("llm router: at least one candidate is required")
	}
	for _, c := range cfg.Candidates {
		if c == nil {
			return nil, errors.New("llm router: nil candidate")
		}
	}
	priv := make(map[string]struct{}, len(cfg.PrivateProviders))
	for _, n := range cfg.PrivateProviders {
		priv[n] = struct{}{}
	}
	return &Router{
		candidates: append([]GenerateClient(nil), cfg.Candidates...),
		healthy:    cfg.Healthy,
		private:    priv,
	}, nil
}

// Select picks a candidate. Order: unknown-sensitivity check; then health,
// capability, sensitivity and cost filters; only then PreferredProvider (within
// the surviving set); otherwise the TaskClass priority table.
func (r *Router) Select(req RoutingRequest) (GenerateClient, error) {
	switch req.Sensitivity {
	case "", sensitivityPublic, sensitivityPrivate:
	default:
		return nil, ErrUnknownSensitivity
	}

	var pool []GenerateClient
	for _, c := range r.candidates {
		name := c.Name()
		if !r.healthy(name) {
			continue
		}
		if !satisfies(c.Capabilities(), req) {
			continue
		}
		if req.Sensitivity == sensitivityPrivate {
			if _, ok := r.private[name]; !ok {
				continue
			}
		}
		if req.CostBudgetMicroUSD != 0 {
			continue // cost unknown for every candidate
		}
		pool = append(pool, c)
	}
	if len(pool) == 0 {
		return nil, ErrNoCandidateSatisfiesRequest
	}

	if req.PreferredProvider != "" {
		for _, c := range pool {
			if c.Name() == req.PreferredProvider {
				return c, nil
			}
		}
	}

	for _, name := range taskClassPriority[req.TaskClass] {
		for _, c := range pool {
			if c.Name() == name {
				return c, nil
			}
		}
	}
	return pool[0], nil
}

// modalityMet reports whether c has every capability flagged in need.
func modalityMet(c, need Capabilities) bool {
	return (!need.Text || c.Text) && (!need.ImageInput || c.ImageInput) &&
		(!need.AudioInput || c.AudioInput) && (!need.AudioOutput || c.AudioOutput) &&
		(!need.ToolCalling || c.ToolCalling) && (!need.Realtime || c.Realtime)
}

func satisfies(c Capabilities, req RoutingRequest) bool {
	if !modalityMet(c, req.Modality) {
		return false
	}
	if req.RequiresToolCalling && !c.ToolCalling {
		return false
	}
	if req.ContextSizeTokens > 0 && c.MaxContext < req.ContextSizeTokens {
		return false
	}
	return true
}
