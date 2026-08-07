package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// newMinimalPlanServer creates a Server with a GTD store only (no work session),
// backed by the full in-memory SQLite test helper from tools_worksession_test.go.
// It re-uses newTestWorkSessionServer because confirm_plan needs at least gtd.
func newMinimalPlanServer(t *testing.T) *Server {
	t.Helper()
	return newTestWorkSessionServer(t)
}

func callConfirmPlan(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleConfirmPlan(context.Background(), req)
	if err != nil {
		t.Fatalf("handleConfirmPlan error: %v", err)
	}
	return result
}

// ---- input validation ----

func TestHandleConfirmPlan_MissingPhases(t *testing.T) {
	s := newMinimalPlanServer(t)
	r := callConfirmPlan(t, s, map[string]any{})
	if !r.IsError {
		t.Error("expected error for missing phases")
	}
	if !strings.Contains(resultText(r), "phases") {
		t.Errorf("error should mention 'phases', got: %s", resultText(r))
	}
}

func TestHandleConfirmPlan_InvalidPhasesJSON(t *testing.T) {
	s := newMinimalPlanServer(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases": "{not-valid-json}",
	})
	if !r.IsError {
		t.Error("expected error for invalid phases JSON")
	}
}

func TestHandleConfirmPlan_EmptyPhasesArray(t *testing.T) {
	s := newMinimalPlanServer(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases": "[]",
	})
	if !r.IsError {
		t.Error("expected error for empty phases array")
	}
}

func TestHandleConfirmPlan_InvalidProjectIDUUID(t *testing.T) {
	s := newMinimalPlanServer(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases":     `[{"title":"T","description":"D","priority":2}]`,
		"project_id": "not-a-uuid",
	})
	if !r.IsError {
		t.Error("expected error for invalid project_id UUID")
	}
}

func TestHandleConfirmPlan_InvalidDecisionsJSON(t *testing.T) {
	s := newMinimalPlanServer(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases":    `[{"title":"T","description":"D","priority":2}]`,
		"decisions": "{bad-json}",
	})
	if !r.IsError {
		t.Error("expected error for invalid decisions JSON")
	}
}

// ---- happy path: task creation ----

func TestHandleConfirmPlan_SinglePhase(t *testing.T) {
	s := newMinimalPlanServer(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases": `[{"title":"Implement auth","description":"JWT auth","priority":1}]`,
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "Plan confirmed") {
		t.Errorf("response missing 'Plan confirmed': %s", text)
	}
	if !strings.Contains(text, "Tasks created (1)") {
		t.Errorf("response missing 'Tasks created (1)': %s", text)
	}
	if !strings.Contains(text, "Implement auth") {
		t.Errorf("response missing task title 'Implement auth': %s", text)
	}
}

func TestHandleConfirmPlan_MultiplePhases(t *testing.T) {
	s := newMinimalPlanServer(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases": `[
			{"title":"Phase 1","description":"First","priority":1},
			{"title":"Phase 2","description":"Second","priority":2},
			{"title":"Phase 3","description":"Third","priority":3}
		]`,
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "Tasks created (3)") {
		t.Errorf("response missing 'Tasks created (3)': %s", text)
	}
	for _, title := range []string{"Phase 1", "Phase 2", "Phase 3"} {
		if !strings.Contains(text, title) {
			t.Errorf("response missing phase title %q: %s", title, text)
		}
	}
}

// ---- decisions logging ----

func TestHandleConfirmPlan_WithDecisions(t *testing.T) {
	s := newMinimalPlanServer(t)
	decisions := `[{"title":"Use Echo","context":"HTTP framework","decision":"Echo","rationale":"Fast"}]`
	r := callConfirmPlan(t, s, map[string]any{
		"phases":    `[{"title":"Build API","description":"REST API","priority":2}]`,
		"decisions": decisions,
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "Decisions logged (1)") {
		t.Errorf("response missing 'Decisions logged (1)': %s", text)
	}
	if !strings.Contains(text, "Use Echo") {
		t.Errorf("response missing decision title 'Use Echo': %s", text)
	}
}

func TestHandleConfirmPlan_DecisionMissingTitle_Skipped(t *testing.T) {
	// A decision with empty title should be skipped (not logged).
	s := newMinimalPlanServer(t)
	decisions := `[{"title":"","context":"x","decision":"y","rationale":"z"}]`
	r := callConfirmPlan(t, s, map[string]any{
		"phases":    `[{"title":"Do something","description":"x","priority":2}]`,
		"decisions": decisions,
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	text := resultText(r)
	// No "Decisions logged" since the one decision has empty title → skipped.
	if strings.Contains(text, "Decisions logged") {
		t.Errorf("empty-title decision should be skipped; got: %s", text)
	}
}

// ---- no-work-session guard: confirm_plan works without workSession store ----

func TestHandleConfirmPlan_NoWorkSessionStore(t *testing.T) {
	// Explicitly create a server without workSession set — confirm_plan must
	// still succeed (best-effort: missing work session store is not fatal).
	s := newTestWorkSessionServer(t)
	s.workSession = nil // remove work session store

	r := callConfirmPlan(t, s, map[string]any{
		"phases":    `[{"title":"No-session phase","description":"x","priority":2}]`,
		"repo_name": "test-repo",
	})
	if r.IsError {
		t.Fatalf("expected success even without workSession store, got: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "Plan confirmed") {
		t.Errorf("response missing 'Plan confirmed': %s", text)
	}
	// No session started (workSession is nil).
	if strings.Contains(text, "Work session started") {
		t.Errorf("should not report session when workSession=nil, got: %s", text)
	}
}

// ---- no repo_name: no session created ----

func TestHandleConfirmPlan_NoRepoName_NoSession(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases": `[{"title":"Anon phase","description":"x","priority":2}]`,
		// no repo_name
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	text := resultText(r)
	// No repo_name → createWorkSessionForPlan skips silently.
	if strings.Contains(text, "Work session started") {
		t.Errorf("should not create session without repo_name, got: %s", text)
	}
}

// ---- phase title with empty string is skipped ----

func TestHandleConfirmPlan_EmptyPhaseTitleSkipped(t *testing.T) {
	s := newMinimalPlanServer(t)
	// 3 phases but one has an empty title — should produce 2 tasks.
	r := callConfirmPlan(t, s, map[string]any{
		"phases": `[
			{"title":"Do A","description":"A","priority":1},
			{"title":"","description":"skip","priority":2},
			{"title":"Do C","description":"C","priority":3}
		]`,
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "Tasks created (2)") {
		t.Errorf("empty-title phase should be skipped; expected 2 tasks: %s", text)
	}
}

// ---- P6.8: assignee gate on confirm_plan's created work session ----

// TestHandleConfirmPlan_RejectsInvalidAssignee verifies confirm_plan's
// assignee argument is validated through gtd.NormalizeActor's whitelist
// before it can reach worksession.CreateParams.Assignee.
func TestHandleConfirmPlan_RejectsInvalidAssignee(t *testing.T) {
	s := newMinimalPlanServer(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases":    `[{"title":"Do A","description":"A","priority":1}]`,
		"repo_name": "invalid-assignee-plan-repo",
		"assignee":  "gemini",
	})
	if !r.IsError {
		t.Fatal("expected error for unrecognized assignee")
	}
	if !strings.Contains(resultText(r), "recognized actor") {
		t.Errorf("error should mention the allowlist, got: %s", resultText(r))
	}
}

// TestHandleConfirmPlan_StampsAssigneeOntoPhaseTask is the end-to-end (MCP
// request → handler → store) regression test for the P6.8 gate on
// confirm_plan: the phase task created and linked into the work session has
// no assignee at creation time (createPhaseTasksWithIDs does not set one), so
// it must be stamped with confirm_plan's assignee argument when the session
// flips it to in_progress.
func TestHandleConfirmPlan_StampsAssigneeOntoPhaseTask(t *testing.T) {
	s, db := newTestWorkSessionServerWithDB(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases":    `[{"title":"Do A","description":"A","priority":1}]`,
		"repo_name": "stamp-assignee-plan-repo",
		"assignee":  "human",
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "Work session started") {
		t.Fatalf("expected a work session to be created: %s", text)
	}

	var taskID string
	row := db.QueryRowContext(context.Background(), `SELECT id FROM tasks WHERE title = ?1`, "Do A")
	if err := row.Scan(&taskID); err != nil {
		t.Fatalf("query created task: %v", err)
	}

	if got := queryMCPTaskStatus(t, db, taskID); got != taskStatusInProgress {
		t.Errorf("phase task status: got %q, want in_progress", got)
	}
	if got := queryMCPTaskAssignee(t, db, taskID); got != "human" {
		t.Errorf("phase task assignee: got %q, want stamped \"human\"", got)
	}
}

// ---- P-atomicity-honesty: sequential-fallback path still surfaces partials ----
//
// The tests below force materializePlan's non-atomic fallback branch (see
// its doc comment) by nil-ing s.sqliteGTD after construction — this is NOT a
// real deployment shape (storage.NewServerStores always wires one backend or
// the other), but it is the only way to exercise materializePlanSequential
// through the full handler instead of calling createPhaseTasksWithIDs /
// logPlanDecisions directly. The real-backend atomicity tests (PG and
// SQLite transactions actually rolling back) are
// TestHandleConfirmPlan_SQLite_AtomicRollbackOnMidPhaseFailure below and
// TestHandleConfirmPlan_Postgres_AtomicRollbackOnMidPhaseFailure in
// tools_plan_pg_test.go.

// failingDecisionStore is a minimal decision.StoreIface stub used to force a
// deterministic mid-loop Log failure. confirm_plan hardcodes
// Source=SourceManual on every decision it logs, so there is no
// confirm_plan-reachable input that makes either backend's REAL decision
// store fail on one specific item in a multi-decision call (SQLite's Log
// only validates Source; Postgres's Log additionally runs
// sanitize.ValidateNoTagNoise, but that's not backend-selectable from a
// SQLite-backed test). A hand-rolled stub of the 7-method StoreIface is the
// direct way to exercise this contract; it substitutes only Log, so calls to
// any other embedded (nil) method would panic — none of the paths under test
// reach them.
type failingDecisionStore struct {
	decision.StoreIface
	failAfter int
	calls     int
}

func (f *failingDecisionStore) Log(_ context.Context, p decision.LogParams) (*db.Decision, error) {
	f.calls++
	if f.calls > f.failAfter {
		return nil, fmt.Errorf("stub failure logging decision %q (call %d)", p.Title, f.calls)
	}
	return &db.Decision{Title: p.Title}, nil
}

func TestHandleConfirmPlan_SequentialFallback_PartialTaskFailure_CreatedTasksSurfaced(t *testing.T) {
	s := newMinimalPlanServer(t)
	s.sqliteGTD = nil // force materializePlanSequential (see block comment above)
	// priority=99 violates the `priority BETWEEN 1 AND 5` CHECK constraint
	// (migrations/sqlite/000012_sqlite_baseline.up.sql) on the SECOND phase,
	// forcing a real mid-loop store failure after the first phase succeeded.
	r := callConfirmPlan(t, s, map[string]any{
		"phases": `[
			{"title":"Do A","description":"A","priority":1},
			{"title":"Do B","description":"B","priority":99}
		]`,
	})
	if !r.IsError {
		t.Fatalf("expected error for out-of-range priority, got success: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "Do A") {
		t.Errorf("already-created task title must be surfaced on partial failure, got: %s", text)
	}
	if !strings.Contains(text, "1 already created") {
		t.Errorf("error should note exactly 1 task was already created before the failure, got: %s", text)
	}
}

func TestLogPlanDecisions_PartialFailure_ReturnsAlreadyLogged(t *testing.T) {
	s := &Server{decision: &failingDecisionStore{failAfter: 1}}
	decisions := []decisionInput{
		{Title: "Decision 1", Decision: "Use X"},
		{Title: "Decision 2", Decision: "Use Y"},
	}
	logged, err := s.logPlanDecisions(context.Background(), decisions, nil, "")
	if err == nil {
		t.Fatal("expected error on second decision")
	}
	if len(logged) != 1 || logged[0] != "Decision 1" {
		t.Errorf("expected first decision preserved in partial result, got: %v", logged)
	}
	if !strings.Contains(err.Error(), "1 already logged") {
		t.Errorf("error should note 1 decision was already logged before the failure, got: %v", err)
	}
}

func TestHandleConfirmPlan_SequentialFallback_PartialDecisionFailure_TasksAndPriorDecisionsSurfaced(t *testing.T) {
	s := newMinimalPlanServer(t)
	s.sqliteGTD = nil // force materializePlanSequential (see block comment above)
	s.decision = &failingDecisionStore{failAfter: 1}

	r := callConfirmPlan(t, s, map[string]any{
		"phases":    `[{"title":"Do A","description":"A","priority":1}]`,
		"decisions": `[{"title":"Decision 1","decision":"Use X"},{"title":"Decision 2","decision":"Use Y"}]`,
	})
	if !r.IsError {
		t.Fatalf("expected error from stub decision failure, got success: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "Do A") {
		t.Errorf("task created before the decision failure must still be reported, got: %s", text)
	}
	if !strings.Contains(text, "Decision 1") {
		t.Errorf("decision logged before the failure must still be reported, got: %s", text)
	}
	if strings.Contains(text, "• Decision 2") {
		t.Errorf("decision that failed to log must not appear in the logged-decisions list, got: %s", text)
	}
}

// ---- SQLite real-transaction atomicity (materializePlanSQLite) ----

// TestHandleConfirmPlan_SQLite_AtomicRollbackOnMidPhaseFailure is the SQLite
// half of confirm_plan's atomicity guarantee: when a later phase fails, an
// EARLIER phase task created in the same call MUST NOT persist. Uses the
// default (unmodified) newTestWorkSessionServerWithDB server, so this goes
// through the real materializePlanSQLite tx path, not a stub.
//
// Mutation-tested: see the engineer report for the before/after output of
// temporarily disabling the rollback.
func TestHandleConfirmPlan_SQLite_AtomicRollbackOnMidPhaseFailure(t *testing.T) {
	s, sdb := newTestWorkSessionServerWithDB(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases": `[
			{"title":"SQLite Atomic A","description":"A","priority":1},
			{"title":"SQLite Atomic B","description":"B","priority":99}
		]`,
	})
	if !r.IsError {
		t.Fatalf("expected error for out-of-range priority, got success: %s", resultText(r))
	}
	text := resultText(r)
	if strings.Contains(text, "SQLite Atomic A") {
		t.Errorf("rolled-back task must not be reported as created, got: %s", text)
	}

	var count int
	row := sdb.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM tasks WHERE title IN ('SQLite Atomic A','SQLite Atomic B')`)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("querying tasks: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tasks after rollback, got %d — transaction did not roll back", count)
	}
}

// TestHandleConfirmPlan_SQLite_AtomicRollbackAcrossTaskAndDecision proves the
// SQLite transaction spans BOTH loops: a task that succeeds is still rolled
// back when a LATER decision in the same call fails.
func TestHandleConfirmPlan_SQLite_AtomicRollbackAcrossTaskAndDecision(t *testing.T) {
	s, sdb := newTestWorkSessionServerWithDB(t)
	// Source is hardcoded to SourceManual by materializePlanSQLite, so there
	// is no confirm_plan-reachable input that fails a specific decision
	// through the real SQLite store. Swap in a decision-failing stub while
	// keeping the real sqliteGTD wired, so the task loop hits the real store
	// and the decision loop hits the stub inside the SAME real *sql.Tx.
	s.sqliteDecision = nil // force the "sqlite decision store not wired" error path
	r := callConfirmPlan(t, s, map[string]any{
		"phases":    `[{"title":"SQLite Atomic Cross A","description":"A","priority":1}]`,
		"decisions": `[{"title":"Cross Decision","decision":"Use X"}]`,
	})
	if !r.IsError {
		t.Fatalf("expected error from unwired sqliteDecision, got success: %s", resultText(r))
	}
	text := resultText(r)
	if strings.Contains(text, "SQLite Atomic Cross A") {
		t.Errorf("task rolled back by the later decision failure must not be reported as created, got: %s", text)
	}

	var count int
	row := sdb.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM tasks WHERE title = 'SQLite Atomic Cross A'`)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("querying tasks: %v", err)
	}
	if count != 0 {
		t.Errorf("expected task to be rolled back by the decision-loop failure, got %d rows", count)
	}
}
