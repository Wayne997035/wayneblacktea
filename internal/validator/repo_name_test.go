package validator

import (
	"fmt"
	"testing"
)

// TestIsValidRepoName covers the shared validator used by MCP tools, HTTP
// handlers, and the store layer (PG + SQLite) for project repo_name values
// (GTD c282cc04 item #2: validation sinks to a single source of truth).
func TestIsValidRepoName(t *testing.T) {
	t.Parallel()

	accept := []string{
		"", // empty is valid — repo_name is optional
		"wayneblacktea",
		"food-photo-log",
		"my_repo.name",
		"a",
		"A1",
		"1234567890",
	}
	for _, s := range accept {
		t.Run("accept/"+s, func(t *testing.T) {
			t.Parallel()
			if !IsValidRepoName(s) {
				t.Errorf("IsValidRepoName(%q) = false, want true", s)
			}
		})
	}

	reject := []string{
		"bad repo name!", // space + bang
		"owner/repo",     // slash not allowed (this is a slug, not owner/repo)
		"repo;rm -rf",    // shell separator
		"repo\n",         // trailing newline
		"repo\t",         // tab
		"repo\x00",       // null byte
		"../etc/passwd",  // path traversal
		"repo with space",
		"repo$(cmd)",
		"repo`cmd`",
		string(make([]byte, 101)), // 101 chars (all NUL, also fails length + charset)
	}
	for i, s := range reject {
		t.Run(fmt.Sprintf("reject/%d", i), func(t *testing.T) {
			t.Parallel()
			if IsValidRepoName(s) {
				t.Errorf("IsValidRepoName(%q) = true, want false", s)
			}
		})
	}
}

// TestIsValidRepoName_LengthBoundary verifies the {1,100} length bound.
func TestIsValidRepoName_LengthBoundary(t *testing.T) {
	t.Parallel()

	exactly100 := make([]byte, 100)
	for i := range exactly100 {
		exactly100[i] = 'a'
	}
	if !IsValidRepoName(string(exactly100)) {
		t.Error("100-char name should be valid (boundary)")
	}

	exactly101 := make([]byte, 101)
	for i := range exactly101 {
		exactly101[i] = 'a'
	}
	if IsValidRepoName(string(exactly101)) {
		t.Error("101-char name should be rejected (over boundary)")
	}
}
