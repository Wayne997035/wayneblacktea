package mcp

import "testing"

// [F160-09] Recovered from an untracked worktree (pr160-lane-a) where it was
// written but never landed — parseSyncRepoOptionalArgs (tools_context.go:671)
// had zero test coverage on the integration branch before this file.

// TestF160_09_ParseSyncRepoOptionalArgs_PresenceSemantics pins Ω6
// (2026-08-20-mcp-surface-spec.md) at the MCP-args layer: parseSyncRepoOptionalArgs
// must distinguish "key absent from args" (nil — preserve the stored value)
// from "key present with an empty string" (non-nil pointer to "" — an
// explicit clear) and from "key present with a value" (non-nil pointer to
// that value). Folding the first two together is exactly the bug Ω6 fixed —
// every sync_repo call that omitted a field used to silently wipe it.
func TestF160_09_ParseSyncRepoOptionalArgs_PresenceSemantics(t *testing.T) {
	cases := []struct {
		name    string
		args    map[string]any
		wantNil bool
		want    string
	}{
		{
			name:    "key absent from args -> nil (preserve stored value on omission)",
			args:    map[string]any{},
			wantNil: true,
		},
		{
			name:    "key present with empty string -> non-nil pointer to \"\" (explicit clear)",
			args:    map[string]any{"path": ""},
			wantNil: false,
			want:    "",
		},
		{
			name:    "key present with a value -> non-nil pointer to that value",
			args:    map[string]any{"path": "/repos/wbt"},
			wantNil: false,
			want:    "/repos/wbt",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, errResult := parseSyncRepoOptionalArgs(tc.args)
			if errResult != nil {
				t.Fatalf("unexpected error result: %+v", errResult)
			}
			if tc.wantNil {
				if got.path != nil {
					t.Errorf("path = %q, want nil", *got.path)
				}
				return
			}
			if got.path == nil {
				t.Fatalf("path = nil, want non-nil pointer to %q", tc.want)
			}
			if *got.path != tc.want {
				t.Errorf("path = %q, want %q", *got.path, tc.want)
			}
		})
	}
}

// TestF160_09_ParseSyncRepoOptionalArgs_AllFieldsWired confirms each of the 5
// optional fields lands in its own matching struct field — a copy-paste
// duplication bug (e.g. "language" accidentally parsed twice instead of
// "current_branch") would pass a single-field test like the one above but
// fail here.
func TestF160_09_ParseSyncRepoOptionalArgs_AllFieldsWired(t *testing.T) {
	args := map[string]any{
		"path":              "p",
		"description":       "d",
		"language":          "l",
		"current_branch":    "c",
		"next_planned_step": "n",
	}
	got, errResult := parseSyncRepoOptionalArgs(args)
	if errResult != nil {
		t.Fatalf("unexpected error result: %+v", errResult)
	}
	checks := []struct {
		name string
		got  *string
		want string
	}{
		{"path", got.path, "p"},
		{"description", got.description, "d"},
		{"language", got.language, "l"},
		{"current_branch", got.currentBranch, "c"},
		{"next_planned_step", got.nextPlannedStep, "n"},
	}
	for _, c := range checks {
		if c.got == nil {
			t.Errorf("%s = nil, want pointer to %q", c.name, c.want)
			continue
		}
		if *c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, *c.got, c.want)
		}
	}
}

// TestF160_09_ParseSyncRepoOptionalArgs_TypeMismatchPropagatesError pins the
// existing error path: a non-string value for any of the 5 fields returns a
// non-nil tool-error result rather than panicking or silently coercing.
func TestF160_09_ParseSyncRepoOptionalArgs_TypeMismatchPropagatesError(t *testing.T) {
	_, errResult := parseSyncRepoOptionalArgs(map[string]any{"path": 42})
	if errResult == nil {
		t.Fatal("expected a non-nil error result for a non-string path value")
	}
	if !errResult.IsError {
		t.Errorf("errResult.IsError = false, want true")
	}
}
