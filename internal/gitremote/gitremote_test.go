package gitremote

import "testing"

// TestDeriveGitHubSlug moved verbatim from internal/cli's TestDeriveRepoSlug
// with the function ([F0925-31]); behaviour is unchanged.
func TestDeriveGitHubSlug(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"https://github.com/owner/repo.git", "owner/repo"},
		{"https://github.com/owner/repo", "owner/repo"},
		{"git@github.com:owner/repo.git", "owner/repo"},
		{"git@github.com:owner/repo", "owner/repo"},
		{"ssh://git@github.com/owner/repo.git", "owner/repo"},
		{"https://gitlab.com/owner/repo.git", ""},
		{"", ""},
		{"not a url", ""},
	}
	for _, c := range cases {
		if got := DeriveGitHubSlug(c.in); got != c.want {
			t.Errorf("DeriveGitHubSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
