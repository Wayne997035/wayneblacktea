package llm

import (
	"context"
	"errors"
	"testing"
	"time"
)

// [F1124-50] [F1124-51] router tests use fake candidates only.

const (
	provGroq       = "groq"
	provOpenRouter = "openrouter"
	provOpenAICmp  = "openai-compatible"
)

type fakeRouteClient struct {
	name string
	caps Capabilities
}

func (f fakeRouteClient) Name() string               { return f.name }
func (f fakeRouteClient) Capabilities() Capabilities { return f.caps }
func (f fakeRouteClient) Generate(context.Context, GenerateRequest) (GenerateResult, error) {
	return GenerateResult{}, nil
}

func fc(name string, tools bool, ctx int) fakeRouteClient {
	return fakeRouteClient{name: name, caps: Capabilities{Text: true, ToolCalling: tools, MaxContext: ctx}}
}

func allHealthy(string) bool { return true }

func mustRouter(t *testing.T, cfg RouterConfig) *Router {
	t.Helper()
	if cfg.Healthy == nil {
		cfg.Healthy = allHealthy
	}
	r, err := NewRouter(cfg)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return r
}

func mustSelectName(t *testing.T, r *Router, req RoutingRequest) string {
	t.Helper()
	c, err := r.Select(req)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	return c.Name()
}

func mustSelectNone(t *testing.T, r *Router, req RoutingRequest) {
	t.Helper()
	c, err := r.Select(req)
	if !errors.Is(err, ErrNoCandidateSatisfiesRequest) || c != nil {
		t.Fatalf("want ErrNoCandidateSatisfiesRequest and nil candidate, got %v %v", c, err)
	}
}

func TestNewRouter_RejectsMissingHealthOrCandidates(t *testing.T) {
	cands := []GenerateClient{fc(claudeProviderName, true, 1000)}
	tests := []struct {
		name string
		cfg  RouterConfig
	}{
		{"nil healthy", RouterConfig{Candidates: cands}},
		{"no candidates", RouterConfig{Healthy: allHealthy}},
		{"nil candidate entry", RouterConfig{Healthy: allHealthy, Candidates: []GenerateClient{nil}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if r, err := NewRouter(tt.cfg); err == nil || r != nil {
				t.Fatalf("want error and nil router, got %v %v", r, err)
			}
		})
	}
	if _, err := NewRouter(RouterConfig{Healthy: allHealthy, Candidates: cands}); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestRouter_SelectsProviderByTaskClass(t *testing.T) {
	r := mustRouter(t, RouterConfig{Candidates: []GenerateClient{
		fc(provOpenRouter, true, 1000), fc(claudeProviderName, true, 1000), fc(provGroq, true, 1000),
	}})
	tests := []struct {
		class TaskClass
		want  string
	}{
		{"chat", provGroq},
		{"classify", provGroq},
		{"deep_reasoning", claudeProviderName},
		{"", provOpenRouter},              // empty class: Candidates order
		{"no_such_class", provOpenRouter}, // unknown class: Candidates order
	}
	for _, tt := range tests {
		t.Run(string(tt.class), func(t *testing.T) {
			if got := mustSelectName(t, r, RoutingRequest{TaskClass: tt.class}); got != tt.want {
				t.Fatalf("class %q: got %s want %s", tt.class, got, tt.want)
			}
		})
	}
	// Unlisted candidate sorts after listed ones.
	r2 := mustRouter(t, RouterConfig{Candidates: []GenerateClient{fc("custom", true, 1000), fc(provOpenAICmp, true, 1000)}})
	if got := mustSelectName(t, r2, RoutingRequest{TaskClass: "chat"}); got != provOpenAICmp {
		t.Fatalf("listed must beat unlisted, got %s", got)
	}
}

func TestRouter_FallsBackWhenCapabilityMissing(t *testing.T) {
	r := mustRouter(t, RouterConfig{Candidates: []GenerateClient{fc(provGroq, false, 1000), fc(claudeProviderName, true, 1000)}})
	if got := mustSelectName(t, r, RoutingRequest{TaskClass: "chat", RequiresToolCalling: true}); got != claudeProviderName {
		t.Fatalf("got %s want claude", got)
	}
	only := mustRouter(t, RouterConfig{Candidates: []GenerateClient{fc(provGroq, false, 1000)}})
	if _, err := only.Select(RoutingRequest{RequiresToolCalling: true}); !errors.Is(err, ErrNoCandidateSatisfiesRequest) {
		t.Fatalf("want ErrNoCandidateSatisfiesRequest, got %v", err)
	}
}

func TestRouter_ContextSizeExceedsAllCandidates(t *testing.T) {
	r := mustRouter(t, RouterConfig{Candidates: []GenerateClient{fc(provGroq, true, 1000), fc(claudeProviderName, true, 2000)}})
	if _, err := r.Select(RoutingRequest{ContextSizeTokens: 2001}); !errors.Is(err, ErrNoCandidateSatisfiesRequest) {
		t.Fatalf("want ErrNoCandidateSatisfiesRequest, got %v", err)
	}
	if got := mustSelectName(t, r, RoutingRequest{ContextSizeTokens: 1500}); got != claudeProviderName {
		t.Fatalf("got %s want claude", got)
	}
	if got := mustSelectName(t, r, RoutingRequest{ContextSizeTokens: 1000}); got != provGroq {
		t.Fatalf("boundary equal must fit, got %s", got)
	}
}

func TestRouter_LatencyTargetDoesNotBlockSelection(t *testing.T) {
	r := mustRouter(t, RouterConfig{Candidates: []GenerateClient{fc(provGroq, true, 1000), fc(claudeProviderName, true, 1000)}})
	done := make(chan string, 1)
	go func() {
		c, err := r.Select(RoutingRequest{LatencyTarget: time.Millisecond})
		if err != nil {
			done <- "err:" + err.Error()
			return
		}
		done <- c.Name()
	}()
	select {
	case got := <-done:
		if got != provGroq {
			t.Fatalf("got %s want groq (Candidates order)", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Select blocked")
	}
}

func TestRouter_ModalityFiltersCandidates(t *testing.T) {
	img := fakeRouteClient{name: provOpenRouter, caps: Capabilities{Text: true, ImageInput: true, MaxContext: 1000}}
	r := mustRouter(t, RouterConfig{Candidates: []GenerateClient{fc(provGroq, true, 1000), img, fc(claudeProviderName, true, 1000)}})
	if got := mustSelectName(t, r, RoutingRequest{Modality: Capabilities{ImageInput: true}}); got != provOpenRouter {
		t.Fatalf("got %s want openrouter", got)
	}
	if _, err := r.Select(RoutingRequest{Modality: Capabilities{AudioInput: true}}); !errors.Is(err, ErrNoCandidateSatisfiesRequest) {
		t.Fatalf("want ErrNoCandidateSatisfiesRequest, got %v", err)
	}
}

func TestRouter_CostBudgetExcludesExpensiveCandidates(t *testing.T) {
	r := mustRouter(t, RouterConfig{Candidates: []GenerateClient{fc(provGroq, true, 1000), fc(claudeProviderName, true, 1000)}})
	// Positive control: same candidates, no budget -> selected.
	if got := mustSelectName(t, r, RoutingRequest{}); got != provGroq {
		t.Fatalf("control: got %s", got)
	}
	if _, err := r.Select(RoutingRequest{CostBudgetMicroUSD: 1}); !errors.Is(err, ErrNoCandidateSatisfiesRequest) {
		t.Fatalf("want ErrNoCandidateSatisfiesRequest, got %v", err)
	}
	// preferred must not bypass the budget
	mustSelectNone(t, r, RoutingRequest{CostBudgetMicroUSD: 1_000_000, PreferredProvider: claudeProviderName})
}

func TestRouter_ExcludesUnhealthyProviders(t *testing.T) {
	healthy := func(p string) bool { return p != provGroq }
	r := mustRouter(t, RouterConfig{
		Healthy:    healthy,
		Candidates: []GenerateClient{fc(provGroq, true, 1000), fc(claudeProviderName, true, 1000)},
	})
	if got := mustSelectName(t, r, RoutingRequest{TaskClass: "chat"}); got != claudeProviderName {
		t.Fatalf("got %s want claude", got)
	}
	none := mustRouter(t, RouterConfig{Healthy: func(string) bool { return false }, Candidates: []GenerateClient{fc(provGroq, true, 1000)}})
	if _, err := none.Select(RoutingRequest{}); !errors.Is(err, ErrNoCandidateSatisfiesRequest) {
		t.Fatalf("want ErrNoCandidateSatisfiesRequest, got %v", err)
	}
}

func TestRouter_PreferredProviderOverridesTaskClassDefault(t *testing.T) {
	r := mustRouter(t, RouterConfig{Candidates: []GenerateClient{fc(provGroq, true, 1000), fc(claudeProviderName, true, 1000)}})
	// chat default is groq; preferred claude wins.
	if got := mustSelectName(t, r, RoutingRequest{TaskClass: "chat", PreferredProvider: claudeProviderName}); got != claudeProviderName {
		t.Fatalf("got %s want claude", got)
	}
	t.Run("unhealthy preferred falls to healthy", func(t *testing.T) {
		rr := mustRouter(t, RouterConfig{
			Healthy:    func(p string) bool { return p != claudeProviderName },
			Candidates: []GenerateClient{fc(claudeProviderName, true, 1000), fc(provGroq, true, 1000)},
		})
		if got := mustSelectName(t, rr, RoutingRequest{TaskClass: "deep_reasoning", PreferredProvider: claudeProviderName}); got != provGroq {
			t.Fatalf("got %s want groq", got)
		}
	})
	t.Run("budget not bypassed by preferred", func(t *testing.T) {
		mustSelectNone(t, r, RoutingRequest{PreferredProvider: claudeProviderName, CostBudgetMicroUSD: 5})
	})
	t.Run("preferred lacking capability not chosen", func(t *testing.T) {
		rr := mustRouter(t, RouterConfig{Candidates: []GenerateClient{fc(provGroq, false, 1000), fc(claudeProviderName, true, 1000)}})
		if got := mustSelectName(t, rr, RoutingRequest{PreferredProvider: provGroq, RequiresToolCalling: true}); got != claudeProviderName {
			t.Fatalf("got %s want claude", got)
		}
	})
}

func TestRouter_SameInputProducesSameOutput(t *testing.T) {
	r := mustRouter(t, RouterConfig{Candidates: []GenerateClient{
		fc(provOpenRouter, true, 1000), fc(claudeProviderName, true, 1000), fc(provGroq, true, 1000), fc(provOpenAICmp, true, 1000),
	}})
	req := RoutingRequest{TaskClass: "classify", RequiresToolCalling: true, ContextSizeTokens: 10}
	first := mustSelectName(t, r, req)
	for i := 0; i < 50; i++ {
		if got := mustSelectName(t, r, req); got != first {
			t.Fatalf("iteration %d: got %s want %s", i, got, first)
		}
	}
}

func TestRouter_PrivacySensitivityRestrictsCandidates(t *testing.T) {
	cands := []GenerateClient{fc(provGroq, true, 1000), fc(claudeProviderName, true, 1000)}
	t.Run("empty allowlist rejects private", func(t *testing.T) {
		r := mustRouter(t, RouterConfig{Candidates: cands})
		if _, err := r.Select(RoutingRequest{Sensitivity: "private"}); !errors.Is(err, ErrNoCandidateSatisfiesRequest) {
			t.Fatalf("want ErrNoCandidateSatisfiesRequest, got %v", err)
		}
	})
	t.Run("allowlist restricts to listed", func(t *testing.T) {
		r := mustRouter(t, RouterConfig{Candidates: cands, PrivateProviders: []string{claudeProviderName}})
		if got := mustSelectName(t, r, RoutingRequest{Sensitivity: "private", TaskClass: "chat"}); got != claudeProviderName {
			t.Fatalf("got %s want claude", got)
		}
	})
	t.Run("public and empty unrestricted", func(t *testing.T) {
		r := mustRouter(t, RouterConfig{Candidates: cands, PrivateProviders: []string{claudeProviderName}})
		for _, s := range []string{"", "public"} {
			if got := mustSelectName(t, r, RoutingRequest{Sensitivity: s, TaskClass: "chat"}); got != provGroq {
				t.Fatalf("sensitivity %q: got %s want groq", s, got)
			}
		}
	})
	t.Run("preferred outside allowlist is not chosen", func(t *testing.T) {
		r := mustRouter(t, RouterConfig{Candidates: cands, PrivateProviders: []string{claudeProviderName}})
		if got := mustSelectName(t, r, RoutingRequest{Sensitivity: "private", PreferredProvider: provGroq}); got != claudeProviderName {
			t.Fatalf("got %s want claude", got)
		}
	})
	t.Run("empty allowlist with preferred still rejected", func(t *testing.T) {
		r := mustRouter(t, RouterConfig{Candidates: cands})
		mustSelectNone(t, r, RoutingRequest{Sensitivity: "private", PreferredProvider: provGroq})
	})
}

func TestRouter_UnknownSensitivityRejected(t *testing.T) {
	r := mustRouter(t, RouterConfig{
		Candidates:       []GenerateClient{fc(provGroq, true, 1000), fc(claudeProviderName, true, 1000)},
		PrivateProviders: []string{claudeProviderName},
	})
	for _, label := range []string{"Private", "secret", " private", "PUBLIC"} {
		for _, pref := range []string{"", claudeProviderName} {
			c, err := r.Select(RoutingRequest{Sensitivity: label, PreferredProvider: pref})
			if !errors.Is(err, ErrUnknownSensitivity) {
				t.Fatalf("label %q pref %q: want ErrUnknownSensitivity, got %v", label, pref, err)
			}
			if errors.Is(err, ErrNoCandidateSatisfiesRequest) {
				t.Fatalf("label %q: must not be ErrNoCandidateSatisfiesRequest", label)
			}
			if c != nil {
				t.Fatalf("label %q: returned candidate %s", label, c.Name())
			}
		}
	}
}
