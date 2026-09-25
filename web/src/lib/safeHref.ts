// URL-scheme allowlist guard for hrefs built from backend/user data.
//
// Any href rendered straight from data (a knowledge item's URL, a task's
// artifact link, a suggested-artifact link, ...) creates an XSS surface if
// the value uses a scheme like `javascript:` or `data:`. This guard allows
// only http / https / wbt (case-insensitive); anything else collapses to
// "#" so a link element can still be rendered but stays inert.
//
// Hardening rules:
//   1. /i flag — case-insensitive matching so `HTTPS://` and `JaVaScRiPt:`
//      are both handled consistently (accept the former, reject the
//      latter) regardless of casing.
//   2. Control-character rejection — strings containing NUL / CR / LF / TAB
//      may be parsed differently across browsers / OS layers. We reject
//      outright rather than try to strip; a legit https URL has none.
//   3. Leading-whitespace rejection — `\thttps://...` could fool a naive
//      `startsWith('http')` check elsewhere; reject any leading whitespace.
//
// Callers are expected to keep surfacing the raw payload as visible text
// (or an inert placeholder) when this returns "#" — this keeps the
// suspicious value visible to the user while neutralising the href itself.
const SCHEME_ALLOWLIST = /^(https?|wbt):\/\//i

// Reject control chars anywhere in the string (NUL / CR / LF / TAB / other
// C0 control codes). A legitimate URL contains none of these.
// eslint-disable-next-line no-control-regex
const CONTROL_CHARS = /[\x00-\x1f\x7f]/

export function safeHref(raw: string | undefined): string {
  if (!raw) return '#'
  if (CONTROL_CHARS.test(raw)) return '#'
  // Reject any leading whitespace before scheme check (a regex anchored at
  // `^` with `/i` flag still respects whitespace literally, but we exclude
  // it explicitly so the contract is obvious to future readers).
  if (raw !== raw.trimStart()) return '#'
  return SCHEME_ALLOWLIST.test(raw) ? raw : '#'
}
