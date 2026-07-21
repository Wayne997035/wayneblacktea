package gtd

import (
	"strings"
	"testing"
)

// TestNormalizeActor_KnownProductionValues covers every distinct raw
// assignee spelling observed in production (see p6-6 dispatch) plus the
// bare canonical values, asserting each resolves (or correctly fails to
// resolve) exactly as designed.
func TestNormalizeActor_KnownProductionValues(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		want      string
		wantError bool
	}{
		{name: "claude bare", raw: "claude", want: "claude"},
		{name: "codex bare", raw: "codex", want: "codex"},
		{name: "backend is a role not an actor", raw: "backend", wantError: true},
		{name: "Codex/Claude dual-actor ambiguous", raw: "Codex/Claude", wantError: true},
		{name: "claude/codex dual-actor ambiguous", raw: "claude/codex", wantError: true},
		{name: "Codex Lead alias", raw: "Codex Lead", want: "codex"},
		{name: "claude engineer dispatch alias", raw: "claude (engineer dispatch)", want: "claude"},
		{name: "claude-code alias", raw: "claude-code", want: "claude"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeActor(tc.raw)
			if tc.wantError {
				if err == nil {
					t.Fatalf("NormalizeActor(%q) = %q, nil; want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeActor(%q) unexpected error: %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("NormalizeActor(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestNormalizeActor_CaseAndWhitespaceInsensitive verifies alias matching
// ignores case and leading/trailing whitespace.
func TestNormalizeActor_CaseAndWhitespaceInsensitive(t *testing.T) {
	cases := []string{"CLAUDE", "  claude  ", "Claude-Code", " CODEX LEAD "}
	want := []string{"claude", "claude", "claude", "codex"}
	for i, raw := range cases {
		got, err := NormalizeActor(raw)
		if err != nil {
			t.Fatalf("NormalizeActor(%q) unexpected error: %v", raw, err)
		}
		if got != want[i] {
			t.Errorf("NormalizeActor(%q) = %q, want %q", raw, got, want[i])
		}
	}
}

// TestNormalizeActor_UnknownValue asserts an unrecognized actor is rejected
// (whitelist, not blacklist) and the error message lists the allowlist
// without echoing the raw input back (backend-security-design.md §5.4-style
// sanitisation — untrusted input must not round-trip into an error string).
func TestNormalizeActor_UnknownValue(t *testing.T) {
	raw := "gemini"
	_, err := NormalizeActor(raw)
	if err == nil {
		t.Fatal("expected error for unrecognized actor \"gemini\"")
	}
	msg := err.Error()
	for _, canonical := range CanonicalActors {
		if !strings.Contains(msg, canonical) {
			t.Errorf("error message should list canonical value %q, got: %s", canonical, msg)
		}
	}
	if strings.Contains(msg, raw) {
		t.Errorf("error message must not echo raw input %q, got: %s", raw, msg)
	}
}

// TestNormalizeActor_EmptyInput asserts empty/whitespace-only input is
// rejected rather than silently resolving to some default actor.
func TestNormalizeActor_EmptyInput(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		if _, err := NormalizeActor(raw); err == nil {
			t.Errorf("NormalizeActor(%q) should error, got nil", raw)
		}
	}
}

// TestDryRunBackfillActors_ProductionSample runs the full set of distinct
// production assignee spellings through the dry-run classifier. Ambiguous
// dual-actor and role-label values MUST be flagged for manual decision, not
// auto-mapped to a guess.
func TestDryRunBackfillActors_ProductionSample(t *testing.T) {
	candidates := []ActorBackfillCandidate{
		{ID: "t-claude", Raw: "claude"},
		{ID: "t-codex", Raw: "codex"},
		{ID: "t-backend", Raw: "backend"},
		{ID: "t-codex-claude-slash", Raw: "Codex/Claude"},
		{ID: "t-claude-codex-slash", Raw: "claude/codex"},
		{ID: "t-codex-lead", Raw: "Codex Lead"},
		{ID: "t-claude-dispatch", Raw: "claude (engineer dispatch)"},
		{ID: "t-claude-code", Raw: "claude-code"},
	}
	results := DryRunBackfillActors(candidates)
	if len(results) != len(candidates) {
		t.Fatalf("expected %d results, got %d", len(candidates), len(results))
	}

	byID := make(map[string]ActorBackfillResult, len(results))
	for _, r := range results {
		byID[r.ID] = r
	}

	wantSuggested := map[string]string{
		"t-claude":          "claude",
		"t-codex":           "codex",
		"t-codex-lead":      "codex",
		"t-claude-dispatch": "claude",
		"t-claude-code":     "claude",
	}
	for id, want := range wantSuggested {
		r, ok := byID[id]
		if !ok {
			t.Fatalf("missing result for %s", id)
		}
		if r.NeedsManualDecision {
			t.Errorf("%s: expected auto-suggestion %q, got NeedsManualDecision (reason: %s)", id, want, r.Reason)
		}
		if r.SuggestedActor != want {
			t.Errorf("%s: SuggestedActor = %q, want %q", id, r.SuggestedActor, want)
		}
	}

	wantManual := []string{"t-backend", "t-codex-claude-slash", "t-claude-codex-slash"}
	for _, id := range wantManual {
		r, ok := byID[id]
		if !ok {
			t.Fatalf("missing result for %s", id)
		}
		if !r.NeedsManualDecision {
			t.Errorf("%s: expected NeedsManualDecision=true, got SuggestedActor=%q", id, r.SuggestedActor)
		}
		if r.SuggestedActor != "" {
			t.Errorf("%s: SuggestedActor must be empty when NeedsManualDecision, got %q", id, r.SuggestedActor)
		}
		if r.Reason == "" {
			t.Errorf("%s: Reason must be non-empty when NeedsManualDecision", id)
		}
	}
}

// TestDryRunBackfillActors_EmptyInput asserts the pure classifier handles an
// empty candidate slice without panicking and returns an empty (not nil)
// result slice.
func TestDryRunBackfillActors_EmptyInput(t *testing.T) {
	results := DryRunBackfillActors(nil)
	if results == nil {
		t.Fatal("expected non-nil empty slice for nil input")
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}
