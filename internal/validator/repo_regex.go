package validator

import (
	"errors"
	"regexp"
)

// RepoSlugRe validates owner/repo style GitHub slugs at the reconcile boundary
// where the slug flows into `gh -R <slug>` CLI invocation. Rejects whitespace,
// shell metas, path traversal, control chars.
//
// SECURITY: must match before passing the slug to a subprocess command.
// GitHub itself permits ASCII letters, digits, hyphens, underscores, and dots
// in both owner and repo segments; rejecting anything else is conservative
// but eliminates shell-injection / path-traversal / newline-smuggling vectors.
var RepoSlugRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// ErrInvalidGitHubSlug is the sentinel for a repos.github_slug that is not a
// valid owner/repo slug ([F0925-31]).
var ErrInvalidGitHubSlug = errors.New("github_slug must be an owner/repo GitHub slug: " +
	"exactly two segments of letters, digits, '.', '_' or '-', each starting with a letter, digit or '_'")

// ValidGitHubSlug reports whether s may be stored as repos.github_slug and
// later passed to `gh -R`: RepoSlugRe (exactly one '/') plus the workspace
// repo name segment rule, so "../x" and "-x/y" — which RepoSlugRe alone
// admits — are rejected. Cost: a repo whose name starts with '.' (e.g.
// "owner/.github") cannot be reconciled.
func ValidGitHubSlug(s string) bool {
	return RepoSlugRe.MatchString(s) && ValidRepoPath(s)
}
