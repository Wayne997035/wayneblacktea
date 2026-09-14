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
// who was never told. The provider succeeds first, so there IS evidence of a
// working path — which is what separates this from the unknown case below.
func TestGetLLMHealth_TransientFailureStaysOK(t *testing.T) {
	flaky := &healthStubProvider{name: "groq", model: "groq/compound"}
	chain := llm.NewChain(flaky)
	if _, err := chain.CompleteJSON(context.Background(), llm.JSONRequest{Task: "t"}); err != nil {
		t.Fatalf("priming call failed: %v", err)
	}
	flaky.err = &llm.Retryable{Provider: "groq", Reason: "http_429", Err: context.DeadlineExceeded}
	_, _ = chain.CompleteJSON(context.Background(), llm.JSONRequest{Task: "t"})

	rec, body := serveLLMHealth(t, chain)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d after a single transient failure, want %d", rec.Code, http.StatusOK)
	}
	if body.Status != llmStatusOK {
		t.Errorf("status field = %q, want %q", body.Status, llmStatusOK)
	}
}

// TestGetLLMHealth_UnknownWhenNoProviderHasEverSucceeded is the security
// review's Major finding, turned into a test.
//
// This is the incident state reproduced exactly: the process has just
// restarted, its only provider points at a decommissioned model, and no call
// has landed yet. A decommissioned model id is not rejected at construction,
// so nothing has failed either. The first version of this endpoint answered
// 200 "ok" here — meaning that anyone who checked it right after the deploy
// meant to FIX the outage would have been told everything was fine.
func TestGetLLMHealth_UnknownWhenNoProviderHasEverSucceeded(t *testing.T) {
	chain := llm.NewChain(&healthStubProvider{name: "groq", model: "llama-dead"})

	rec, body := serveLLMHealth(t, chain)
	if body.Status == llmStatusOK || rec.Code == http.StatusOK {
		t.Errorf("status = %q / HTTP %d with zero completed calls — the endpoint must never "+
			"report a working path it has no evidence for; that is the whole failure it exists "+
			"to expose", body.Status, rec.Code)
	}
	if body.Status != llmStatusUnknown {
		t.Errorf("status field = %q, want %q", body.Status, llmStatusUnknown)
	}
	if len(body.Providers) != 1 || body.Providers[0].Calls != 0 {
		t.Errorf("providers = %+v, want one entry with calls == 0", body.Providers)
	}
}

// TestGetLLMHealth_ImpairedNotDegradedWhenFallbackServes is the security
// review's third finding. Returning 503 while a fallback answers every
// request would page for a non-outage — and it would do so precisely to
// people who followed this PR's own advice to configure a second provider.
// An alert that cries wolf gets switched off, which puts you back where the
// incident started.
func TestGetLLMHealth_ImpairedNotDegradedWhenFallbackServes(t *testing.T) {
	dead := &healthStubProvider{
		name:  "groq",
		model: "llama-dead",
		err:   &llm.Retryable{Provider: "groq", Reason: "http_404", Err: context.DeadlineExceeded},
	}
	alive := &healthStubProvider{name: "openrouter", model: "or/model"}
	chain := llm.NewChain(dead, alive)
	driveFailures(chain) // dead fails every time; alive serves every time

	rec, body := serveLLMHealth(t, chain)
	if rec.Code != http.StatusOK {
		t.Errorf("HTTP %d while a fallback served every call — 503 here pages for a non-outage",
			rec.Code)
	}
	if body.Status != llmStatusImpaired {
		t.Errorf("status field = %q, want %q — a dead provider behind a working fallback is "+
			"worth surfacing but is not an outage", body.Status, llmStatusImpaired)
	}
	// Reverse frame: "impaired" must not hide that one provider is dead.
	var sawSustained bool
	for _, p := range body.Providers {
		if p.Sustained {
			sawSustained = true
		}
	}
	if !sawSustained {
		t.Error("no provider reported Sustained — impaired must still expose which one is dead")
	}
}

// TestGetLLMHealth_FailureRatioIsDerivable is the security review's second
// finding. ConsecutiveFailures resets on any success, so a provider failing
// four calls in every five never reaches the sustained threshold. Without a
// lifetime failure count nothing in the response would reveal it.
func TestGetLLMHealth_FailureRatioIsDerivable(t *testing.T) {
	flapping := &healthStubProvider{name: "groq", model: "groq/compound"}
	chain := llm.NewChain(flapping)
	fail := &llm.Retryable{Provider: "groq", Reason: "http_500", Err: context.DeadlineExceeded}
	for range 5 {
		for range 4 {
			flapping.err = fail
			_, _ = chain.CompleteJSON(context.Background(), llm.JSONRequest{Task: "t"})
		}
		flapping.err = nil
		_, _ = chain.CompleteJSON(context.Background(), llm.JSONRequest{Task: "t"})
	}

	_, body := serveLLMHealth(t, chain)
	p := body.Providers[0]
	if p.Sustained {
		t.Fatal("fixture is wrong: a 4-in-5 failure rate must NOT trip the consecutive threshold, " +
			"otherwise this test is not exercising the gap it exists for")
	}
	if p.Calls != 25 || p.Failures != 20 {
		t.Errorf("calls = %d, failures = %d; want 25 / 20 so an 80%% failure rate is derivable "+
			"from the response alone", p.Calls, p.Failures)
	}
}
