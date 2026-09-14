package llm

import (
	"net/http"
	"testing"
)

// [GTD a3fcbeb3] Every provider constructor used to write
// `safeClient.Timeout = <its own constant>` and believe it had set the
// request budget. It had set half of it: httpguard.NewSafeHTTPClient's
// Transport carries its own ResponseHeaderTimeout, and that is the one that
// decides how long the client will wait for the first response header.
//
// Measured in production 2026-09-14: a groq call whose source said 30s died
// at latency_ms=5011 with reason=timeout. 5011 matched none of groqTimeout
// (30s), drafterTimeout (15s) or decisionProposerTimeout (30s) — the number
// came from a constant in a different package that no caller had ever
// looked at.
//
// This test pins the CLASS, not the one provider that happened to break.
//
// MUTATION (manually verified, not shipped as code): reverting groq.go to
// `safeClient.Timeout = groqTimeout` turns ONLY the groq subtest red
// ("ResponseHeaderTimeout = 5s but Client.Timeout = 30s") and leaves
// openrouter and openai-compatible green — so the guard localises the
// regression to the caller that reintroduced it rather than just going red
// somewhere.

// transportOf returns the *http.Transport behind a client, failing the test
// when the client is not shaped the way every provider here builds it. A
// provider that switched to some other Transport would silently escape the
// assertion below, so this reports that rather than skipping.
func transportOf(t *testing.T, name string, c *http.Client) *http.Transport {
	t.Helper()
	if c == nil {
		t.Fatalf("%s: client is nil", name)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("%s: Transport is %T, not *http.Transport — the header-timeout "+
			"assertion below cannot see it, so this provider is unguarded", name, c.Transport)
	}
	return tr
}

// TestProviderClients_HeaderBudgetMatchesTheDeclaredBudget asserts the
// invariant rather than three magic numbers: whatever total budget a provider
// declares, waiting for the first header must be allowed to use it. An LLM
// that thinks before answering spends most of the request before the header
// arrives, so a smaller header budget can only reject work that was going to
// succeed.
func TestProviderClients_HeaderBudgetMatchesTheDeclaredBudget(t *testing.T) {
	groq, err := NewGroqClient(GroqConfig{APIKey: "k"})
	if err != nil {
		t.Fatalf("NewGroqClient: %v", err)
	}
	openrouter, err := NewOpenRouterClient(OpenRouterConfig{APIKey: "k", Model: "m"})
	if err != nil {
		t.Fatalf("NewOpenRouterClient: %v", err)
	}
	oai, err := NewOpenAICompatibleClient(OpenAICompatibleConfig{
		BaseURL: "http://localhost:11434", Model: "m",
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleClient: %v", err)
	}

	for _, tc := range []struct {
		name   string
		client *http.Client
	}{
		{"groq", groq.http},
		{"openrouter", openrouter.http},
		{"openai-compatible", oai.http},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := transportOf(t, tc.name, tc.client)
			if tc.client.Timeout <= 0 {
				t.Fatalf("%s: Client.Timeout is %v — no budget declared at all",
					tc.name, tc.client.Timeout)
			}
			if tr.ResponseHeaderTimeout != tc.client.Timeout {
				t.Errorf("%s: ResponseHeaderTimeout = %v but Client.Timeout = %v. The smaller "+
					"of the two is the real ceiling, so this provider's declared budget is a "+
					"fiction — exactly the defect that killed decision_draft at 5011ms while "+
					"its source said 30s",
					tc.name, tr.ResponseHeaderTimeout, tc.client.Timeout)
			}
		})
	}
}
