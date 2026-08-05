package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/atom"
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
	if len(draftRows) != 1 || draftRows[0].Result != outcomeResultUnknown {
		t.Fatalf("expected exactly 1 unknown draft after complete_task, got %+v", draftRows)
	}
	draftID := draftRows[0].ID

	r := callRecordOutcomeFor(t, s, taskID, finalResultSuccess, "shipped fine")
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
	if rows[0].Result != finalResultSuccess {
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
	if len(draftRows) != 1 || draftRows[0].Result != outcomeResultUnknown {
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

	r1 := callRecordOutcomeFor(t, s, entityID, finalResultSuccess, "looked fine at the time")
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
		if o.ID == first.ID && o.Result != finalResultSuccess {
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

	r1 := callRecordOutcomeFor(t, s, entityID, finalResultSuccess, "idempotent retry test")
	if r1.IsError {
		t.Fatalf("first record_outcome: %s", resultText(r1))
	}
	var first outcome.Outcome
	if err := json.Unmarshal([]byte(resultText(r1)), &first); err != nil {
		t.Fatalf("unmarshal first: %v", err)
	}

	r2 := callRecordOutcomeFor(t, s, entityID, finalResultSuccess, "idempotent retry test")
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

	r1 := callRecordOutcomeFor(t, s, parsedTaskID, finalResultSuccess, "first pass looked good")
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
		if o.ID == first.ID && o.Result != finalResultSuccess {
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

// TestOutcomeLifecycle_RecordOutcomeUnknownAgainstExistingDraft_PreservesContent
// reproduces PR #152 second army finding M-1 end-to-end through the real
// record_outcome MCP tool (the actual threat surface: a prompt-injected
// agent calling record_outcome(result="unknown")) against a real
// SQLite-backed server. Before the fix, RecordExecutionResult routed a
// result="unknown" call straight into FinalizeDraft's blanket UPDATE
// whenever the entity already had an 'unknown' draft — silently erasing the
// draft's notes, metrics, related_rule_ids, and work_session_id with
// whatever the attacking call supplied (here: nothing). The draft must
// survive completely unchanged, in the SAME row (no new row, no audit
// trail loss either way).
func TestOutcomeLifecycle_RecordOutcomeUnknownAgainstExistingDraft_PreservesContent(t *testing.T) {
	s := newTestWorkSessionServer(t)
	entityID := uuid.New()
	sessionID := uuid.New()
	relatedRuleID := uuid.New()

	// Seed a draft carrying real content directly via record_outcome itself
	// (result="unknown" is a documented AllowedResults value, not only ever
	// auto-seeded by complete_task) — mirrors a user recording "still
	// unknown, but here's what I know so far".
	seedR := callRecordOutcome(t, s, map[string]any{
		"entity_type":      entityTypeTask,
		"entity_id":        entityID.String(),
		"result":           outcomeResultUnknown,
		"notes":            "real postmortem content the attacker wants gone",
		"metrics_json":     `{"duration_ms":4200}`,
		"related_rule_ids": `["` + relatedRuleID.String() + `"]`,
		"session_id":       sessionID.String(),
	})
	if seedR.IsError {
		t.Fatalf("seed record_outcome: %s", resultText(seedR))
	}
	var seeded outcome.Outcome
	if err := json.Unmarshal([]byte(resultText(seedR)), &seeded); err != nil {
		t.Fatalf("unmarshal seeded: %v", err)
	}
	if seeded.Result != outcomeResultUnknown {
		t.Fatalf("precondition failed: seeded draft Result = %q, want %s", seeded.Result, outcomeResultUnknown)
	}

	// The attack: another record_outcome call, still result="unknown", with
	// NO notes/metrics/related_rule_ids/session_id — exactly what a minimal
	// prompt-injected tool call would send.
	attackR := callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   entityID.String(),
		"result":      outcomeResultUnknown,
	})
	if attackR.IsError {
		t.Fatalf("attack record_outcome: %s", resultText(attackR))
	}
	var afterAttack outcome.Outcome
	if err := json.Unmarshal([]byte(resultText(attackR)), &afterAttack); err != nil {
		t.Fatalf("unmarshal after attack: %v", err)
	}

	if afterAttack.ID != seeded.ID {
		t.Errorf("attack call must return the SAME draft row: got %s, want %s", afterAttack.ID, seeded.ID)
	}
	if afterAttack.Notes != seeded.Notes {
		t.Errorf("Notes erased by unknown-against-unknown call: got %q, want unchanged %q", afterAttack.Notes, seeded.Notes)
	}
	if string(afterAttack.Metrics) != string(seeded.Metrics) {
		t.Errorf("Metrics erased by unknown-against-unknown call: got %q, want unchanged %q", afterAttack.Metrics, seeded.Metrics)
	}
	if len(afterAttack.RelatedRuleIDs) != 1 || afterAttack.RelatedRuleIDs[0] != relatedRuleID {
		t.Errorf("RelatedRuleIDs erased by unknown-against-unknown call: got %v, want unchanged [%s]",
			afterAttack.RelatedRuleIDs, relatedRuleID)
	}
	if afterAttack.WorkSessionID == nil || *afterAttack.WorkSessionID != sessionID {
		t.Errorf("WorkSessionID erased by unknown-against-unknown call: got %v, want unchanged %s",
			afterAttack.WorkSessionID, sessionID)
	}

	// No new row and no supersession — this is a pure no-write no-op, not a
	// second audit entry either.
	rows := outcomesForEntity(t, s, entityID)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 outcome row after unknown-against-unknown call, got %d: %+v", len(rows), rows)
	}
}

// TestOutcomeLifecycle_RecordOutcomeUnknownWithContent_EnrichesExistingDraft
// reproduces PR #152 second army finding M-2a end-to-end through the real
// record_outcome MCP tool: a result="unknown" call that DOES carry new
// content (exactly the shape the MCP instructions in server.go document
// callers using to enrich a complete_task-seeded draft) against an existing
// 'unknown' draft must actually WRITE that content — the M-1 fix's earlier
// form silently dropped it (see ActionDraftPreserved / ActionDraftEnriched
// doc comments). Same row, no new row, result stays unknown, but the new
// content lands and pre-existing fields the second call didn't touch
// survive (merge, not overwrite).
func TestOutcomeLifecycle_RecordOutcomeUnknownWithContent_EnrichesExistingDraft(t *testing.T) {
	s := newTestWorkSessionServer(t)
	entityID := uuid.New()
	relatedRuleID := uuid.New()

	seedR := callRecordOutcome(t, s, map[string]any{
		"entity_type":      entityTypeTask,
		"entity_id":        entityID.String(),
		"result":           outcomeResultUnknown,
		"metrics_json":     `{"duration_ms":4200}`,
		"related_rule_ids": `["` + relatedRuleID.String() + `"]`,
	})
	if seedR.IsError {
		t.Fatalf("seed record_outcome: %s", resultText(seedR))
	}
	var seeded outcome.Outcome
	if err := json.Unmarshal([]byte(resultText(seedR)), &seeded); err != nil {
		t.Fatalf("unmarshal seeded: %v", err)
	}

	// Enrich: still result="unknown", but this call supplies notes the seed
	// call didn't have. metrics/related_rule_ids are omitted here — they
	// must survive from the seed call (merge, not overwrite).
	enrichR := callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   entityID.String(),
		"result":      outcomeResultUnknown,
		"notes":       "still unknown, but here's what I know so far",
	})
	if enrichR.IsError {
		t.Fatalf("enrich record_outcome: %s", resultText(enrichR))
	}
	var enriched outcome.Outcome
	if err := json.Unmarshal([]byte(resultText(enrichR)), &enriched); err != nil {
		t.Fatalf("unmarshal enriched: %v", err)
	}

	if enriched.ID != seeded.ID {
		t.Errorf("enrich call must return the SAME draft row: got %s, want %s", enriched.ID, seeded.ID)
	}
	if enriched.Result != outcomeResultUnknown {
		t.Errorf("Result = %q, want unknown (enrich is not a finalization)", enriched.Result)
	}
	if enriched.Notes != "still unknown, but here's what I know so far" {
		t.Errorf("Notes = %q, want the newly-supplied content to have been written (M-2a regression)", enriched.Notes)
	}
	if string(enriched.Metrics) != `{"duration_ms":4200}` {
		t.Errorf("Metrics = %q, want preserved from the seed call (merge, not overwrite)", enriched.Metrics)
	}
	if len(enriched.RelatedRuleIDs) != 1 || enriched.RelatedRuleIDs[0] != relatedRuleID {
		t.Errorf("RelatedRuleIDs = %v, want preserved from the seed call [%s]", enriched.RelatedRuleIDs, relatedRuleID)
	}

	rows := outcomesForEntity(t, s, entityID)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 outcome row after seed->enrich, got %d: %+v", len(rows), rows)
	}
}

// ---------------------------------------------------------------------------
// Side-effect gating for ActionDraftPreserved vs ActionDraftEnriched — a gap
// three-army mutation testing found in the M-1 round: the skip list in
// handleRecordOutcome (tools_outcome.go) that suppresses SetOutcomeLink /
// atomize for no-write actions had NO test coverage. Both tests below wire a
// real SQLite-backed server (workSession + atom stores are real; atomizer is
// swapped for a fake sentinel + srv.atomizeFn spy, matching
// newAtomizeServer's pattern in tools_worksession_test.go / tools_atom_test.go).
// ---------------------------------------------------------------------------

// newAtomizeSpyServer builds a real SQLite-backed *Server (like
// newTestWorkSessionServer) and additionally wires a fake atomizer sentinel
// + an atomizeFn spy so tests can observe whether launchAtomize's payload
// function actually ran.
func newAtomizeSpyServer(t *testing.T) (*Server, chan uuid.UUID) {
	t.Helper()
	s := newTestWorkSessionServer(t)
	if s.atomizer == nil {
		// New() sets atomizer to ai.NewAtomizer(), which returns nil when
		// CLAUDE_API_KEY is absent. We only need a non-nil sentinel to pass
		// launchAtomize's nil guard — atomizeFn below replaces the real call.
		s.atomizer = &ai.Atomizer{}
	}
	calls := make(chan uuid.UUID, 4)
	s.atomizeFn = func(_ context.Context, _ *ai.Atomizer, _ atom.StoreIface, _ *uuid.UUID, _ string, parentID uuid.UUID, _ string) {
		calls <- parentID
	}
	return s, calls
}

// TestHandleRecordOutcome_DraftPreserved_SkipsAtomize verifies the skip-list
// side of M-2a's fix for atomize: an ActionDraftPreserved call
// (unknown-against-unknown, NO new content at all) must not trigger
// atomize — nothing was written, so atomizing would mean atomizing the
// pre-existing draft's notes again for no reason (duplicate atoms on every
// no-op poll/retry).
//
// NOTE on SetOutcomeLink: this test deliberately does NOT pass session_id
// on the attack call, and does not assert anything about SetOutcomeLink.
// Per hasNewContent (lifecycle.go), a non-nil WorkSessionID unconditionally
// counts as "new content", so a call carrying session_id can never land in
// ActionDraftPreserved under this fix — it is reclassified to
// ActionDraftEnriched (covered by
// TestHandleRecordOutcome_DraftEnriched_TriggersSetOutcomeLinkAndAtomize
// below) precisely so a caller's session link is never silently dropped.
// There is therefore no live code path where handleRecordOutcome's
// SetOutcomeLink call is reachable AND action == ActionDraftPreserved; a
// test asserting "SetOutcomeLink didn't fire" here would only be exercising
// the unrelated `in.sessionID != nil` guard (already covered by
// TestHandleRecordOutcome_NoSessionID_Regression), not the skip-list branch.
// TestHasNewContent (lifecycle_test.go) is the test that pins down this
// WorkSessionID-forces-Enriched invariant.
func TestHandleRecordOutcome_DraftPreserved_SkipsAtomize(t *testing.T) {
	s, atomizeCalls := newAtomizeSpyServer(t)
	entityID := uuid.New()

	// Seed a draft with real notes so the returned draft (o.Notes) is
	// non-empty — if the skip-list guard were missing, the atomize check
	// `if o.Notes != ""` below it would fire.
	seedR := callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   entityID.String(),
		"result":      outcomeResultUnknown,
		"notes":       "seed content",
	})
	if seedR.IsError {
		t.Fatalf("seed record_outcome: %s", resultText(seedR))
	}
	// Drain whatever the seed call's own ActionCreated triggered before
	// isolating the preserved call under test.
	select {
	case <-atomizeCalls:
	case <-time.After(200 * time.Millisecond):
	}

	attackR := callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   entityID.String(),
		"result":      outcomeResultUnknown,
	})
	if attackR.IsError {
		t.Fatalf("attack record_outcome: %s", resultText(attackR))
	}

	select {
	case pid := <-atomizeCalls:
		t.Errorf("atomizeFn fired for ActionDraftPreserved (parent_id=%s), want no call", pid)
	case <-time.After(150 * time.Millisecond):
		// expected: nothing arrives
	}
}

// TestHandleRecordOutcome_DraftEnriched_TriggersSetOutcomeLinkAndAtomize
// verifies the flip side: an ActionDraftEnriched call (unknown-against-
// unknown, WITH new content) DOES write, so it must trigger both
// SetOutcomeLink and atomize exactly like ActionFinalizedDraft/ActionCreated
// would.
func TestHandleRecordOutcome_DraftEnriched_TriggersSetOutcomeLinkAndAtomize(t *testing.T) {
	s, atomizeCalls := newAtomizeSpyServer(t)
	entityID := uuid.New()

	seedR := callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   entityID.String(),
		"result":      outcomeResultUnknown,
	})
	if seedR.IsError {
		t.Fatalf("seed record_outcome: %s", resultText(seedR))
	}

	startR := callStartWork(t, s, map[string]any{"repo_name": "draft-enriched-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	enrichR := callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   entityID.String(),
		"result":      outcomeResultUnknown,
		"notes":       "enriching with real content",
		"session_id":  sessID,
	})
	if enrichR.IsError {
		t.Fatalf("enrich record_outcome: %s", resultText(enrichR))
	}
	var enriched outcome.Outcome
	if err := json.Unmarshal([]byte(resultText(enrichR)), &enriched); err != nil {
		t.Fatalf("unmarshal enriched: %v", err)
	}

	select {
	case pid := <-atomizeCalls:
		if pid != enriched.ID {
			t.Errorf("atomizeFn fired with parent_id=%s, want the enriched outcome's ID %s", pid, enriched.ID)
		}
	case <-time.After(2 * time.Second):
		t.Error("atomizeFn did not fire for ActionDraftEnriched within timeout, want a call (M-2a regression)")
	}

	sessIDParsed, _ := uuid.Parse(sessID)
	got, err := s.workSession.GetByID(context.Background(), s.workspaceUUIDVal(), sessIDParsed)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.OutcomeID == nil || *got.OutcomeID != enriched.ID {
		t.Errorf("SetOutcomeLink did not fire for ActionDraftEnriched: session.outcome_id = %v, want %s", got.OutcomeID, enriched.ID)
	}
}

// TestHandleRecordOutcome_DraftEnrichRetry_ByteIdenticalSkipsSecondAtomize is
// the PR #152 round 3 Major reproduction end-to-end through the real
// record_outcome MCP tool against a real SQLite-backed server: a
// byte-identical retry of a content-bearing enrich call (same notes) must
// NOT trigger a second atomize call. Before this fix, the retry landed back
// in RecordExecutionResult's draft branch (result stays "unknown" after the
// first enrich), found hasNewContent still true, and re-ran FinalizeDraft —
// reported as ActionDraftEnriched a second time, which is NOT in
// tools_outcome.go's idempotent-skip list, so atomize fired twice for the
// same notes text.
func TestHandleRecordOutcome_DraftEnrichRetry_ByteIdenticalSkipsSecondAtomize(t *testing.T) {
	s, atomizeCalls := newAtomizeSpyServer(t)
	entityID := uuid.New()

	seedR := callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   entityID.String(),
		"result":      outcomeResultUnknown,
	})
	if seedR.IsError {
		t.Fatalf("seed record_outcome: %s", resultText(seedR))
	}

	enrichArgs := map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   entityID.String(),
		"result":      outcomeResultUnknown,
		"notes":       "enriching with real content",
	}

	firstR := callRecordOutcome(t, s, enrichArgs)
	if firstR.IsError {
		t.Fatalf("first enrich record_outcome: %s", resultText(firstR))
	}
	var first outcome.Outcome
	if err := json.Unmarshal([]byte(resultText(firstR)), &first); err != nil {
		t.Fatalf("unmarshal first: %v", err)
	}

	select {
	case pid := <-atomizeCalls:
		if pid != first.ID {
			t.Errorf("first call: atomizeFn fired with parent_id=%s, want %s", pid, first.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first call: atomizeFn did not fire within timeout, want a call (this call genuinely wrote new content)")
	}

	// The retry: identical args, byte-for-byte.
	secondR := callRecordOutcome(t, s, enrichArgs)
	if secondR.IsError {
		t.Fatalf("retry enrich record_outcome: %s", resultText(secondR))
	}
	var second outcome.Outcome
	if err := json.Unmarshal([]byte(resultText(secondR)), &second); err != nil {
		t.Fatalf("unmarshal second: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("retry must return the SAME row: got %s, want %s", second.ID, first.ID)
	}

	select {
	case pid := <-atomizeCalls:
		t.Errorf("atomizeFn fired a SECOND time for a byte-identical retry (parent_id=%s) — Major regression", pid)
	case <-time.After(150 * time.Millisecond):
		// expected: nothing arrives
	}

	rows := outcomesForEntity(t, s, entityID)
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 outcome row after seed->enrich->identical-retry, got %d: %+v", len(rows), rows)
	}
}
