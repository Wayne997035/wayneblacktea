package validator

import (
	"strings"
	"testing"
)

// TestCheckTaskInput verifies CheckTaskInput composes CheckVagueness (field
// fixed at "description") and CheckKindFields — the single-source validator
// list every task creation/acceptance entry point now routes through.
func TestCheckTaskInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		description string
		kind        string
		wantEmpty   bool
		wantContain string
	}{
		// happy path: descriptive text, no kind-specific requirements (general).
		{
			name:        "general kind descriptive text is clean",
			description: "Fix OOM in internal/gtd/store.go:392 by adding context deadline",
			kind:        "general",
			wantEmpty:   true,
		},
		// happy path: fix-pr kind with all required markers present.
		{
			name:        "fix-pr kind complete",
			description: "branch: fix/oom\nacceptance: TestOOMFix passes\ninternal/gtd/store.go:392 add deadline",
			kind:        "fix-pr",
			wantEmpty:   true,
		},
		// error: vagueness marker alone triggers a warning (CheckVagueness half).
		{
			name:        "vague marker triggers CheckVagueness",
			description: "TBD",
			kind:        "general",
			wantEmpty:   false,
			wantContain: "description: contains vague marker TBD",
		},
		// error: kind-specific field missing triggers a warning (CheckKindFields half).
		{
			name:        "fix-pr missing required fields triggers CheckKindFields",
			description: "some longer text without any of the required fix-pr markers present at all",
			kind:        "fix-pr",
			wantEmpty:   false,
			wantContain: "fix-pr task: description should contain \"branch:\"",
		},
		// edge: both halves fire at once — warnings from each check accumulate.
		{
			name:        "both CheckVagueness and CheckKindFields fire",
			description: "TBD",
			kind:        "fix-pr",
			wantEmpty:   false,
			wantContain: "fix-pr task: description should contain \"branch:\"",
		},
		// edge: chore kind skips CheckVagueness entirely and has no CheckKindFields rules.
		{
			name:        "chore kind skips all checks",
			description: "TBD",
			kind:        "chore",
			wantEmpty:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CheckTaskInput(tc.description, tc.kind)
			if tc.wantEmpty {
				if len(got) != 0 {
					t.Errorf("CheckTaskInput(%q, %q) = %v; want empty", tc.description, tc.kind, got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("CheckTaskInput(%q, %q) = empty; want non-empty warnings", tc.description, tc.kind)
			}
			found := false
			for _, w := range got {
				if strings.Contains(w, tc.wantContain) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("CheckTaskInput(%q, %q) = %v; want a warning containing %q", tc.description, tc.kind, got, tc.wantContain)
			}
		})
	}
}

// TestCheckTaskInput_MatchesManualComposition verifies CheckTaskInput's
// output is byte-for-byte identical to manually calling CheckVagueness +
// CheckKindFields in the same order — the exact inline pattern every call
// site used before this dedup. Guards against silent drift if either half's
// signature or order ever changes.
func TestCheckTaskInput_MatchesManualComposition(t *testing.T) {
	t.Parallel()

	cases := []struct {
		description string
		kind        string
	}{
		{"TBD", "fix-pr"},
		{"branch: fix/oom\nacceptance: works\nmain.go:1", "fix-pr"},
		{"a short one", "general"},
		{"", "research"},
	}

	for _, c := range cases {
		var want []string
		want = append(want, CheckVagueness("description", c.description, c.kind)...)
		want = append(want, CheckKindFields(c.kind, c.description)...)

		got := CheckTaskInput(c.description, c.kind)

		if len(got) != len(want) {
			t.Fatalf("CheckTaskInput(%q, %q) = %v; want %v", c.description, c.kind, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("CheckTaskInput(%q, %q)[%d] = %q; want %q", c.description, c.kind, i, got[i], want[i])
			}
		}
	}
}

// TestStrictModeEnabled_ParseBool verifies StrictModeEnabled reads
// WBT_STRICT_VAGUENESS through strconv.ParseBool, accepting the canonical
// truthy/falsy set. This is the single source now consumed by both the HTTP
// handler package and the MCP package (previously two duplicated env
// readers).
func TestStrictModeEnabled_ParseBool(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want bool
	}{
		{"truthy_1", "1", true},
		{"truthy_lower_t", "t", true},
		{"truthy_upper_T", "T", true},
		{"truthy_true", "true", true},
		{"truthy_True", "True", true},
		{"truthy_TRUE", "TRUE", true},
		{"falsy_0", "0", false},
		{"falsy_false", "false", false},
		{"falsy_empty", "", false},
		{"invalid_garbage_falsy", "yes", false},
		{"invalid_two_falsy", "2", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WBT_STRICT_VAGUENESS", tc.env)
			if got := StrictModeEnabled(); got != tc.want {
				t.Errorf("StrictModeEnabled() with env=%q = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}
