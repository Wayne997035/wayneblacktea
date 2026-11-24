package guard

import "github.com/Wayne997035/wayneblacktea/internal/risk"

// [F1124-20] The T0-T7 classification logic lives in internal/risk; this file
// only keeps guard's existing public names so matcher, store and cmd/wbt-guard
// compile unchanged. RiskTier is an alias (=), not a new type, so values flow
// between the two packages without conversion.

// RiskTier classifies how risky a tool invocation is.
// Higher value = higher risk.  T7 is a catch-all for unrecognised commands.
type RiskTier = risk.Tier

const (
	T0 = risk.T0 // read-only commands
	T1 = risk.T1 // build / test
	T2 = risk.T2 // local run
	T3 = risk.T3 // safe git mutation
	T4 = risk.T4 // risky git mutation / sub-agent
	T5 = risk.T5 // destructive local
	T6 = risk.T6 // destructive remote
	T7 = risk.T7 // unknown / cannot parse
)

// ClassifyBash delegates to risk.ClassifyBash and returns the highest risk
// tier found across the whole pipeline together with a human-readable reason.
func ClassifyBash(command string) (RiskTier, string) {
	return risk.ClassifyBash(command)
}
