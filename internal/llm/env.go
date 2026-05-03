package llm

import (
	"log/slog"
	"os"
	"strings"
)

// Env keys consumed by BuildChainFromEnv. Documented in .env.example.
const (
	envProvider          = "AI_PROVIDER"
	envFallbackProviders = "AI_FALLBACK_PROVIDERS"
	envOpenRouterKey     = "OPENROUTER_API_KEY"
	envOpenRouterModel   = "OPENROUTER_MODEL"
	envOpenRouterModels  = "OPENROUTER_MODELS"
	envClaudeKey         = "CLAUDE_API_KEY"
	envGroqKey           = "GROQ_API_KEY"
	envGroqModel         = "GROQ_MODEL"
)

// BuildChainFromEnv reads the provider routing env vars and returns a Chain
// composed of the providers whose keys are actually present.
//
// Resolution order:
//  1. If AI_FALLBACK_PROVIDERS is set, use that comma-separated list as the
//     ordered preference. Each entry is one of "claude", "openrouter", "groq".
//  2. Else if AI_PROVIDER is set, use only that single provider.
//  3. Else (legacy / backward compat): fall back to the historical "if a key
//     is set, that provider is active" behaviour. The order is
//     claude → openrouter → groq, biased toward Claude because the existing
//     classifier/reviewer call sites were Claude-only.
//
// Providers whose API key is empty are silently skipped — this is NOT a retry
// attempt; missing-key never reaches the chain.
//
// Configuration errors (e.g. OPENROUTER_API_KEY set but neither
// OPENROUTER_MODEL nor OPENROUTER_MODELS) are logged at WARN and the provider
// is skipped — degrading gracefully is preferred over a hard startup failure
// for an optional feature.
func BuildChainFromEnv() *Chain {
	order := resolveOrder()
	out := make([]JSONClient, 0, len(order))
	for _, name := range order {
		switch name {
		case "claude":
			c, err := NewClaudeClient(ClaudeConfig{APIKey: os.Getenv(envClaudeKey)})
			if err != nil {
				slog.Warn("llm: claude provider config error, skipping", "err", err)
				continue
			}
			if c != nil {
				out = append(out, c)
			}
		case "openrouter":
			c, err := NewOpenRouterClient(OpenRouterConfig{
				APIKey: os.Getenv(envOpenRouterKey),
				Model:  os.Getenv(envOpenRouterModel),
				Models: parseModelsList(os.Getenv(envOpenRouterModels)),
			})
			if err != nil {
				slog.Warn("llm: openrouter provider config error, skipping", "err", err)
				continue
			}
			if c != nil {
				out = append(out, c)
			}
		case "groq":
			c, err := NewGroqClient(GroqConfig{
				APIKey: os.Getenv(envGroqKey),
				Model:  os.Getenv(envGroqModel),
			})
			if err != nil {
				slog.Warn("llm: groq provider config error, skipping", "err", err)
				continue
			}
			if c != nil {
				out = append(out, c)
			}
		default:
			slog.Warn("llm: unknown provider name in chain config, skipping", "name", name)
		}
	}
	return NewChain(out...)
}

// resolveOrder returns the ordered list of provider names to attempt, before
// any missing-key filtering. See BuildChainFromEnv for the resolution rules.
func resolveOrder() []string {
	if raw := os.Getenv(envFallbackProviders); raw != "" {
		return splitAndDedupe(raw)
	}
	if single := strings.TrimSpace(os.Getenv(envProvider)); single != "" {
		return []string{strings.ToLower(single)}
	}
	// Legacy backward-compat order: keep Claude as the historical head so
	// "only CLAUDE_API_KEY set" gives a single-Claude chain identical to
	// the pre-refactor behaviour.
	return []string{"claude", "openrouter", "groq"}
}

// splitAndDedupe parses "a,b,a,c" into ["a", "b", "c"] (lower-cased, trimmed).
// Empty entries are dropped.
func splitAndDedupe(raw string) []string {
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		name := strings.ToLower(strings.TrimSpace(p))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

// parseModelsList parses "model-a,model-b,openrouter/free" into a slice with
// whitespace trimmed. Empty input returns nil so the OpenRouterClient falls
// back to the single Model field.
func parseModelsList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
