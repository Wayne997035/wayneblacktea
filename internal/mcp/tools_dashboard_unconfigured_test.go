//go:build !integration

// Tagged to match tools_behaviorrule_test.go, which owns the
// callProposeBehaviorRule / newBehaviorRuleServer helpers borrowed below for
// the cross-tool convention check. Without the tag this file compiles under
// `-tags integration` while those helpers do not exist — and `go test` with no
// tags stays green the whole time, so only the full gate would find it.

package mcp

import (
	"context"
	"strings"
	"testing"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// [GTD 4d5b3354 #3] Neither of these paths had any test. That is how the shape
// stayed wrong: "store not wired" was answered with IsError:false and an empty
// candidates array, which an agent reads as "there are no candidates" — a
// different answer from the true one, reached without any error to notice.
//
// The assertions are on IsError and on the absence of an empty result set, not
// on the message text: the message is allowed to be reworded, the two facts are
// not.

func callDashboardTool(
	t *testing.T,
	h func(context.Context, mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error),
	args map[string]any,
) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned a transport error: %v", err)
	}
	return r
}

func TestDetectCompletionCandidates_UnconfiguredStoreIsAnError(t *testing.T) {
	t.Parallel()
	s := &Server{} // completionCandidates left nil: the domain is not wired

	res := callDashboardTool(t, s.handleDetectCompletionCandidates, nil)

	if !res.IsError {
		t.Fatal("unwired store answered IsError=false — an agent cannot tell this " +
			"apart from a successful scan that found nothing")
	}
	if body := resultText(res); strings.Contains(body, `"candidates"`) {
		t.Errorf("response still carries a candidates field: %q. An empty array here "+
			"is a claim about the workspace that was never checked", body)
	}
}

func TestReconcileDashboard_UnconfiguredStoreIsAnError(t *testing.T) {
	t.Parallel()
	s := &Server{}

	res := callDashboardTool(t, s.handleReconcileDashboard, nil)

	if !res.IsError {
		t.Fatal("unwired store answered IsError=false — reconcile_dashboard's caller " +
			"reads that as a completed automation-health snapshot")
	}
}

// TestDashboardUnconfigured_MatchesTheSurfaceConvention pins the reason the
// previous shape was wrong rather than merely different: every other optional
// dependency on this surface reports "not wired" as an error. If that ever
// stops being true the convention has moved, and these two should move with it
// — deliberately, not by being the last ones left behind.
func TestDashboardUnconfigured_MatchesTheSurfaceConvention(t *testing.T) {
	t.Parallel()
	s := &Server{}

	for name, res := range map[string]*mcpmsg.CallToolResult{
		"detect_completion_candidates": callDashboardTool(t, s.handleDetectCompletionCandidates, nil),
		"reconcile_dashboard":          callDashboardTool(t, s.handleReconcileDashboard, nil),
		"propose_behavior_rule":        callProposeBehaviorRule(t, newBehaviorRuleServer(nil), nil),
	} {
		if !res.IsError {
			t.Errorf("%s reports an unwired optional dependency as success", name)
		}
	}
}
