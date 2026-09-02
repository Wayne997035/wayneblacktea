package proposal

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestDecodeGoalParams_LengthCaps guards against unbounded Title/Description
// bytes reaching gtd.CreateGoalParams — see backend-security-design.md §2.1
// ("LLM tool input is hostile"): a prompt-injected agent controls
// pending_proposals.payload via propose_goal, and this decoder is reachable
// from POST /api/proposals/:id/confirm (and, after the ConfirmBatch fix,
// /api/proposals/confirm-batch too).
func TestDecodeGoalParams_LengthCaps(t *testing.T) {
	cases := []struct {
		name       string
		payload    map[string]any
		wantErr    bool
		wantSubstr string
	}{
		{
			name:    "within limits → ok",
			payload: map[string]any{"title": "Become CEO", "area": "career", "description": "short"},
			wantErr: false,
		},
		{
			name:       "title exceeds 512 bytes → rejected",
			payload:    map[string]any{"title": strings.Repeat("a", 513), "area": "career"},
			wantErr:    true,
			wantSubstr: "goal title exceeds 512 bytes",
		},
		{
			name:       "description exceeds 64 KB → rejected",
			payload:    map[string]any{"title": "ok", "area": "career", "description": strings.Repeat("b", 65537)},
			wantErr:    true,
			wantSubstr: "goal description exceeds 64 KB",
		},
		{
			name:    "title exactly 512 bytes → ok (boundary)",
			payload: map[string]any{"title": strings.Repeat("a", 512), "area": "career"},
			wantErr: false,
		},
		{
			// M1 (round-2 security review): area had no length cap while
			// title/description did, so a prompt-injected agent could stuff
			// unbounded content into area to bypass the other two caps.
			name:       "area exceeds 512 bytes → rejected",
			payload:    map[string]any{"title": "ok", "area": strings.Repeat("z", 513)},
			wantErr:    true,
			wantSubstr: "goal area exceeds 512 bytes",
		},
		{
			name:    "area exactly 512 bytes → ok (boundary)",
			payload: map[string]any{"title": "ok", "area": strings.Repeat("z", 512)},
			wantErr: false,
		},
		{
			name:       "empty title → rejected",
			payload:    map[string]any{"title": "", "area": "career"},
			wantErr:    true,
			wantSubstr: "goal payload missing title",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			_, err = DecodeGoalParams(raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("DecodeGoalParams: want error, got nil")
				}
				if !strings.Contains(err.Error(), tc.wantSubstr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.wantSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeGoalParams: unexpected error: %v", err)
			}
		})
	}
}

// TestDecodeProjectParams_LengthCaps mirrors TestDecodeGoalParams_LengthCaps
// for Name/Title/Description — see that test's doc comment for the threat
// model.
func TestDecodeProjectParams_LengthCaps(t *testing.T) {
	cases := []struct {
		name       string
		payload    map[string]any
		wantErr    bool
		wantSubstr string
	}{
		{
			name:    "within limits → ok",
			payload: map[string]any{"name": "proj", "title": "Project", "area": "projects"},
			wantErr: false,
		},
		{
			name:       "name exceeds 512 bytes → rejected",
			payload:    map[string]any{"name": strings.Repeat("a", 513), "title": "ok", "area": "projects"},
			wantErr:    true,
			wantSubstr: "project name exceeds 512 bytes",
		},
		{
			name:       "title exceeds 512 bytes → rejected",
			payload:    map[string]any{"name": "ok", "title": strings.Repeat("b", 513), "area": "projects"},
			wantErr:    true,
			wantSubstr: "project title exceeds 512 bytes",
		},
		{
			name:       "description exceeds 64 KB → rejected",
			payload:    map[string]any{"name": "ok", "title": "ok", "description": strings.Repeat("c", 65537)},
			wantErr:    true,
			wantSubstr: "project description exceeds 64 KB",
		},
		{
			// M1 (round-2 security review): same area-cap bypass as
			// TestDecodeGoalParams_LengthCaps's "area exceeds 512 bytes" case.
			name:       "area exceeds 512 bytes → rejected",
			payload:    map[string]any{"name": "ok", "title": "ok", "area": strings.Repeat("z", 513)},
			wantErr:    true,
			wantSubstr: "project area exceeds 512 bytes",
		},
		{
			name:    "area exactly 512 bytes → ok (boundary)",
			payload: map[string]any{"name": "ok", "title": "ok", "area": strings.Repeat("z", 512)},
			wantErr: false,
		},
		{
			name:       "empty title → rejected",
			payload:    map[string]any{"name": "ok", "title": ""},
			wantErr:    true,
			wantSubstr: "project payload missing title",
		},
		{
			// Minor 1 (round-2 security review): projects.priority has a
			// CHECK (priority BETWEEN 1 AND 5); an out-of-range value must be
			// rejected here (400) rather than reaching the DB and raising pg
			// 23514 inside the accept transaction (500) — see
			// backend-security-design.md §2.1.
			name:       "priority out of range → rejected",
			payload:    map[string]any{"name": "ok", "title": "ok", "priority": 99},
			wantErr:    true,
			wantSubstr: "project priority must be 1-5",
		},
		{
			name:    "priority 1 → ok (boundary)",
			payload: map[string]any{"name": "ok", "title": "ok", "priority": 1},
			wantErr: false,
		},
		{
			name:    "priority 5 → ok (boundary)",
			payload: map[string]any{"name": "ok", "title": "ok", "priority": 5},
			wantErr: false,
		},
		{
			name:    "priority 0 (unset) → ok",
			payload: map[string]any{"name": "ok", "title": "ok"},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			_, err = DecodeProjectParams(raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("DecodeProjectParams: want error, got nil")
				}
				if !strings.Contains(err.Error(), tc.wantSubstr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.wantSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeProjectParams: unexpected error: %v", err)
			}
		})
	}
}

// TestDecodeDecisionParams_LengthCaps mirrors TestDecodeGoalParams_LengthCaps
// for DecodeDecisionParams's Title/Decision/Rationale/Alternatives caps. This
// decoder is the last of the six proposal decoders that previously had no
// upper bound — decision is the highest-volume proposal type in production
// (838 rows at time of writing), and Alternatives is concatenated into
// Rationale before the row is written, so an unbounded Alternatives slice
// amplifies the final Rationale size (see backend-security-design.md §2.1).
func TestDecodeDecisionParams_LengthCaps(t *testing.T) {
	cases := []struct {
		name       string
		payload    map[string]any
		wantErr    bool
		wantSubstr string
	}{
		{
			name:    "within limits → ok",
			payload: map[string]any{"title": "adopt X", "decision": "adopt X", "rationale": "because Y", "alternatives": []string{"Y", "Z"}},
			wantErr: false,
		},
		{
			name:       "title exceeds 512 bytes → rejected",
			payload:    map[string]any{"title": strings.Repeat("a", 513)},
			wantErr:    true,
			wantSubstr: "decision title exceeds 512 bytes",
		},
		{
			name:       "decision text exceeds 64 KB → rejected",
			payload:    map[string]any{"title": "ok", "decision": strings.Repeat("b", 65537)},
			wantErr:    true,
			wantSubstr: "decision text exceeds 64 KB",
		},
		{
			name:       "rationale exceeds 64 KB → rejected",
			payload:    map[string]any{"title": "ok", "rationale": strings.Repeat("c", 65537)},
			wantErr:    true,
			wantSubstr: "decision rationale exceeds 64 KB",
		},
		{
			name:       "too many alternatives → rejected",
			payload:    map[string]any{"title": "ok", "alternatives": makeStrings(51, "x")},
			wantErr:    true,
			wantSubstr: "too many alternatives (max 50)",
		},
		{
			name:       "single alternative exceeds 100 bytes → rejected",
			payload:    map[string]any{"title": "ok", "alternatives": []string{strings.Repeat("d", 101)}},
			wantErr:    true,
			wantSubstr: "individual alternative exceeds 100 bytes",
		},
		{
			name:    "title exactly 512 bytes → ok (boundary)",
			payload: map[string]any{"title": strings.Repeat("a", 512)},
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			_, err = DecodeDecisionParams(raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("DecodeDecisionParams: want error, got nil")
				}
				if !strings.Contains(err.Error(), tc.wantSubstr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tc.wantSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeDecisionParams: unexpected error: %v", err)
			}
		})
	}
}

// makeStrings returns n copies of val — used to build an oversized
// Alternatives slice fixture without a literal 51-element list.
func makeStrings(n int, val string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = val
	}
	return out
}

// invalidKindDecodeTestDescription passes CheckTaskInput cleanly for
// kind=general (≥30 runes, contains a file:line reference) so the tests
// below assert exactly one warning (the kind warning).
const invalidKindDecodeTestDescription = "Fix kind coercion at internal/handler/proposal_handler.go:240"

// TestDecodeTaskParams_InvalidKind (GTD f457740e / [F0902-54]): DecodeTaskParams
// is the A1-seam copy of the same kind-coercion logic the other 3 call sites
// share — not yet production-reachable for TypeTask (see doc comment on
// DecodeTaskParams), but must stay in lockstep so wiring this seam later
// (A1-seam follow-up, GTD 6d960dce / 1fa9889c) doesn't reintroduce the bug
// that previously discarded an invalid suggested_kind silently. Table-driven
// (gocyclo) — mutation check: blank ResolveTaskKind's warning line and the
// "bogus kind" rows below fail red while "empty kind" stays green.
func TestDecodeTaskParams_InvalidKind(t *testing.T) {
	tests := []struct {
		name              string
		suggestedKind     string
		strict            bool
		wantErrSubstrs    []string // non-empty ⇒ expect a non-nil error containing all of these
		wantKind          string   // checked only when wantErrSubstrs is empty
		wantWarningCount  int
		wantWarningSubstr string
	}{
		{
			name:              "non-strict bogus kind → general + warning",
			suggestedKind:     "bogus",
			wantKind:          "general",
			wantWarningCount:  1,
			wantWarningSubstr: "bogus",
		},
		{
			name:             "empty kind → general, no warning",
			suggestedKind:    "",
			wantKind:         "general",
			wantWarningCount: 0,
		},
		{
			name:              "strict bogus kind → error contains bogus",
			suggestedKind:     "bogus",
			strict:            true,
			wantErrSubstrs:    []string{"vagueness check failed", "bogus"},
			wantWarningCount:  1,
			wantWarningSubstr: "bogus",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := json.Marshal(TaskPayload{
				Title:         "Kind test task",
				Description:   invalidKindDecodeTestDescription,
				SuggestedKind: tc.suggestedKind,
				SourceTool:    "test",
			})
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			params, warnings, decErr := DecodeTaskParams(payload, tc.strict)

			if len(tc.wantErrSubstrs) > 0 {
				if decErr == nil {
					t.Fatal("DecodeTaskParams: want error, got nil")
				}
				for _, substr := range tc.wantErrSubstrs {
					if !strings.Contains(decErr.Error(), substr) {
						t.Errorf("error = %q, want it to contain %q", decErr.Error(), substr)
					}
				}
			} else {
				if decErr != nil {
					t.Fatalf("DecodeTaskParams: unexpected error: %v", decErr)
				}
				if params.Kind != tc.wantKind {
					t.Errorf("params.Kind = %q, want %q", params.Kind, tc.wantKind)
				}
			}

			if len(warnings) != tc.wantWarningCount {
				t.Fatalf("warnings = %v, want %d warning(s)", warnings, tc.wantWarningCount)
			}
			if tc.wantWarningSubstr != "" && !strings.Contains(warnings[0], tc.wantWarningSubstr) {
				t.Errorf("warnings[0] = %q, want substring %q", warnings[0], tc.wantWarningSubstr)
			}
		})
	}
}
