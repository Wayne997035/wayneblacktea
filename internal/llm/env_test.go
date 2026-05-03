package llm

import (
	"reflect"
	"testing"
)

// TestBuildChainFromEnv covers acceptance criteria 2-5 from the task brief:
// only-claude → single Claude provider; AI_PROVIDER=openrouter → OpenRouter
// only; explicit fallback list with missing keys → keys filtered upfront.
func TestBuildChainFromEnv(t *testing.T) {
	cases := []struct {
		name      string
		env       map[string]string
		wantNames []string
	}{
		{
			name:      "memory_only_when_no_keys",
			env:       map[string]string{},
			wantNames: []string{},
		},
		{
			name: "claude_only_legacy_path",
			env: map[string]string{
				"CLAUDE_API_KEY": "k",
			},
			wantNames: []string{"claude"},
		},
		{
			name: "ai_provider_openrouter_with_key",
			env: map[string]string{
				"AI_PROVIDER":        "openrouter",
				"OPENROUTER_API_KEY": "k",
				"OPENROUTER_MODEL":   "openrouter/free",
			},
			wantNames: []string{"openrouter"},
		},
		{
			name: "ai_provider_openrouter_without_key_collapses_to_empty",
			env: map[string]string{
				"AI_PROVIDER": "openrouter",
				// No OPENROUTER_API_KEY set.
			},
			wantNames: []string{},
		},
		{
			name: "fallback_list_filters_missing_keys",
			env: map[string]string{
				"AI_FALLBACK_PROVIDERS": "claude,openrouter,groq",
				"OPENROUTER_API_KEY":    "k",
				"OPENROUTER_MODEL":      "openrouter/free",
				// CLAUDE_API_KEY and GROQ_API_KEY deliberately unset.
			},
			wantNames: []string{"openrouter"},
		},
		{
			name: "fallback_list_preserves_order",
			env: map[string]string{
				"AI_FALLBACK_PROVIDERS": "groq,openrouter,claude",
				"GROQ_API_KEY":          "g",
				"OPENROUTER_API_KEY":    "o",
				"OPENROUTER_MODEL":      "openrouter/free",
				"CLAUDE_API_KEY":        "c",
			},
			wantNames: []string{"groq", "openrouter", "claude"},
		},
		{
			name: "openrouter_models_list_parsed",
			env: map[string]string{
				"AI_PROVIDER":        "openrouter",
				"OPENROUTER_API_KEY": "k",
				"OPENROUTER_MODEL":   "openrouter/free",
				"OPENROUTER_MODELS":  "a:free, b:free ,openrouter/free",
			},
			wantNames: []string{"openrouter"},
		},
		{
			name: "unknown_provider_in_fallback_list_skipped",
			env: map[string]string{
				"AI_FALLBACK_PROVIDERS": "claude,bogus,openrouter",
				"CLAUDE_API_KEY":        "c",
				"OPENROUTER_API_KEY":    "o",
				"OPENROUTER_MODEL":      "openrouter/free",
			},
			wantNames: []string{"claude", "openrouter"},
		},
		{
			name: "duplicate_entries_deduped",
			env: map[string]string{
				"AI_FALLBACK_PROVIDERS": "claude,claude,claude",
				"CLAUDE_API_KEY":        "c",
			},
			wantNames: []string{"claude"},
		},
	}

	allKeys := []string{
		"AI_PROVIDER", "AI_FALLBACK_PROVIDERS",
		"CLAUDE_API_KEY",
		"OPENROUTER_API_KEY", "OPENROUTER_MODEL", "OPENROUTER_MODELS",
		"GROQ_API_KEY", "GROQ_MODEL",
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset every relevant env var so cases do not bleed across t.Setenv.
			for _, k := range allKeys {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			chain := BuildChainFromEnv()
			got := chain.Names()
			// Treat nil and empty slice as equivalent so the empty-chain
			// case does not fail on reflect.DeepEqual semantics.
			if len(got) == 0 && len(tc.wantNames) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.wantNames) {
				t.Errorf("Names = %v, want %v", got, tc.wantNames)
			}
		})
	}
}

// TestParseModelsList covers OPENROUTER_MODELS parsing edge cases.
func TestParseModelsList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{"a, b , c", []string{"a", "b", "c"}},
		{"a,,b,", []string{"a", "b"}},
	}
	for _, tc := range cases {
		got := parseModelsList(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseModelsList(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
