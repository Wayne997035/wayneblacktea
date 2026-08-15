package storage

import "testing"

// acceptedRedeployWorstCaseConns is the currently-ACCEPTED worst-case
// connection demand during a Railway redeploy overlap window
// (railway.toml healthcheckTimeout=300s, old instance torn down only after
// the new one passes health checks): two server instances at
// ServerPoolMaxConns each, plus one concurrent CLI/hook process at
// HookPoolMaxConns. It intentionally EXCEEDS AivenAvailableConns (18 > 15)
// -- see the doc comment above ServerPoolMaxConns in factory.go for why
// that gap is a known, accepted risk (occasional SQLSTATE 53300 during the
// overlap window) and not something this test is meant to fix. Accepted
// 2026-08-09 by user; do not silently raise this constant to make a bump
// pass -- re-measure Aiven's budget and update AivenAvailableConns too.
//
// This constant is the guard: a future bump to ServerPoolMaxConns or
// HookPoolMaxConns that pushes worst-case ABOVE what's already accepted
// fails TestRedeployWorstCaseConns_DoesNotSilentlyWorsen, instead of
// depending on someone re-deriving the arithmetic by hand.
const acceptedRedeployWorstCaseConns = 18

func TestRedeployWorstCaseConns_DoesNotSilentlyWorsen(t *testing.T) {
	worstCase := 2*ServerPoolMaxConns + HookPoolMaxConns
	if worstCase > acceptedRedeployWorstCaseConns {
		t.Fatalf("redeploy worst-case connection demand is now %d (2*ServerPoolMaxConns(%d) + "+
			"HookPoolMaxConns(%d)), exceeding the already-accepted worst case of %d against a "+
			"budget of only %d available on Aiven -- re-measure Aiven's max_connections, then "+
			"update AivenAvailableConns / acceptedRedeployWorstCaseConns and factory.go's doc "+
			"comment together, or lower one of the pool sizes before merging",
			worstCase, ServerPoolMaxConns, HookPoolMaxConns, acceptedRedeployWorstCaseConns, AivenAvailableConns)
	}
	// Make the known, accepted gap between what's used (18) and what's
	// actually available (15) machine-visible, not just prose in a comment:
	// this is the "3-connection deficit accepted 2026-08-09" fact, computed
	// from the same named constants rather than restated as a bare number.
	gap := acceptedRedeployWorstCaseConns - AivenAvailableConns
	if gap != 3 {
		t.Errorf("accepted redeploy overshoot (acceptedRedeployWorstCaseConns(%d) - "+
			"AivenAvailableConns(%d)) = %d, want 3 -- the 2026-08-09 accepted-risk gap has "+
			"changed; if intentional, update this expectation and the accompanying doc comments "+
			"together so the known gap stays documented, not silently different",
			acceptedRedeployWorstCaseConns, AivenAvailableConns, gap)
	}
	t.Logf("redeploy worst-case=%d vs AivenAvailableConns=%d (gap=%d, accepted 2026-08-09, see "+
		"factory.go ServerPoolMaxConns doc comment)", worstCase, AivenAvailableConns, gap)
}

// TestAivenAvailableConns_MatchesMeasuredBudget pins the raw measured facts
// so an edit to any one of them (without updating the others, or without
// re-measuring the live instance) is caught here instead of silently
// drifting from factory.go's comment.
func TestAivenAvailableConns_MatchesMeasuredBudget(t *testing.T) {
	if AivenAvailableConns != 15 {
		t.Errorf("AivenAvailableConns = %d, want 15 (20 max_connections - 3 superuser_reserved - "+
			"2 Aiven client backends) -- if this changed on purpose, re-measure against the live "+
			"instance and update this test's expected value too", AivenAvailableConns)
	}
}
