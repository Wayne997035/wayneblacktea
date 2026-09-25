package main

import "testing"

// TestRepoNameFromCwd pins [F0925-29] for the wbt-guard automatic writer: a
// cwd basename breaking the workspace repo name rule is stored as empty
// (the event row is still written), a compliant one is kept.
func TestRepoNameFromCwd(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/Users/me/_project/wayneblacktea": "wayneblacktea",
		"/Users/me/_project":               "_project",
		"/Users/me/workspace/.hidden":      "",
		"/Users/me/-rf":                    "",
		"/Users/me/a b":                    "",
	}
	for cwd, want := range cases {
		if got := repoNameFromCwd(cwd); got != want {
			t.Errorf("repoNameFromCwd(%q) = %q, want %q", cwd, got, want)
		}
	}
}
