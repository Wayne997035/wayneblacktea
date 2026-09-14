package llm

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// [GTD ab472814] These tests exist because of a measured production outage:
// defaultGroqModel named a model Groq had decommissioned, every call returned
// http_404, and all three LLM-backed features degraded to empty while every
// HTTP request still answered 200. Nothing in the logs distinguished "failed
// once" from "has failed every call for three days".
//
// Each test below is written so that reverting one specific piece of
// production code turns THAT test red and leaves the others green — a test
// that only proves the happy path would have passed throughout the outage.
//
// MUTATION (manually verified, not shipped as code) — each mutation was
// applied alone, the suite run, and the mutation reverted:
//
//	M1 defaultGroqModel -> "llama-3.3-70b-versatile"
//	   red: TestNewGroqClient_DefaultModelIsNotTheDecommissionedOne
//	   green (correctly): TestGroq_DefaultModel — it asserts the constant, not
//	   a literal, which is what stops it being a value-pin that has to be
//	   edited on every legitimate bump.
//	M2 drop the `if h.escalated[name] { return }` guard in recordFailure
//	   red: TestChainHealth_SustainedFailureEscalatesOnceNotEveryCall
//	M3 Describe -> return c.Names()
//	   red: TestChainDescribe_NamesTheModelNotJustTheProvider,
//	        TestChainLogStartup_DoesNotWarnWithARealFallback,
//	        handler TestGetLLMHealth_NamesTheModelNotJustTheProvider
//	M4 LogStartup's length-1 branch slog.Warn -> slog.Info
//	   red: TestChainLogStartup_WarnsWhenChainHasNoFallback
//	M5 handler always returns 200 (drop the degraded -> 503 branch)
//	   red: TestGetLLMHealth_503WhenProviderSustainedlyFailing
//	   green (correctly): TestGetLLMHealth_503WhenUnconfigured — the
//	   unconfigured branch returns earlier and is guarded separately.
//	M6 sustainedFailureThreshold -> 1
//	   red: TestChainHealth_BelowThresholdDoesNotEscalate,
//	        handler TestGetLLMHealth_TransientFailureStaysOK
//	M7 OpenRouterClient.Model() -> return c.model (drop the list branch)
//	   red: TestProviderModelAccessors/openrouter_model_list_reports_every_entry
//
// M6 is worth keeping in view: on the FIRST attempt it did not go red,
// because BelowThresholdDoesNotEscalate sized its loop as
// `sustainedFailureThreshold - 1`. Lowering the constant to 1 shrank that
// loop to zero iterations, so the test passed by making no calls at all while
// the regression was live. The oracle had been computed from the value it was
// checking. It now asserts the threshold directly and drives exactly one
// failure as a literal.

// captureLogs redirects the default slog logger into a buffer for the
// duration of one test. Tests using it MUST NOT call t.Parallel: slog.Default
// is process-global, and two parallel tests would interleave into each
// other's buffers and produce assertions that pass or fail by timing.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// stubModelClient is a stubClient that also reports a model id, so it
// satisfies the optional modelNamer interface.
type stubModelClient struct {
	stubClient
	model string
}

func (s *stubModelClient) Model() string { return s.model }

// deadProvider returns a stub that fails every call the way a decommissioned
// model does: a Retryable tagged http_404.
func deadProvider(name, model string) *stubModelClient {
	s := &stubModelClient{model: model}
	s.name = name
	s.err = &Retryable{Provider: name, Reason: "http_404", Err: context.DeadlineExceeded}
	return s
}

// TestChainDescribe_NamesTheModelNotJustTheProvider is the incident test.
// Production logged `llm: provider chain = [groq]` throughout the outage —
// the route was right and the model was dead, and that line could not tell
// the two apart. Reverting Describe to Names makes this test red.
func TestChainDescribe_NamesTheModelNotJustTheProvider(t *testing.T) {
	c := NewChain(deadProvider("groq", "groq/compound"))

	got := strings.Join(c.Describe(), ",")
	if !strings.Contains(got, "groq/compound") {
		t.Errorf("Describe() = %q, want it to name the model — a chain described only "+
			"by provider name cannot distinguish a live model from a decommissioned one", got)
	}
	// Reverse frame: Describe must not LOSE the provider name either.
	if !strings.Contains(got, "groq") {
		t.Errorf("Describe() = %q, dropped the provider name", got)
	}
}

// TestChainDescribe_FallsBackToBareNameWithoutModelNamer pins that a provider
// which does not implement modelNamer still appears. Without this, adding the
// optional interface could silently drop providers from the startup log.
func TestChainDescribe_FallsBackToBareNameWithoutModelNamer(t *testing.T) {
	c := NewChain(&stubClient{name: "legacy", out: "ok"})
	got := c.Describe()
	if len(got) != 1 || got[0] != "legacy" {
		t.Errorf("Describe() = %v, want [legacy] — a provider without a Model() method "+
			"must still be listed", got)
	}
}

// TestChainHealth_SustainedFailureEscalatesOnceNotEveryCall is the heart of
// the fix. The old code logged an identical ERROR on call 1 and on call
// 10,000, so the signal that mattered — this is sustained — was the one thing
// it could not express. Escalating on EVERY call would be just as useless, so
// this asserts exactly one escalation.
func TestChainHealth_SustainedFailureEscalatesOnceNotEveryCall(t *testing.T) {
	buf := captureLogs(t)
	p := deadProvider("groq", "llama-dead")
	c := NewChain(p)

	const calls = sustainedFailureThreshold + 7
	for range calls {
		_, _ = c.CompleteJSON(context.Background(), JSONRequest{Task: "classify"})
	}

	n := strings.Count(buf.String(), "sustained failure")
	if n != 1 {
		t.Errorf("sustained-failure escalations = %d over %d consecutive failures, want exactly 1 "+
			"(0 = the outage is invisible; >1 = the escalation is as noisy as what it replaces)", n, calls)
	}

	h := c.Health()
	if len(h) != 1 {
		t.Fatalf("Health() returned %d providers, want 1", len(h))
	}
	if !h[0].Sustained {
		t.Error("Sustained = false after threshold consecutive failures")
	}
	if h[0].ConsecutiveFailures != calls {
		t.Errorf("ConsecutiveFailures = %d, want %d", h[0].ConsecutiveFailures, calls)
	}
	if h[0].LastReason != "http_404" {
		t.Errorf("LastReason = %q, want http_404 — the reason is what tells an operator "+
			"this is a dead model id rather than a network blip", h[0].LastReason)
	}
}

// TestChainHealth_BelowThresholdDoesNotEscalate is the reverse frame. A guard
// that fires on the first failure would make every transient 429 look like an
// outage, and an operator who learns to ignore it is exactly as blind as one
// who was never told.
func TestChainHealth_BelowThresholdDoesNotEscalate(t *testing.T) {
	// The threshold is asserted directly rather than used to size the loop
	// below. Deriving the loop count from sustainedFailureThreshold made this
	// test self-referential: lowering the constant to 1 shrank the loop to
	// zero iterations, so the test passed by doing nothing while the very
	// regression it guards was live. An oracle must not be computed from the
	// value it is checking.
	if sustainedFailureThreshold < 2 {
		t.Fatalf("sustainedFailureThreshold = %d: a threshold of 1 escalates on the FIRST "+
			"failure, making the escalation channel exactly as noisy as the per-call WARN it "+
			"is supposed to stand out from", sustainedFailureThreshold)
	}

	buf := captureLogs(t)
	p := deadProvider("groq", "m")
	c := NewChain(p)

	// Exactly one failure — a literal, because one failed call is the weakest
	// possible evidence of an outage and must never escalate under ANY
	// threshold this constant is allowed to hold.
	_, _ = c.CompleteJSON(context.Background(), JSONRequest{Task: "classify"})

	if strings.Contains(buf.String(), "sustained failure") {
		t.Error("escalated after a single failure — transient noise must not reach the " +
			"escalation channel")
	}
	if c.Health()[0].Sustained {
		t.Error("Sustained = true after one failure")
	}
}

// TestChainHealth_SuccessResetsAndReportsRecovery pins that an outage ends
// visibly. Without the recovery line a reader can only infer the end from the
// absence of further errors, which is indistinguishable from traffic stopping
// — the exact ambiguity that hid this outage (the log simply went quiet for
// 30 minutes because nobody called the tool).
func TestChainHealth_SuccessResetsAndReportsRecovery(t *testing.T) {
	buf := captureLogs(t)
	p := deadProvider("groq", "m")
	c := NewChain(p)

	for range sustainedFailureThreshold {
		_, _ = c.CompleteJSON(context.Background(), JSONRequest{Task: "classify"})
	}
	p.err = nil
	p.out = "{}"
	if _, err := c.CompleteJSON(context.Background(), JSONRequest{Task: "classify"}); err != nil {
		t.Fatalf("recovery call failed: %v", err)
	}

	if !strings.Contains(buf.String(), "provider recovered") {
		t.Error("no recovery line logged — the end of an outage must be as visible as its start")
	}
	h := c.Health()[0]
	if h.ConsecutiveFailures != 0 || h.Sustained {
		t.Errorf("after success: ConsecutiveFailures = %d, Sustained = %v, want 0/false",
			h.ConsecutiveFailures, h.Sustained)
	}
	if h.LastSuccessAt == nil {
		t.Error("LastSuccessAt is nil after a successful call")
	}
}

// TestChainHealth_ReportsConfiguredButUnexercisedProvider pins the third
// state. A provider with zero calls is neither healthy nor failing, and
// reporting it as healthy is how a chain that has never once worked can look
// fine on a dashboard.
func TestChainHealth_ReportsConfiguredButUnexercisedProvider(t *testing.T) {
	c := NewChain(&stubModelClient{stubClient: stubClient{name: "never-called"}, model: "m"})
	h := c.Health()
	if len(h) != 1 {
		t.Fatalf("Health() = %d entries, want 1 — a configured provider must appear before "+
			"its first call", len(h))
	}
	if h[0].Calls != 0 {
		t.Errorf("Calls = %d, want 0", h[0].Calls)
	}
	if h[0].Sustained {
		t.Error("an unexercised provider must not be reported as sustained-failing")
	}
}

// TestChainLogStartup_WarnsWhenChainHasNoFallback is the deploy-time catch.
// Production ran a length-1 chain for three days; the startup line said
// `[groq]` and nothing said "this cannot fall back".
func TestChainLogStartup_WarnsWhenChainHasNoFallback(t *testing.T) {
	buf := captureLogs(t)
	NewChain(deadProvider("groq", "groq/compound")).LogStartup()

	out := buf.String()
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("startup log for a one-provider chain was not a WARN: %q", out)
	}
	if !strings.Contains(out, "no fallback") {
		t.Errorf("startup log = %q, want it to say the chain has no fallback", out)
	}
}

// TestChainLogStartup_DoesNotWarnWithARealFallback is the reverse frame: a
// warning that fires on a correctly configured chain trains the reader to
// ignore it.
func TestChainLogStartup_DoesNotWarnWithARealFallback(t *testing.T) {
	buf := captureLogs(t)
	NewChain(
		deadProvider("claude", "claude-x"),
		deadProvider("groq", "groq/compound"),
	).LogStartup()

	out := buf.String()
	if strings.Contains(out, "level=WARN") {
		t.Errorf("a two-provider chain must not WARN, got %q", out)
	}
	if !strings.Contains(out, "claude-x") || !strings.Contains(out, "groq/compound") {
		t.Errorf("startup log = %q, want both models named", out)
	}
}

// TestChainLogStartup_WarnsWhenUnconfigured covers the zero case, which is
// legitimate (memory-only mode) but must still be stated out loud.
func TestChainLogStartup_WarnsWhenUnconfigured(t *testing.T) {
	buf := captureLogs(t)
	NewChain().LogStartup()
	if !strings.Contains(buf.String(), "memory-only") {
		t.Errorf("startup log = %q, want the memory-only state named", buf.String())
	}
}

// concurrentStub is a provider that is safe to call from many goroutines.
//
// The shared stubClient in chain_test.go increments an unguarded `calls`
// counter, so reusing it here makes -race report ITS race instead of the one
// this test exists to detect. A red that points at the wrong code is worth no
// more than a green that points at nothing, so the fixture is what gets fixed
// — never the code under test.
type concurrentStub struct {
	name  string
	model string
	err   error
}

func (s *concurrentStub) Name() string  { return s.name }
func (s *concurrentStub) Model() string { return s.model }
func (s *concurrentStub) CompleteJSON(_ context.Context, _ JSONRequest) (string, error) {
	return "", s.err
}

// TestChainHealth_ConcurrentCallsAreRaceFree matters because Chain is shared
// by HTTP handlers and the scheduler. Run under -race this fails without the
// mutex in healthTracker; those counters are the first mutable state Chain
// has ever carried across calls, so nothing before this change needed one.
func TestChainHealth_ConcurrentCallsAreRaceFree(t *testing.T) {
	fail := &Retryable{Provider: "x", Reason: "http_404", Err: context.DeadlineExceeded}
	c := NewChain(
		&concurrentStub{name: "a", model: "m1", err: fail},
		&concurrentStub{name: "b", model: "m2", err: fail},
	)
	var wg sync.WaitGroup
	for range 24 {
		wg.Go(func() {
			_, _ = c.CompleteJSON(context.Background(), JSONRequest{Task: "t"})
			_ = c.Health()
			_ = c.Describe()
		})
	}
	wg.Wait()
	if got := len(c.Health()); got != 2 {
		t.Errorf("Health() = %d providers, want 2", got)
	}
}

// TestProviderModelAccessors covers Model() on the REAL provider types rather
// than on a stub. The Describe tests above prove the chain asks for a model;
// only this proves each provider answers with its own. OpenRouter is the one
// that carries actual branching — it holds a fallback LIST, and reporting only
// the primary would hide exactly the property that makes it resilient, which
// is the property this whole PR is about.
func TestProviderModelAccessors(t *testing.T) {
	claude, err := NewClaudeClient(ClaudeConfig{APIKey: "k", Model: "claude-x"})
	if err != nil {
		t.Fatalf("NewClaudeClient: %v", err)
	}
	if got := claude.Model(); got != "claude-x" {
		t.Errorf("ClaudeClient.Model() = %q, want claude-x", got)
	}

	oai, err := NewOpenAICompatibleClient(OpenAICompatibleConfig{
		BaseURL: "http://localhost:11434", Model: "llama3.2",
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleClient: %v", err)
	}
	if got := oai.Model(); got != "llama3.2" {
		t.Errorf("OpenAICompatibleClient.Model() = %q, want llama3.2", got)
	}

	t.Run("openrouter single model", func(t *testing.T) {
		c, err := NewOpenRouterClient(OpenRouterConfig{APIKey: "k", Model: "solo"})
		if err != nil {
			t.Fatalf("NewOpenRouterClient: %v", err)
		}
		if got := c.Model(); got != "solo" {
			t.Errorf("Model() = %q, want solo", got)
		}
	})

	t.Run("openrouter model list reports every entry", func(t *testing.T) {
		c, err := NewOpenRouterClient(OpenRouterConfig{
			APIKey: "k", Model: "primary", Models: []string{"a", "b", "c"},
		})
		if err != nil {
			t.Fatalf("NewOpenRouterClient: %v", err)
		}
		got := c.Model()
		for _, want := range []string{"a", "b", "c"} {
			if !strings.Contains(got, want) {
				t.Errorf("Model() = %q, missing %q — a startup log that names only the "+
					"primary hides the fallback list, which is the whole reason this "+
					"provider is more resilient than the others", got, want)
			}
		}
	})
}

// TestNewGroqClient_DefaultModelIsNotTheDecommissionedOne is a regression
// guard with a name, not a tautology: it does not assert which model we chose,
// only that the default is never again the id that took production down. A
// test pinning the exact current string would have to be edited on every
// legitimate bump and would therefore stop meaning anything.
func TestNewGroqClient_DefaultModelIsNotTheDecommissionedOne(t *testing.T) {
	const decommissioned = "llama-3.3-70b-versatile"

	c, err := NewGroqClient(GroqConfig{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("NewGroqClient: %v", err)
	}
	if c.Model() == "" {
		t.Fatal("default model is empty — Groq rejects a request with no model")
	}
	if c.Model() == decommissioned {
		t.Errorf("default model is %q, which Groq decommissioned; every call returns http_404 "+
			"and all LLM-backed features silently degrade to empty", decommissioned)
	}

	// Reverse frame: an explicit GROQ_MODEL must still win over the default,
	// because that env var is the escape hatch operators use when the default
	// rots again.
	explicit, err := NewGroqClient(GroqConfig{APIKey: "test-key", Model: "explicit-model"})
	if err != nil {
		t.Fatalf("NewGroqClient(explicit): %v", err)
	}
	if explicit.Model() != "explicit-model" {
		t.Errorf("Model() = %q, want the explicitly configured value to win", explicit.Model())
	}
}
