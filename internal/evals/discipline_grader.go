package evals

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/discipline"
)

// DisciplineChecker reports whether an MCP tool name requires a preceding
// verification (log_decision / confirm_plan) before its call is considered
// non-drifting. Graders depend on this interface rather than a concrete
// type so a test can swap in a fake without touching the live allowlists.
type DisciplineChecker interface {
	RequiresVerification(toolName string) bool
}

// stubDisciplineChecker satisfies DisciplineChecker by reading
// discipline.MutatingTools directly. It MUST NOT copy that map into a local
// constant: doing so would let a tool silently removed from the live
// allowlist keep passing this grader instead of failing it (dispatch threat
// surface: "contributor removes a mutating tool from MutatingTools ->
// verification gate silently bypassed").
type stubDisciplineChecker struct{}

func (stubDisciplineChecker) RequiresVerification(toolName string) bool {
	return discipline.MutatingTools[toolName]
}

// destructiveNamePatterns are tool-name substrings that always carry
// elevated risk regardless of prior decisions, per
// backend-security-design.md §2.3's pattern-based heuristic for
// mcp__*-style tool names (delete_*, revoke_*, force_*, drop_*, _destroy ->
// highest tier). A tool matching one of these must be freshly re-verified
// on every call; an earlier decision never "covers" it for future calls.
var destructiveNamePatterns = []string{"force", "delete", "drop", "revoke", "destroy"}

// isDestructiveName reports whether toolName matches one of
// destructiveNamePatterns (case-insensitive).
func isDestructiveName(toolName string) bool {
	lower := strings.ToLower(toolName)
	for _, pattern := range destructiveNamePatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// CloseoutSnapshot is a point-in-time view of session-end state, mirroring
// the fields internal/mcp/tools_closeout.go's closeoutReport aggregates
// (OpenTaskCount, PendingProposals, HandoffSet) without importing the mcp
// package — evals stays dependency-free of the server it evaluates.
type CloseoutSnapshot struct {
	OpenTaskCount    int
	PendingProposals int
	HandoffSet       bool
}

// CloseoutChecker reports whether a closeout snapshot represents a
// "nothing left open" state.
type CloseoutChecker interface {
	IsComplete(CloseoutSnapshot) bool
}

// stubCloseoutChecker implements the closeout completeness rule: any open
// task, any pending proposal, or a missing handoff means the session is not
// complete. All three MUST be clear for IsComplete to return true.
type stubCloseoutChecker struct{}

func (stubCloseoutChecker) IsComplete(s CloseoutSnapshot) bool {
	if s.OpenTaskCount > 0 {
		return false
	}
	if s.PendingProposals > 0 {
		return false
	}
	return s.HandoffSet
}

// disciplineFixtureCase mirrors testdata/discipline_cases.json's schema.
// Declared here (not reused from evals_test.go's disciplineCaseFixture)
// because non-test files cannot reference types declared in _test.go files
// — go build ./internal/evals/... would fail if this file depended on one.
type disciplineFixtureCase struct {
	ID               string `json:"id"`
	ToolName         string `json:"tool_name"`
	HasPriorDecision bool   `json:"has_prior_decision"`
	ExpectDrift      bool   `json:"expect_drift"`
}

// computeDrift determines whether a single tool call counts as a discipline
// drift signal, using an invented deterministic model built for grading
// purposes: a destructive-pattern tool name always drifts (it must be
// freshly re-verified every call, per isDestructiveName's doc comment);
// every other mutating tool drifts only when no prior decision covers this
// call.
//
// This is NOT wired to and does NOT validate the real drift detector,
// internal/mcp.EvaluateDisciplineDrift (tools_health.go). The production
// function computes drift from a 15-minute decision-window check
// (hasDecisionInWindow comparing each mutating event's timestamp against
// RecentDecisionTimes for its session) and has no isDestructiveName-style
// per-tool-name override at all. Only stubDisciplineChecker's read of the
// live discipline.MutatingTools map below is genuinely shared with
// production; the has_prior_decision boolean and destructive-name
// escalation here are this grader's own invented approximation of "drift",
// not a mirror of the real time-window rule.
func computeDrift(checker DisciplineChecker, toolName string, hasPriorDecision bool) bool {
	if isDestructiveName(toolName) {
		return true
	}
	return checker.RequiresVerification(toolName) && !hasPriorDecision
}

// disciplineDriftGrader evaluates one discipline_cases.json fixture case:
// whether computeDrift's output matches the fixture's expected drift
// signal for that (tool_name, has_prior_decision) pair.
type disciplineDriftGrader struct {
	caseID           string
	toolName         string
	hasPriorDecision bool
	expectDrift      bool
	checker          DisciplineChecker
}

// Grade implements the Grader contract (internal/evals/evals.go).
func (g disciplineDriftGrader) Grade(_ context.Context) EvalResult {
	drift := computeDrift(g.checker, g.toolName, g.hasPriorDecision)
	if drift == g.expectDrift {
		return EvalResult{CaseID: g.caseID, Passed: true}
	}
	return EvalResult{
		CaseID: g.caseID,
		Passed: false,
		Reason: fmt.Sprintf("tool %q (has_prior_decision=%v): drift = %v, want %v",
			g.toolName, g.hasPriorDecision, drift, g.expectDrift),
	}
}
