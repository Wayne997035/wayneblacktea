package validator

import (
	"strings"
	"testing"
)

func TestIsValidKind(t *testing.T) {
	t.Parallel()

	valid := []string{"general", "fix-pr", "feature", "refactor", "research", "chore"}
	for _, k := range valid {
		if !IsValidKind(k) {
			t.Errorf("IsValidKind(%q) = false; want true", k)
		}
	}

	invalid := []string{"", "bug", "GENERAL", "Fix-PR", "unknown", "sprint"}
	for _, k := range invalid {
		if IsValidKind(k) {
			t.Errorf("IsValidKind(%q) = true; want false", k)
		}
	}
}

func TestCheckKindFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		kind        string
		description string
		wantEmpty   bool
		wantContain string
	}{
		// fix-pr happy path
		{
			name:        "fix-pr complete",
			kind:        "fix-pr",
			description: "branch: feature/fix-oom\nacceptance: TestOOMFix passes\ninternal/gtd/store.go:392 add deadline",
			wantEmpty:   true,
		},
		// fix-pr missing branch
		{
			name:        "fix-pr missing branch",
			kind:        "fix-pr",
			description: "acceptance: tests pass\ninternal/gtd/store.go:10 fix",
			wantEmpty:   false,
			wantContain: "branch:",
		},
		// fix-pr missing acceptance
		{
			name:        "fix-pr missing acceptance",
			kind:        "fix-pr",
			description: "branch: fix/foo\ninternal/gtd/store.go:10 fix",
			wantEmpty:   false,
			wantContain: "acceptance:",
		},
		// fix-pr missing file:line
		{
			name:        "fix-pr missing fileline",
			kind:        "fix-pr",
			description: "branch: fix/foo\nacceptance: tests pass",
			wantEmpty:   false,
			wantContain: "file:line",
		},
		// feature happy path
		{
			name:        "feature complete",
			kind:        "feature",
			description: "acceptance: user can create task with kind\nrisk: low — additive change only",
			wantEmpty:   true,
		},
		// feature missing risk
		{
			name:        "feature missing risk",
			kind:        "feature",
			description: "acceptance: tests pass",
			wantEmpty:   false,
			wantContain: "risk:",
		},
		// feature missing acceptance
		{
			name:        "feature missing acceptance",
			kind:        "feature",
			description: "risk: low",
			wantEmpty:   false,
			wantContain: "acceptance:",
		},
		// refactor happy path
		{
			name:        "refactor complete",
			kind:        "refactor",
			description: "scope: internal/gtd\nnon-goals: adding new features",
			wantEmpty:   true,
		},
		// refactor missing non-goals
		{
			name:        "refactor missing non-goals",
			kind:        "refactor",
			description: "scope: internal/gtd only",
			wantEmpty:   false,
			wantContain: "non-goals:",
		},
		// refactor missing scope
		{
			name:        "refactor missing scope",
			kind:        "refactor",
			description: "non-goals: no new features",
			wantEmpty:   false,
			wantContain: "scope:",
		},
		// research happy path
		{
			name:        "research complete",
			kind:        "research",
			description: "question: why does OOM occur?\nsuccess-criteria: root cause identified and documented",
			wantEmpty:   true,
		},
		// research missing question
		{
			name:        "research missing question",
			kind:        "research",
			description: "success-criteria: answer found",
			wantEmpty:   false,
			wantContain: "question:",
		},
		// research missing success-criteria
		{
			name:        "research missing success-criteria",
			kind:        "research",
			description: "question: what is the answer?",
			wantEmpty:   false,
			wantContain: "success-criteria:",
		},
		// general: no required fields
		{
			name:        "general no required fields",
			kind:        "general",
			description: "anything goes here",
			wantEmpty:   true,
		},
		// chore: no required fields
		{
			name:        "chore no required fields",
			kind:        "chore",
			description: "",
			wantEmpty:   true,
		},
		// case insensitivity check
		{
			name:        "feature case insensitive matching",
			kind:        "feature",
			description: "Acceptance: tests pass\nRisk: medium",
			wantEmpty:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CheckKindFields(tc.kind, tc.description)
			if tc.wantEmpty {
				if len(got) != 0 {
					t.Errorf("CheckKindFields(%q, ...) = %v; want empty", tc.kind, got)
				}
				return
			}
			if len(got) == 0 {
				t.Errorf("CheckKindFields(%q, ...) = empty; want warnings", tc.kind)
				return
			}
			if tc.wantContain != "" {
				found := false
				for _, w := range got {
					if strings.Contains(w, tc.wantContain) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("CheckKindFields(%q, ...) = %v; want warning containing %q", tc.kind, got, tc.wantContain)
				}
			}
		})
	}
}
