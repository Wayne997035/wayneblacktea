package validator

import "testing"

// TestRepoSlugRe covers the validator at the reconcile boundary. The regex is
// deliberately strict — owner/repo segments are restricted to GitHub's
// documented charset (ASCII letters, digits, dots, dashes, underscores) so
// the slug is safe to interpolate into a `gh -R <slug>` argv slot.
func TestRepoSlugRe(t *testing.T) {
	t.Parallel()

	accept := []string{
		"Wayne997035/wayneblacktea",
		"owner.dot/repo-name",
		"owner_under/repo.with.dots",
		"a/b",
		"A/B",
		"1/2",
		"my-org/my-repo",
		"foo_bar/baz-qux",
	}
	for _, s := range accept {
		t.Run("accept/"+s, func(t *testing.T) {
			t.Parallel()
			if !RepoSlugRe.MatchString(s) {
				t.Errorf("RepoSlugRe.MatchString(%q) = false, want true", s)
			}
		})
	}

	reject := []string{
		"",                  // empty
		"../etc/passwd",     // path traversal
		"owner/repo;rm -rf", // shell separator + space
		"owner/repo with space",
		"owner/repo\n",      // trailing newline
		"owner/repo\t",      // tab
		"owner",             // missing slash
		"/repo",             // empty owner
		"owner/",            // empty repo
		"owner//repo",       // double slash → 3 segments after split → matches "owner" / "" / "repo", fails first char class
		"owner/repo/extra",  // third segment
		"owner/repo|cmd",    // pipe
		"owner/repo`cmd`",   // backtick
		"owner/repo$(cmd)",  // command substitution
		"owner space/repo",  // space in owner
		"\x00owner/repo",    // null byte
		"owner\x00/repo",    // embedded null
		"owner/repo\x00",    // trailing null
		"owner/repo\r\nfoo", // CRLF smuggling
		"owner/.repo/x",     // path with leading dot segment + extra
	}
	for _, s := range reject {
		t.Run("reject/"+s, func(t *testing.T) {
			t.Parallel()
			if RepoSlugRe.MatchString(s) {
				t.Errorf("RepoSlugRe.MatchString(%q) = true, want false", s)
			}
		})
	}
}
