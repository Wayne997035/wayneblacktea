// URL-scheme allowlist guard for `suggested_artifact` values.
//
// `suggested_artifact` may be a GitHub PR URL ("https://github.com/...") or a
// wayneblacktea-app deep link ("wbt://tasks/<uuid>"). Without an allowlist a
// malicious value like `javascript:alert(1)` would survive into the rendered
// href and create an XSS surface. The allowlist permits only http / https /
// wbt (case-insensitive); anything else collapses to "#" so the link is
// rendered but inert.
//
// Round-2 hardening (PR #126 reviewer + security-engineer MAJOR/MEDIUM):
//   1. /i flag — case-sensitive regex previously accepted only lowercase
//      schemes; `JaVaScRiPt:` would bypass when added to the allowlist but
//      the existing allowlist would also have rejected `HTTPS://` legit URLs.
//      Adding /i fixes both directions consistently.
//   2. Control-character rejection — strings containing NUL / CR / LF / TAB
//      may be parsed differently across browsers / OS layers. We reject
//      outright rather than try to strip; a legit https URL has none.
//   3. Leading-whitespace rejection — `\thttps://...` could fool a naive
//      `startsWith('http')` check elsewhere; reject any leading whitespace.
//
// The raw payload is still surfaced as link text — this keeps the suspicious
// value visible to the user while neutralising the href.
const ARTIFACT_SCHEME_ALLOWLIST = /^(https?|wbt):\/\//i

// Reject control chars anywhere in the string (NUL / CR / LF / TAB / other
// C0 control codes). A legitimate URL contains none of these.
// eslint-disable-next-line no-control-regex
const CONTROL_CHARS = /[\x00-\x1f\x7f]/

export function safeArtifactHref(raw: string | undefined): string {
  if (!raw) return '#'
  if (CONTROL_CHARS.test(raw)) return '#'
  // Reject any leading whitespace before scheme check (a regex anchored at
  // `^` with `/i` flag still respects whitespace literally, but we exclude
  // it explicitly so the contract is obvious to future readers).
  if (raw !== raw.trimStart()) return '#'
  return ARTIFACT_SCHEME_ALLOWLIST.test(raw) ? raw : '#'
}
