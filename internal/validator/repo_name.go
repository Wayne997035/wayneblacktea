package validator

import "errors"

// RepoNameRule describes the workspace repo name format in words. It is the
// text every caller-facing rejection message quotes, so a caller that trips
// the rule is told what a valid name looks like.
const RepoNameRule = "1-100 ASCII characters: " + RepoPathSegmentRule

// RepoPathSegmentRule is the segment half of RepoNameRule, without a length.
// Identifiers that share the segment rule under their own length limit
// (ValidRepoPathMax) quote it next to that limit.
const RepoPathSegmentRule = "one or more '/'-separated segments, " +
	"each starting with a letter, digit or '_' and continuing with letters, digits, '.', '_' or '-'"

// RepoPathPattern is ValidRepoPath's rule written as a regular expression, for
// SQL migrations and documentation. ValidRepoPath does not use it: character
// ranges inside a regex are collation-dependent on some engines, while the
// byte checks below are not.
const RepoPathPattern = `^[A-Za-z0-9_][A-Za-z0-9._-]*(/[A-Za-z0-9_][A-Za-z0-9._-]*)*$`

// RepoPathMaxLen is the length limit shared by every repo_name column and
// repos.name. They are compared with `=`, so different limits would leave some
// repos unable to ever pair with a project.
const RepoPathMaxLen = 100

// ErrInvalidRepoName is the single sentinel for a repo name that fails the
// rule. Other packages alias it (gtd.ErrInvalidRepoName) instead of declaring
// their own, so errors.Is matches no matter which layer rejected the value.
var ErrInvalidRepoName = errors.New(RepoNameMessage)

// ErrInvalidSlug is the store-layer sentinel for a project_arch or status
// snapshot slug that fails RepoPathSegmentRule under the store's own length
// limit (ValidRepoPathMax).
var ErrInvalidSlug = errors.New("slug must be " + RepoPathSegmentRule)

// RepoNameMessage is the rejection text for an invalid repo_name, shared by
// the sentinel above and by every HTTP/MCP entry point that rejects the value
// before it reaches a store. It is a constant: no server state is interpolated.
const RepoNameMessage = "repo_name must be " + RepoNameRule

// ValidRepoPath reports whether s is a valid workspace repo name: 1 to
// RepoPathMaxLen bytes, made of one or more '/'-separated segments. Each
// segment starts with an ASCII letter, digit or '_' and continues with ASCII
// letters, digits, '.', '_' or '-'.
//
// The rule accepts both plain directory names ("wayneblacktea") and
// path-shaped names ("Flare-Go/auth"), which is what production stores. It
// rejects "." and ".." segments, hidden-directory segments, segments starting
// with '-' (they read as command-line flags), empty segments, whitespace,
// control characters and non-ASCII bytes.
func ValidRepoPath(s string) bool {
	return ValidRepoPathMax(s, RepoPathMaxLen)
}

// ValidRepoPathMax applies ValidRepoPath's segment rule with a caller-chosen
// length limit, for identifiers that already had their own limit before the
// rule was shared (project_arch.slug: 128, generate_project_status slug: 64).
func ValidRepoPathMax(s string, maxLen int) bool {
	if len(s) == 0 || len(s) > maxLen {
		return false
	}
	segmentStart := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '/' {
			if segmentStart {
				return false // leading slash or empty segment
			}
			segmentStart = true
			continue
		}
		if segmentStart {
			if !isSegmentStart(c) {
				return false
			}
			segmentStart = false
			continue
		}
		if !isSegmentByte(c) {
			return false
		}
	}
	return !segmentStart // a trailing slash leaves an empty last segment
}

func isSegmentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

func isSegmentByte(c byte) bool {
	return isSegmentStart(c) || c == '.' || c == '-'
}

// IsValidRepoName reports whether name is acceptable for an optional
// repo_name column. Empty string is valid — repo_name is optional; callers
// that require a non-empty value must check that separately. Any non-empty
// value must satisfy ValidRepoPath.
//
// Not a security boundary for subprocesses: repo_name is never interpolated
// into an argv or filesystem path (RepoSlugRe in repo_regex.go guards the slug
// that IS used in `gh -R <slug>`). It keeps the columns comparable with `=`
// and keeps control characters, path-traversal segments and prompt boundary
// marker characters ('=', '[', whitespace) out of the DB.
func IsValidRepoName(name string) bool {
	return name == "" || ValidRepoPath(name)
}

// RepoNameOrEmpty returns name when it is a valid repo name and "" otherwise.
// Automatic writers use it: they have no caller to report an error to, and
// dropping the whole row would lose the audit record, so they store the row
// with an empty repo_name instead.
func RepoNameOrEmpty(name string) string {
	if IsValidRepoName(name) {
		return name
	}
	return ""
}
