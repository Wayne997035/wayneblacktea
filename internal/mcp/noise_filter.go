package mcp

import "github.com/Wayne997035/wayneblacktea/internal/validator"

// maxTagCount mirrors validator.MaxTagCount for the one remaining
// package-level reference in this package (tools_knowledge.go's tag-count
// check).
const maxTagCount = validator.MaxTagCount

// noiseReason is a human-readable explanation returned with -32602 rejections.
type noiseReason = string

// checkField, checkDecisionNoise, checkCommandField, checkHandoffNoise, and
// sanitizeTags are thin package-local wrappers around internal/validator so
// the existing call sites in this package (tools_worksession.go,
// tools_session.go, tools_learning.go, tools_knowledge.go, tools_decision.go)
// don't need to change their imports or call signatures. The actual
// noise/injection-pattern logic (script-tag / markdown-fence detection, field
// length caps, control-char rejection) lives in internal/validator/noise.go —
// this file is not a second implementation, so both the MCP and HTTP entry
// points evaluate the exact same rules.
func checkField(name, value string) noiseReason {
	return validator.CheckField(name, value)
}

func checkDecisionNoise(title, ctx, decision, rationale string) noiseReason {
	return validator.CheckDecisionNoise(title, ctx, decision, rationale)
}

func checkCommandField(name, value string) noiseReason {
	return validator.CheckCommandField(name, value)
}

func checkHandoffNoise(intent, contextSummary string) noiseReason {
	return validator.CheckHandoffNoise(intent, contextSummary)
}

func sanitizeTags(raw []string) ([]string, noiseReason) {
	return validator.SanitizeTags(raw)
}
