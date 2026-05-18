package validator

import "regexp"

// GitHubPRURLRe matches GitHub PR URLs of the form
// https://github.com/{owner}/{repo}/pull/{number}[/]
// SECURITY: only used for format validation — no HTTP fetch is ever made.
// Single canonical definition shared by handler and mcp packages to avoid drift.
var GitHubPRURLRe = regexp.MustCompile(`^https://github\.com/[^/]+/[^/]+/pull/\d+(/)?$`)
