package handler

import (
	"net/http"

	"github.com/Wayne997035/wayneblacktea/internal/llm"
	"github.com/labstack/echo/v4"
)

// [GTD ab472814] GET /api/health/llm — the on-demand answer to "is the LLM
// side actually alive", which production had no way to ask.
//
// The incident this exists for: the configured provider pointed at a model
// the vendor had decommissioned, so every call returned http_404 while every
// HTTP request still answered 200. Three LLM-backed features were down for
// three days and every externally observable signal said healthy.
//
// This endpoint reports only what real traffic already proved. It does NOT
// call the provider: a probe that spends money and quota to answer a
// liveness question is a new failure surface (and, on an endpoint, a way for
// a caller to spend someone else's budget). Chain.Health accumulates the
// verdict from the requests the application makes anyway.

// The three values llmHealthResponse.Status can take. Named constants rather
// than literals so the handler and its tests assert against one definition —
// a test that hard-codes the string it expects cannot catch a typo in the
// handler that produces it.
const (
	llmStatusOK           = "ok"
	llmStatusImpaired     = "impaired"
	llmStatusDegraded     = "degraded"
	llmStatusUnknown      = "unknown"
	llmStatusUnconfigured = "unconfigured"
)

// classifyLLM turns the per-provider snapshot into one verdict plus the HTTP
// status that carries it.
//
// The three-way split ok / impaired / degraded exists because the first
// version of this endpoint had only ok and degraded, and both ends were
// wrong:
//
//   - "ok" was returned whenever no provider was in SUSTAINED failure — which
//     includes a freshly restarted process whose only provider is 100% dead
//     and has simply not been called yet. That is precisely the state this
//     endpoint was built to expose: a decommissioned model id is not rejected
//     at construction, so the chain looks perfect until the first call fails.
//     Anyone checking the endpoint right after the deploy that was supposed
//     to fix the outage would have been told everything was fine.
//   - "degraded" (503) was returned when ANY provider was sustained, even
//     when a healthy fallback was serving every request. Following this PR's
//     own advice — configure a second provider — would then page for a
//     non-outage, and an alert that cries wolf gets switched off, which puts
//     you back where the incident started.
//
// So: evidence of a working path is what earns 200, and only the loss of
// every path earns 503.
//
// "unknown" is fail-closed on purpose: unparseable / n/a / skipped are
// never read as pass. The cost is real and worth stating:
// after every restart this answers 503 until the first LLM call lands, and
// calls can be half an hour apart. That window is noise. It is accepted
// because the alternative — reporting "ok" with no evidence — is the exact
// failure that let three features stay dead for three days.
func classifyLLM(providers []llm.ProviderHealth) (string, int) {
	if len(providers) == 0 {
		return llmStatusUnknown, http.StatusServiceUnavailable
	}
	sustained, proven := 0, 0
	for _, p := range providers {
		if p.Sustained {
			sustained++
		}
		if p.LastSuccessAt != nil {
			proven++
		}
	}
	switch {
	case sustained == len(providers):
		// Nothing left that can serve.
		return llmStatusDegraded, http.StatusServiceUnavailable
	case proven == 0:
		// No provider has ever completed a call. Not known-bad, but there is
		// no evidence of a working path, and saying "ok" here is the defect.
		return llmStatusUnknown, http.StatusServiceUnavailable
	case sustained > 0:
		// A fallback is carrying it. Visible, but not an outage to page on.
		return llmStatusImpaired, http.StatusOK
	default:
		return llmStatusOK, http.StatusOK
	}
}

// llmHealthResponse is the JSON shape for GET /api/health/llm.
type llmHealthResponse struct {
	// Status is "ok" | "degraded" | "unconfigured" — the single field a
	// caller needs if it reads nothing else.
	Status string `json:"status"`
	// Chain lists the resolved providers as "name(model)", in attempt order.
	Chain []string `json:"chain"`
	// Length is len(Chain). HasFallback is Length > 1 — named separately
	// because "this chain cannot fall back" is the condition that turned one
	// vendor's deprecation into a total outage, and a reader should not have
	// to derive it.
	Length      int  `json:"length"`
	HasFallback bool `json:"has_fallback"`
	// Providers carries the per-provider counters. A provider with calls == 0
	// is configured but unexercised — neither proven healthy nor failing.
	Providers []llm.ProviderHealth `json:"providers"`
}

// LLMHealthHandler serves the LLM chain health surface.
//
// It holds the concrete *llm.Chain rather than an interface on purpose: an
// interface field would accept a typed-nil *llm.Chain, which is non-nil as an
// interface value and panics on first use. The nil case is real here — a
// deployment with no provider keys is supported — so it is worth making
// unrepresentable rather than documented.
type LLMHealthHandler struct {
	chain *llm.Chain
}

// NewLLMHealthHandler builds the handler. A nil chain is valid and reports
// status "unconfigured".
func NewLLMHealthHandler(chain *llm.Chain) *LLMHealthHandler {
	return &LLMHealthHandler{chain: chain}
}

// GetLLMHealth handles GET /api/health/llm.
//
// The status code carries the verdict deliberately: an uptime monitor that
// only knows how to check status codes is exactly the consumer this endpoint
// is for, and requiring it to parse JSON to notice an outage would reproduce
// the original failure — a signal that is present but that nothing reads.
// Which state maps to which code, and why, is in classifyLLM.
func (h *LLMHealthHandler) GetLLMHealth(c echo.Context) error {
	if h.chain == nil || h.chain.Len() == 0 {
		return c.JSON(http.StatusServiceUnavailable, llmHealthResponse{
			Status:    llmStatusUnconfigured,
			Chain:     []string{},
			Providers: []llm.ProviderHealth{},
		})
	}

	providers := h.chain.Health()
	status, code := classifyLLM(providers)
	return c.JSON(code, llmHealthResponse{
		Status:      status,
		Chain:       h.chain.Describe(),
		Length:      h.chain.Len(),
		HasFallback: h.chain.Len() > 1,
		Providers:   providers,
	})
}
