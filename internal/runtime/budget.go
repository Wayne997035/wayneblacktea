package runtime

import "time"

// FinishReason is the reason a Runtime run stopped.
type FinishReason string

const (
	// FinishReasonMaxTurns means the turn count exceeded Budget.MaxTurns.
	FinishReasonMaxTurns FinishReason = "max_turns_exceeded"
	// FinishReasonMaxDuration means the wall-clock duration exceeded Budget.MaxDuration.
	FinishReasonMaxDuration FinishReason = "max_duration_exceeded"
	// FinishReasonMaxCost means the cost exceeded Budget.MaxCostMicroUSD.
	FinishReasonMaxCost FinishReason = "max_cost_exceeded"
)

// Interactive defaults. There is deliberately no cost default: the spec pack
// only says "configurable cost ceiling" with no number.
const (
	DefaultInteractiveMaxTurns     = 12
	DefaultInteractiveMaxToolCalls = 20
	DefaultInteractiveMaxDuration  = 120 * time.Second
)

// Budget is the set of per-run limits. A limit <= 0 means "unset": that
// dimension is not checked. Callers that forget to fill a limit therefore get
// no enforcement for it, so the wiring layer must populate defaults.
type Budget struct {
	MaxTurns int
	// MaxToolCalls is configuration only. [F1123-31] CheckExceeded does NOT
	// evaluate it: Elapsed has no tool-call counter until a later slice.
	MaxToolCalls    int
	MaxDuration     time.Duration
	MaxCostMicroUSD int64
}

// Elapsed holds the running counters compared against a Budget.
type Elapsed struct {
	Turns        int
	Duration     time.Duration
	CostMicroUSD int64
}

// CheckExceeded reports whether any budget dimension is exceeded and why.
//
// [F1123-31] When several dimensions are exceeded at once, the evaluation
// order is duration, then cost, then turns (a slow/hung call is the most
// urgent stop signal). Do not reorder. Comparison is strictly greater-than:
// a value exactly equal to its limit is not exceeded.
//
// [F1123-32] A limit <= 0 disables that dimension's check (0 never means
// "any usage exceeds"). Within limits it returns (false, "").
func (b Budget) CheckExceeded(e Elapsed) (exceeded bool, reason FinishReason) {
	if b.MaxDuration > 0 && e.Duration > b.MaxDuration {
		return true, FinishReasonMaxDuration
	}
	if b.MaxCostMicroUSD > 0 && e.CostMicroUSD > b.MaxCostMicroUSD {
		return true, FinishReasonMaxCost
	}
	if b.MaxTurns > 0 && e.Turns > b.MaxTurns {
		return true, FinishReasonMaxTurns
	}
	return false, ""
}
