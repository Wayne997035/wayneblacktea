package validator

import "regexp"

// RepoNameRe enforces a safe slug format for project repo_name values.
// Previously defined only inside internal/mcp/tools_gtd.go — hoisted here so
// MCP tools, HTTP handlers, and the store layer (PG + SQLite) share a single
// source of truth for the format instead of drifting independently.
//
// Not a security boundary by itself (repo_name is never interpolated into a
// subprocess argv or filesystem path — see RepoSlugRe in repo_regex.go for
// the slug that IS used in `gh -R <slug>`), but keeps the column
// semantically queryable and rejects control characters / path-traversal
// sequences from entering the DB.
var RepoNameRe = regexp.MustCompile(`^[a-zA-Z0-9_.\-]{1,100}$`)

// IsValidRepoName reports whether name is acceptable as a project repo_name
// value. Empty string is valid — repo_name is optional; callers that require
// a non-empty value must check that separately.
func IsValidRepoName(name string) bool {
	if name == "" {
		return true
	}
	return RepoNameRe.MatchString(name)
}
