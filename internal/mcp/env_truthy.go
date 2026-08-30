package mcp

import "strings"

// truthyEnvOptOut reports whether raw is one of the four values this package
// accepts as an explicit opt-OUT: "1", "true", "yes", "on" — case-insensitive,
// surrounding whitespace ignored.
//
// An empty or unrecognised value is NOT truthy. Both callers default their
// feature to ON, and an earlier inverted-default version of
// decisionProposerEnabled disabled the proposer for any non-empty value
// outside {0,false,no,off}, so an unrelated setting like "auto" or "default"
// silently turned the feature off and surprised operators. Opt-out is explicit
// or it does not happen.
//
// Why this is a function and not two switches: decisionProposerEnabled
// (middleware_decision_proposer.go) and progressiveDisclosureEnabled
// (tools_expand.go) each spelled the same four-value switch out in full, which
// put the package over goconst's occurrence threshold for "true". Both sites
// answered that with a //nolint:goconst plus a comment promising this
// extraction "as a follow-up, not in a lint-only round".
//
// Suppression turned out to be the unstable half of that trade. goconst names
// ONE of the occurrence sites; which one it names depends on the set of files
// being analysed; and nolintlint then fails the build for the directive left
// sitting on the other site. That is not hypothetical — it is why `task lint`
// fails on a clean master today, with the directive on
// middleware_decision_proposer.go and the report on tools_expand.go, after the
// pair had already been re-aligned once. Removing the duplication removes the
// report at every site under every file set, and needs no directive to
// maintain.
func truthyEnvOptOut(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
