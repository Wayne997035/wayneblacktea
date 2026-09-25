// Package gitremote turns a git remote URL into the GitHub owner/repo slug.
// It is shared by the post-merge hook (internal/cli) and cmd/seed, which
// both need the slug that `gh -R` and repos.github_slug expect ([F0925-31]).
package gitremote

import (
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/validator"
)

// DeriveGitHubSlug normalizes a GitHub remote URL to "owner/repo".
// Non-github.com remotes (GitLab, Bitbucket, etc.) return "" — out of scope
// for PR reconcile.
func DeriveGitHubSlug(originURL string) string {
	u := strings.TrimSpace(originURL)
	if u == "" {
		return ""
	}
	var path string
	switch {
	case strings.HasPrefix(u, "https://github.com/"):
		path = strings.TrimPrefix(u, "https://github.com/")
	case strings.HasPrefix(u, "git@github.com:"):
		path = strings.TrimPrefix(u, "git@github.com:")
	case strings.HasPrefix(u, "ssh://git@github.com/"):
		path = strings.TrimPrefix(u, "ssh://git@github.com/")
	default:
		return ""
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if validator.RepoSlugRe.MatchString(path) {
		return path
	}
	return ""
}
