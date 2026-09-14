package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/llm"
	"github.com/labstack/echo/v4"
)

// [GTD ab472814] The endpoint exists because production ran three LLM-backed
// features at 0% success for three days while every HTTP request answered
// 200. The tests below therefore care about one thing above all: that a dead
// chain does NOT look healthy.

// healthStubProvider implements llm.JSONClient. err == nil means the call
// succeeds. It holds no mutable state so it is safe to share.
type healthStubProvider struct {
	name  string
	model string
	err   error
}

func (s *healthStubProvider) Name() string  { return s.name }
func (s *healthStubProvider) Model() string { return s.model }
func (s *healthStubProvider) CompleteJSON(_ context.Context, _ llm.JSONRequest) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return "{}", nil
}

// driveFailures calls the chain enough times to push every provider past the
// sustained-failure threshold. The count is deliberately well above the
// threshold rather than derived from it: sustainedFailureThreshold is
// unexported, and if it is ever raised past this number the assertion below
// fails loudly (503 expected, 200 received) instead of quietly testing
// nothing.
func driveFailures(c *llm.Chain) {
	for range 20 {
		_, _ = c.CompleteJSON(context.Background(), llm.JSONRequest{Task: "classify"})
	}
}

func serveLLMHealth(t *testing.T, chain *llm.Chain) (*httptest.ResponseRecorder, llmHealthResponse) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/health/llm", nil)
	rec := httptest.NewRecorder()
	h := NewLLMHealthHandler(chain)
	if err := h.GetLLMHealth(e.NewContext(req, rec)); err != nil {
		t.Fatalf("GetLLMHealth: %v", err)
	}
	var body llmHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response %q: %v", rec.Body.String(), err)
	}
	return rec, body
}

// TestGetLLMHealth_OKWhenChainIsHealthy is the positive control. Without it,
// a handler hard-coded to return 503 would pass every other test in this file
// — and an endpoint that always says "degraded" is as useless as one that
// always says "ok".
func TestGetLLMHealth_OKWhenChainIsHealthy(t *testing.T) {
	chain := llm.NewChain(
		&healthStubProvider{name: "claude", model: "claude-x"},
		&healthStubProvider{name: "groq", model: "groq/compound"},
	)
	if _, err := chain.CompleteJSON(context.Background(), llm.JSONRequest{Task: "t"}); err != nil {
		t.Fatalf("priming call failed: %v", err)
	}

	rec, body := serveLLMHealth(t, chain)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body.Status != llmStatusOK {
		t.Errorf("status field = %q, want %q", body.Status, llmStatusOK)
	}
	if !body.HasFallback || body.Length != 2 {
		t.Errorf("HasFallback = %v, Length = %d; want true/2", body.HasFallback, body.Length)
	}
}

// TestGetLLMHealth_503WhenProviderSustainedlyFailing is the incident test. A
// chain in this exact state served production for three days behind HTTP 200.
func TestGetLLMHealth_503WhenProviderSustainedlyFailing(t *testing.T) {
	dead := &healthStubProvider{
		name:  "groq",
		model: "llama-dead",
		err:   &llm.Retryable{Provider: "groq", Reason: "http_404", Err: context.DeadlineExceeded},
	}
	chain := llm.NewChain(dead)
	driveFailures(chain)

	rec, body := serveLLMHealth(t, chain)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d — a chain failing every call must not answer OK",
			rec.Code, http.StatusServiceUnavailable)
	}
	if body.Status != llmStatusDegraded {
		t.Errorf("status field = %q, want %q", body.Status, llmStatusDegraded)
	}
	if body.HasFallback {
		t.Error("HasFallback = true for a one-provider chain")
	}
	if len(body.Providers) != 1 || !body.Providers[0].Sustained {
		t.Errorf("providers = %+v, want one entry marked Sustained", body.Providers)
	}
	if body.Providers[0].LastReason != "http_404" {
		t.Errorf("LastReason = %q, want http_404 — the reason is what tells an operator this is "+
			"a dead model id and not a network blip", body.Providers[0].LastReason)
	}
}

// TestGetLLMHealth_NamesTheModelNotJustTheProvider pins the field whose
// absence made the outage invisible: the chain was correctly named "groq"
// the entire time it was pointed at a model that no longer existed.
func TestGetLLMHealth_NamesTheModelNotJustTheProvider(t *testing.T) {
	chain := llm.NewChain(&healthStubProvider{name: "groq", model: "groq/compound"})
	_, body := serveLLMHealth(t, chain)

	joined := strings.Join(body.Chain, ",")
	if !strings.Contains(joined, "groq/compound") {
		t.Errorf("chain = %v, want the model named — provider name alone cannot distinguish "+
			"a live model from a decommissioned one", body.Chain)
	}
}

// TestGetLLMHealth_503WhenUnconfigured covers both shapes of "no provider":
// a nil chain and an empty one. Both are reachable — a deployment with no
// provider keys is supported — and neither may report ok.
func TestGetLLMHealth_503WhenUnconfigured(t *testing.T) {
	for _, tc := range []struct {
		name  string
		chain *llm.Chain
	}{
		{"nil chain", nil},
		{"empty chain", llm.NewChain()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, body := serveLLMHealth(t, tc.chain)
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
			}
			if body.Status != llmStatusUnconfigured {
				t.Errorf("status field = %q, want %q", body.Status, llmStatusUnconfigured)
			}
		})
	}
}

// TestGetLLMHealth_TransientFailureStaysOK is the reverse frame. An endpoint
// that reports degraded on the first failed call would flap on every 429
// burst, and an operator who learns to ignore it is exactly as blind as one
// who was never told.
func TestGetLLMHealth_TransientFailureStaysOK(t *testing.T) {
	flaky := &healthStubProvider{
		name:  "groq",
		model: "groq/compound",
		err:   &llm.Retryable{Provider: "groq", Reason: "http_429", Err: context.DeadlineExceeded},
	}
	chain := llm.NewChain(flaky)
	_, _ = chain.CompleteJSON(context.Background(), llm.JSONRequest{Task: "t"})

	rec, body := serveLLMHealth(t, chain)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d after a single transient failure, want %d", rec.Code, http.StatusOK)
	}
	if body.Status != llmStatusOK {
		t.Errorf("status field = %q, want %q", body.Status, llmStatusOK)
	}
}
