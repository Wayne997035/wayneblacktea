package validator

import "regexp"

// RepoSlugRe validates owner/repo style GitHub slugs at the reconcile boundary
// where the slug flows into `gh -R <slug>` CLI invocation. Rejects whitespace,
// shell metas, path traversal, control chars.
//
// SECURITY: must match before passing the slug to a subprocess command.
// GitHub itself permits ASCII letters, digits, hyphens, underscores, and dots
// in both owner and repo segments; rejecting anything else is conservative
// but eliminates shell-injection / path-traversal / newline-smuggling vectors.
var RepoSlugRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
