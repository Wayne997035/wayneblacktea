package handler_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/proposal"
)

// A handler built WITHOUT WithGoalProjectAccept must fail CLOSED for TypeTask: a
// resolve-only fallback would flip the proposal to accepted with no tasks row
// behind it, and Resolve only matches status='pending', so that loss is
// permanent. These cases need no DB: a fake proposal store records every Resolve
// call, and "zero" is the assertion. The helpers (unwiredProposalRow,
// unwiredHandler, postConfirm, ...) come from
// proposal_handler_decision_wiring_test.go and the decision suite.

func TestConfirmProposal_TypeTask_AdapterNotWired_FailsClosed(t *testing.T) {
	// [F205-12] an unwired handler must not resolve a TypeTask proposal.
	t.Run("accept_fails_closed", func(t *testing.T) {
		t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
		row := unwiredProposalRow(proposal.TypeTask, taskPayloadJSON(t, "unwired-task-single", cleanKindTestDescription, taskKindGeneral))
		store := newFakeProposalStore(row)

		rec := postConfirm(unwiredHandler(store), row.ID, acceptBody)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := strings.TrimSpace(rec.Body.String()); got != internalErrorBody {
			t.Errorf("body = %s, want %s", got, internalErrorBody)
		}
		if len(store.resolved) != 0 {
			t.Errorf("Resolve calls = %v, want none (an unwired TypeTask must not be resolved)", store.resolved)
		}
		if store.byID[row.ID].Status != proposalPending {
			t.Errorf("proposal status = %q, want still %q", store.byID[row.ID].Status, proposalPending)
		}
	})

	// The nil-adapter check runs BEFORE the payload gate: a vague payload under
	// strict mode would be a 400 if the gate ran first, but an unwired handler
	// answers 500 whatever the payload says.
	t.Run("fails_closed_before_the_payload_gate", func(t *testing.T) {
		t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOn)
		row := unwiredProposalRow(proposal.TypeTask, taskPayloadJSON(t, "unwired-task-vague", taskWarnVague, taskKindGeneral))
		store := newFakeProposalStore(row)

		rec := postConfirm(unwiredHandler(store), row.ID, acceptBody)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("want 500 (the unwired check precedes the gate), got %d: %s", rec.Code, rec.Body.String())
		}
		if len(store.resolved) != 0 {
			t.Errorf("Resolve calls = %v, want none", store.resolved)
		}
	})

	// Control on the same unwired handler: the fail-closed carve-out covers
	// accept only. Reject takes no materialisation, so it still resolves.
	t.Run("reject_still_rejected", func(t *testing.T) {
		t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
		row := unwiredProposalRow(proposal.TypeTask, taskPayloadJSON(t, "unwired-task-reject", cleanKindTestDescription, taskKindGeneral))
		store := newFakeProposalStore(row)

		rec := postConfirm(unwiredHandler(store), row.ID, `{"action":"reject"}`)

		if rec.Code != http.StatusOK {
			t.Fatalf("want 200 (reject is not gated), got %d: %s", rec.Code, rec.Body.String())
		}
		if len(store.resolved) != 1 || store.resolved[0] != row.ID {
			t.Errorf("Resolve calls = %v, want exactly [%s]", store.resolved, row.ID)
		}
		if store.byID[row.ID].Status != string(proposal.StatusRejected) {
			t.Errorf("proposal status = %q, want %q", store.byID[row.ID].Status, proposal.StatusRejected)
		}
	})
}

func TestConfirmBatch_TypeTask_AdapterNotWired_FailsClosed(t *testing.T) {
	// [F205-13] an unwired handler must not resolve a TypeTask proposal in a batch either.
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
	taskRow := unwiredProposalRow(proposal.TypeTask, taskPayloadJSON(t, "unwired-task-batch", cleanKindTestDescription, taskKindGeneral))
	goalRow := unwiredProposalRow(proposal.TypeGoal, mustMarshal(t, map[string]any{"title": "unwired-task-goal", "area": "career"}))
	store := newFakeProposalStore(taskRow, goalRow)

	rec := postConfirmBatch(t, unwiredHandler(store), batchActionAccept, taskRow.ID, goalRow.ID)

	entry := decodeBatchResultEntry(t, rec.Body.Bytes(), taskRow.ID.String())
	if entry.OK || entry.Skipped {
		t.Errorf("task entry = %+v, want ok:false skipped:false", entry)
	}
	if entry.Error != "internal error" {
		t.Errorf("task entry error = %q, want %q", entry.Error, "internal error")
	}
	for _, id := range store.resolved {
		if id == taskRow.ID {
			t.Errorf("Resolve was called for the unwired task %s", id)
		}
	}
	if store.byID[taskRow.ID].Status != proposalPending {
		t.Errorf("task proposal status = %q, want still %q", store.byID[taskRow.ID].Status, proposalPending)
	}
	// Control, same request: the goal on the same unwired handler still takes
	// the legacy resolve-only path, so the fail-closed carve-out is per-type.
	assertBatchResultOK(t, rec.Body.Bytes(), goalRow.ID.String())
}
