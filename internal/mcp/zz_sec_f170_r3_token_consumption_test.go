package mcp

import (
	"context"
	"strings"
	"testing"
)

// [F170-SEC-R3-03] Both confirm paths used to LoadAndDelete the token and
// validate afterwards, so a refusal destroyed the pending operation. The
// rightful caller's retry was then answered "no pending reconciliation" /
// "no pending deletion" — an explanation unrelated to why it was actually
// refused, sending whoever reads it to debug the wrong thing, and costing a
// full preview round trip inside a 60s TTL.
//
// The two tools are documented as one property, so they are tested as one
// property here rather than in their own files; a fix applied to only one of
// them leaves half of these red.

// TestF170SECR303_ReconcileRejectedConfirmDoesNotConsumeToken is the pair of
// TestF170_12_ReconcileCrossSessionConfirmRejected: that one proves the wrong
// session is refused, this one proves the refusal costs the RIGHT session
// nothing.
//
// Mutation proof: put LoadAndDelete back at the top of
// handleReconcileMergedPRsConfirm and the second confirm below fails with
// "no pending reconciliation".
func TestF170SECR303_ReconcileRejectedConfirmDoesNotConsumeToken(t *testing.T) {
	s := withReconcileCandidates(t, newTestWorkSessionServer(t))
	branch := "feature/f170-r3-03-nonconsuming"
	task := seedBranchedTask(t, s, "non-consuming refusal", branch)

	ctxA := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "r3-03-session-A"})
	ctxB := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "r3-03-session-B"})

	preview := extractReconcileToken(t, callReconcileCtx(t, ctxA, s, reconcilePayloadForBranch(branch), nil))
	if len(preview.Matches) != 1 {
		t.Fatalf("preview matches = %d, want 1 — fixture did not match, so nothing below "+
			"would be exercising a real token", len(preview.Matches))
	}

	rejected := callReconcileCtx(t, ctxB, s, nil, map[string]any{
		"confirm":         true,
		"reconcile_token": preview.ReconcileToken,
	})
	if !rejected.IsError {
		t.Fatalf("cross-session confirm must still be rejected: %s", resultText(rejected))
	}

	// The point of the finding: session A can still spend its own token.
	retry := callReconcileCtx(t, ctxA, s, nil, map[string]any{
		"confirm":         true,
		"reconcile_token": preview.ReconcileToken,
	})
	if retry.IsError {
		t.Fatalf("the issuing session could not spend its own token after somebody else "+
			"was refused — the refusal consumed it: %s", resultText(retry))
	}

	got, err := s.gtd.GetTaskByID(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != taskStatusCompleted {
		t.Errorf("task status = %q, want %q — the retry reported success but did nothing",
			got.Status, taskStatusCompleted)
	}
}

// TestF170SECR303_DeleteTaskRejectedConfirmDoesNotConsumeToken is the same
// property on delete_task's session branch.
func TestF170SECR303_DeleteTaskRejectedConfirmDoesNotConsumeToken(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	ctxA := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "r3-03-del-A"})
	ctxB := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "r3-03-del-B"})

	token := extractToken(t, callDeleteTaskCtx(t, ctxA, s, map[string]any{"task_id": id.String()}))

	rejected := callDeleteTaskCtx(t, ctxB, s, map[string]any{
		"task_id":        id.String(),
		"confirm":        true,
		"deletion_token": token,
	})
	if !rejected.IsError {
		t.Fatalf("cross-session confirm must still be rejected: %s", resultText(rejected))
	}

	retry := callDeleteTaskCtx(t, ctxA, s, map[string]any{
		"task_id":        id.String(),
		"confirm":        true,
		"deletion_token": token,
	})
	if retry.IsError {
		t.Fatalf("the issuing session could not spend its own deletion token after somebody "+
			"else was refused: %s", resultText(retry))
	}

	tasks, err := s.gtd.Tasks(context.Background(), nil)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	for _, tk := range tasks {
		if tk.ID == id {
			t.Error("retry reported success but the task is still there")
		}
	}
}

// TestF170SECR303_DeleteTaskWrongTokenStillConsumes pins the asymmetry that
// the fix above deliberately did NOT flatten.
//
// deleteTokens is keyed by TASK ID, not by the token, so a caller holding only
// a task id can reach the token comparison. If that branch also refused
// without consuming, the 60s window would become a free guessing gallery for
// the token. The session branch has no such exposure — reaching it already
// required presenting the correct token — which is why exactly one of the two
// is non-consuming.
//
// Without this test, "make refusals non-consuming" reads like a rule that
// should apply to both branches, and the next person to tidy this up would
// remove the guess-limiting behaviour believing they were finishing the job.
func TestF170SECR303_DeleteTaskWrongTokenStillConsumes(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	real := extractToken(t, callDeleteTask(t, s, map[string]any{"task_id": id.String()}))

	guess := callDeleteTask(t, s, map[string]any{
		"task_id":        id.String(),
		"confirm":        true,
		"deletion_token": "not-the-token",
	})
	if !guess.IsError {
		t.Fatalf("a wrong deletion_token must error: %s", resultText(guess))
	}

	after := callDeleteTask(t, s, map[string]any{
		"task_id":        id.String(),
		"confirm":        true,
		"deletion_token": real,
	})
	if !after.IsError {
		t.Fatal("one wrong guess must burn the pending deletion; the real token still " +
			"worked afterwards, so the token is now brute-forcible within its TTL")
	}
	if !strings.Contains(resultText(after), "no pending deletion") {
		t.Errorf("after a burned token the caller should be told there is nothing pending, got: %s",
			resultText(after))
	}
}
