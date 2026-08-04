package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// Outcome lifecycle convergence — cross-path combinations (arch-r2 A13,
// decision 80c1e8ae). All tests here run against a REAL SQLite-backed
// server (newTestWorkSessionServerWithDB), not stubs, so they exercise the
// full outcome.RecordExecutionResult / SeedDraft / FinalizeDraft chain
// end-to-end through the actual MCP handlers, covering the pairwise
// combinations of the three outcome-creation call sites: complete_task
// (seedDraftOutcome), record_outcome (handleRecordOutcome), and finish_work
// on failure (autoCreateOutcomeOnFailure).
// ---------------------------------------------------------------------------

// outcomesForEntity returns every outcome row for entityID, ordered as
// ListRecentOutcomes returns them (created_at DESC).
func outcomesForEntity(t *testing.T, s *Server, entityID uuid.UUID) []outcome.Outcome {
	t.Helper()
	all, err := s.outcome.ListRecentOutcomes(context.Background(), nil, "task", 100)
	if err != nil {
		t.Fatalf("ListRecentOutcomes: %v", err)
	}
	var out []outcome.Outcome
	for _, o := range all {
		if o.EntityID == entityID {
			out = append(out, o)
		}
	}
	return out
}

// callRecordOutcomeFor is a small wrapper reducing boilerplate across this
// file's combination tests.
func callRecordOutcomeFor(t *testing.T, s *Server, entityID uuid.UUID, result, notes string) *mcpmsg.CallToolResult {
	t.Helper()
	return callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   entityID.String(),
		"result":      result,
		"notes":       notes,
	})
}

// TestOutcomeLifecycle_CompleteTaskThenRecordOutcome_FinalizesDraftInPlace
// covers combo (complete_task, record_outcome): complete_task seeds an
// 'unknown' draft; record_outcome must finalize that SAME row instead of
// creating an unrelated second one.
func TestOutcomeLifecycle_CompleteTaskThenRecordOutcome_FinalizesDraftInPlace(t *testing.T) {
	s := newTestWorkSessionServer(t)
	taskID := seedTaskWithDueDate(t, s, "")

	if r := callCompleteTask(t, s, map[string]any{"task_id": taskID.String()}); r.IsError {
		t.Fatalf("complete_task: %s", resultText(r))
	}
	draftRows := outcomesForEntity(t, s, taskID)
	if len(draftRows) != 1 || draftRows[0].Result != "unknown" {
		t.Fatalf("expected exactly 1 unknown draft after complete_task, got %+v", draftRows)
	}
	draftID := draftRows[0].ID

	r := callRecordOutcomeFor(t, s, taskID, "success", "shipped fine")
	if r.IsError {
		t.Fatalf("record_outcome: %s", resultText(r))
	}
	var recorded outcome.Outcome
	if err := json.Unmarshal([]byte(resultText(r)), &recorded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if recorded.ID != draftID {
		t.Errorf("record_outcome must finalize the SAME draft row: got %s, want %s", recorded.ID, draftID)
	}

	rows := outcomesForEntity(t, s, taskID)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 outcome row after complete_task->record_outcome, got %d: %+v", len(rows), rows)
	}
	if rows[0].Result != "success" {
		t.Errorf("Result = %q, want success", rows[0].Result)
	}
}

// TestOutcomeLifecycle_CompleteTaskThenFinishWorkFailure_FinalizesDraftInPlace
// covers combo (complete_task, finish_work): the task completed mid-session
// then reported as a failure by finish_work must finalize the SAME draft
// row seeded by complete_task, not add a second one — this is exactly the
// production duplication pattern found in the audit (2 entities with both
// an 'unknown' draft and a terminal outcome coexisting).
func TestOutcomeLifecycle_CompleteTaskThenFinishWorkFailure_FinalizesDraftInPlace(t *testing.T) {
	s, db := newTestWorkSessionServerWithDB(t)
	taskID := uuid.New().String()
	insertMCPTestTask(t, db, "", taskID)
	parsedTaskID, _ := uuid.Parse(taskID)

	startR := callStartWork(t, s, map[string]any{
		"repo_name": "lifecycle-combo-repo",
		"title":     "t",
		"goal":      "g",
		"task_ids":  `["` + taskID + `"]`,
	})
	sessID := startSessionID(t, startR)

	if r := callCompleteTask(t, s, map[string]any{"task_id": taskID}); r.IsError {
		t.Fatalf("complete_task: %s", resultText(r))
	}
	draftRows := outcomesForEntity(t, s, parsedTaskID)
	if len(draftRows) != 1 || draftRows[0].Result != "unknown" {
		t.Fatalf("expected exactly 1 unknown draft after complete_task, got %+v", draftRows)
	}
	draftID := draftRows[0].ID

	r := callFinishWork(t, s, map[string]any{
		"session_id":   sessID,
		"summary":      "shipped but broke prod",
		"final_result": finalResultFailure,
	})
	if r.IsError {
		t.Fatalf("finish_work: %s", resultText(r))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["outcome_id"] != draftID.String() {
		t.Errorf("finish_work must finalize the SAME draft row: outcome_id=%v, want %s", result["outcome_id"], draftID)
	}

	rows := outcomesForEntity(t, s, parsedTaskID)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 outcome row after complete_task->finish_work(failure), got %d: %+v", len(rows), rows)
	}
	if rows[0].Result != finalResultFailure {
		t.Errorf("Result = %q, want %s", rows[0].Result, finalResultFailure)
	}
}

// TestOutcomeLifecycle_RecordOutcomeThenRecordOutcome_DifferentResult_Supersedes
// covers combo (record_outcome, record_outcome) with a genuinely different
// result: the second call must NOT silently overwrite the first — it
// creates a new row with SupersedesID pointing back at the first, which
// stays unmodified (audit trail preserved).
func TestOutcomeLifecycle_RecordOutcomeThenRecordOutcome_DifferentResult_Supersedes(t *testing.T) {
	s := newTestWorkSessionServer(t)
	entityID := uuid.New()

	r1 := callRecordOutcomeFor(t, s, entityID, "success", "looked fine at the time")
	if r1.IsError {
		t.Fatalf("first record_outcome: %s", resultText(r1))
	}
	var first outcome.Outcome
	if err := json.Unmarshal([]byte(resultText(r1)), &first); err != nil {
		t.Fatalf("unmarshal first: %v", err)
	}

	r2 := callRecordOutcomeFor(t, s, entityID, "regressed", "actually broke prod a week later")
	if r2.IsError {
		t.Fatalf("second record_outcome: %s", resultText(r2))
	}
	var second outcome.Outcome
	if err := json.Unmarshal([]byte(resultText(r2)), &second); err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}

	if second.ID == first.ID {
		t.Fatal("a genuinely different result must create a NEW row, not reuse the first")
	}
	if second.SupersedesID == nil || *second.SupersedesID != first.ID {
		t.Errorf("SupersedesID = %v, want %s (explicit supersession, not silent overwrite)", second.SupersedesID, first.ID)
	}

	// The first row must be untouched — this is the audit-trail-preservation
	// property the threat model in the dispatch calls out explicitly.
	rows := outcomesForEntity(t, s, entityID)
	if len(rows) != 2 {
		t.Fatalf("expected exactly 2 rows (original + supersession), got %d: %+v", len(rows), rows)
	}
	for _, o := range rows {
		if o.ID == first.ID && o.Result != "success" {
			t.Errorf("first row's result must remain 'success' (not silently overwritten), got %q", o.Result)
		}
	}
}

// TestOutcomeLifecycle_RecordOutcomeThenRecordOutcome_IdenticalReplay_NoSecondRow
// covers combo (record_outcome, record_outcome) with an IDENTICAL retry:
// same entity, same result, same notes. This must be a no-op replay — not
// a second row, and not a second background atomization of the same notes
// text.
func TestOutcomeLifecycle_RecordOutcomeThenRecordOutcome_IdenticalReplay_NoSecondRow(t *testing.T) {
	s := newTestWorkSessionServer(t)
	entityID := uuid.New()

	r1 := callRecordOutcomeFor(t, s, entityID, "success", "idempotent retry test")
	if r1.IsError {
		t.Fatalf("first record_outcome: %s", resultText(r1))
	}
	var first outcome.Outcome
	if err := json.Unmarshal([]byte(resultText(r1)), &first); err != nil {
		t.Fatalf("unmarshal first: %v", err)
	}

	r2 := callRecordOutcomeFor(t, s, entityID, "success", "idempotent retry test")
	if r2.IsError {
		t.Fatalf("second (retried) record_outcome: %s", resultText(r2))
	}
	var second outcome.Outcome
	if err := json.Unmarshal([]byte(resultText(r2)), &second); err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}

	if second.ID != first.ID {
		t.Errorf("identical retry must return the SAME row, got a different ID: %s vs %s", second.ID, first.ID)
	}

	rows := outcomesForEntity(t, s, entityID)
	if len(rows) != 1 {
		t.Errorf("identical retry must not produce a second row: got %d rows, want 1", len(rows))
	}
}

// TestOutcomeLifecycle_RecordOutcomeThenFinishWorkFailure_Supersedes covers
// combo (record_outcome, finish_work): record_outcome already recorded a
// terminal "success" for the task; finish_work later reports the SAME task
// failed. The two are genuinely different executions' results, so this must
// supersede (new row), not silently overwrite the earlier "success".
func TestOutcomeLifecycle_RecordOutcomeThenFinishWorkFailure_Supersedes(t *testing.T) {
	s, db := newTestWorkSessionServerWithDB(t)
	taskID := uuid.New().String()
	insertMCPTestTask(t, db, "", taskID)
	parsedTaskID, _ := uuid.Parse(taskID)

	r1 := callRecordOutcomeFor(t, s, parsedTaskID, "success", "first pass looked good")
	if r1.IsError {
		t.Fatalf("record_outcome: %s", resultText(r1))
	}
	var first outcome.Outcome
	if err := json.Unmarshal([]byte(resultText(r1)), &first); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	startR := callStartWork(t, s, map[string]any{
		"repo_name": "lifecycle-ro-then-fw-repo",
		"title":     "t",
		"goal":      "g",
		"task_ids":  `["` + taskID + `"]`,
	})
	sessID := startSessionID(t, startR)

	r2 := callFinishWork(t, s, map[string]any{
		"session_id":   sessID,
		"summary":      "regression found in follow-up session",
		"final_result": finalResultFailure,
	})
	if r2.IsError {
		t.Fatalf("finish_work: %s", resultText(r2))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultText(r2)), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	outcomeIDStr, _ := result["outcome_id"].(string)
	if outcomeIDStr == first.ID.String() {
		t.Fatal("finish_work must not reuse record_outcome's row for a different result — expected a new superseding row")
	}

	rows := outcomesForEntity(t, s, parsedTaskID)
	if len(rows) != 2 {
		t.Fatalf("expected exactly 2 rows (record_outcome's success + finish_work's supersession), got %d: %+v", len(rows), rows)
	}
	for _, o := range rows {
		if o.ID == first.ID && o.Result != "success" {
			t.Errorf("record_outcome's original row must remain 'success', got %q", o.Result)
		}
		if o.ID.String() == outcomeIDStr {
			if o.Result != finalResultFailure {
				t.Errorf("finish_work's row Result = %q, want %s", o.Result, finalResultFailure)
			}
			if o.SupersedesID == nil || *o.SupersedesID != first.ID {
				t.Errorf("finish_work's row SupersedesID = %v, want %s", o.SupersedesID, first.ID)
			}
		}
	}
}

// TestOutcomeLifecycle_FinishWorkFailureTwice_Supersedes covers combo
// (finish_work, finish_work): the same task fails in two different work
// sessions with different summaries. Each finish_work call is a genuinely
// distinct execution's result, so the second must supersede the first
// rather than being silently treated as a duplicate.
func TestOutcomeLifecycle_FinishWorkFailureTwice_Supersedes(t *testing.T) {
	s, db := newTestWorkSessionServerWithDB(t)
	taskID := uuid.New().String()
	insertMCPTestTask(t, db, "", taskID)
	parsedTaskID, _ := uuid.Parse(taskID)

	start1 := callStartWork(t, s, map[string]any{
		"repo_name": "lifecycle-fw-fw-repo-1",
		"title":     "t1",
		"goal":      "g1",
		"task_ids":  `["` + taskID + `"]`,
	})
	sess1 := startSessionID(t, start1)
	r1 := callFinishWork(t, s, map[string]any{
		"session_id":   sess1,
		"summary":      "first attempt failed: timeout",
		"final_result": finalResultFailure,
	})
	if r1.IsError {
		t.Fatalf("first finish_work: %s", resultText(r1))
	}
	var res1 map[string]any
	if err := json.Unmarshal([]byte(resultText(r1)), &res1); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	firstOutcomeID, _ := res1["outcome_id"].(string)

	start2 := callStartWork(t, s, map[string]any{
		"repo_name": "lifecycle-fw-fw-repo-2",
		"title":     "t2",
		"goal":      "g2",
		"task_ids":  `["` + taskID + `"]`,
	})
	sess2 := startSessionID(t, start2)
	r2 := callFinishWork(t, s, map[string]any{
		"session_id":   sess2,
		"summary":      "second attempt failed: different root cause",
		"final_result": finalResultFailure,
	})
	if r2.IsError {
		t.Fatalf("second finish_work: %s", resultText(r2))
	}
	var res2 map[string]any
	if err := json.Unmarshal([]byte(resultText(r2)), &res2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	secondOutcomeID, _ := res2["outcome_id"].(string)

	if secondOutcomeID == firstOutcomeID {
		t.Fatal("two distinct failure executions (different summaries) must not collapse into one row")
	}

	rows := outcomesForEntity(t, s, parsedTaskID)
	if len(rows) != 2 {
		t.Fatalf("expected exactly 2 rows for two distinct finish_work failures, got %d: %+v", len(rows), rows)
	}
	found := false
	for _, o := range rows {
		if o.ID.String() == secondOutcomeID {
			found = true
			if o.SupersedesID == nil || o.SupersedesID.String() != firstOutcomeID {
				t.Errorf("second finish_work's row SupersedesID = %v, want %s", o.SupersedesID, firstOutcomeID)
			}
		}
	}
	if !found {
		t.Fatalf("second outcome_id %s not found among entity rows", secondOutcomeID)
	}
}
