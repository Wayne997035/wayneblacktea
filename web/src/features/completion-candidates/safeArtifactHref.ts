// URL-scheme allowlist guard for `suggested_artifact` values.
//
// `suggested_artifact` may be a GitHub PR URL ("https://github.com/...") or a
// wayneblacktea-app deep link ("wbt://tasks/<uuid>"). Without an allowlist a
// malicious value like `javascript:alert(1)` would survive into the rendered
// href and create an XSS surface. The allowlist permits only http / https /
// wbt; anything else collapses to "#" so the link is rendered but inert.
//
// The raw payload is still surfaced as link text — this keeps the suspicious
// value visible to the user while neutralising the href.
const ARTIFACT_SCHEME_ALLOWLIST = /^(https?|wbt):\/\//

export function safeArtifactHref(raw: string | undefined): string {
  if (!raw) return '#'
  return ARTIFACT_SCHEME_ALLOWLIST.test(raw) ? raw : '#'
}
