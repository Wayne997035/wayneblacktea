package gtd

import (
	"regexp"
	"strings"
)

// slugRe matches any character sequence that is not a lowercase ASCII letter or digit.
var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// TitleToBranchSlug converts a task title into a git branch name slug of the
// form "feature/<slug>" or "fix/<slug>" (the latter when the title starts with
// "fix"). Slug characters are restricted to [a-z0-9-] and capped at 60
// characters so the full branch name stays within Git's 255-byte ref limit.
//
// This is the single canonical implementation shared by handler and mcp
// packages — previously each package held its own copy (taskTitleToBranchSlug /
// mcpTaskTitleToBranchSlug). Keep in sync with any future slug format changes.
func TitleToBranchSlug(title string) string {
	lower := strings.ToLower(title)
	slug := slugRe.ReplaceAllString(lower, "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 60 {
		slug = strings.TrimRight(slug[:60], "-")
	}
	if strings.HasPrefix(lower, "fix") {
		return "fix/" + slug
	}
	return "feature/" + slug
}
