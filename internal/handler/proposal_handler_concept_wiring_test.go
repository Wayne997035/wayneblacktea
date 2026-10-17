package handler_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
)

// A handler built WITHOUT WithGoalProjectAccept must fail CLOSED for
// TypeConcept: a resolve-only fallback would flip the proposal to accepted
// with no concepts row behind it, and Resolve only matches status='pending',
// so that loss is permanent. The old two-step concept path is gone together
// with the handler's learning dependency (NewProposalHandler takes only the
// proposal store). These cases need no DB: a fake proposal store records every
// Resolve call, and "zero Resolve calls" is the assertion. The helpers
// (unwiredProposalRow, postConfirm, ...) come from
// proposal_handler_decision_wiring_test.go and the decision suite.

func TestConfirmProposal_TypeConcept_AdapterNotWired_FailsClosed(t *testing.T) {
	// [F205-07] an unwired handler must not resolve a TypeConcept proposal.
	row := unwiredProposalRow(proposal.TypeConcept, conceptPayloadJSON(t, "unwired-concept-single"))
	store := newFakeProposalStore(row)

	rec := postConfirm(handler.NewProposalHandler(store), row.ID, acceptBody)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != internalErrorBody {
		t.Errorf("body = %s, want %s", got, internalErrorBody)
	}
	if len(store.resolved) != 0 {
		t.Errorf("Resolve calls = %v, want none (an unwired TypeConcept must not be resolved)", store.resolved)
	}
	if store.byID[row.ID].Status != proposalPending {
		t.Errorf("proposal status = %q, want still %q", store.byID[row.ID].Status, proposalPending)
	}
}

func TestConfirmBatch_TypeConcept_AdapterNotWired_FailsClosed(t *testing.T) {
	conRow := unwiredProposalRow(proposal.TypeConcept, conceptPayloadJSON(t, "unwired-concept-batch"))
	goalRow := unwiredProposalRow(proposal.TypeGoal, mustMarshal(t, map[string]any{"title": "unwired-concept-goal", "area": "career"}))
	store := newFakeProposalStore(conRow, goalRow)

	rec := postConfirmBatch(t, handler.NewProposalHandler(store), batchActionAccept, conRow.ID, goalRow.ID)

	entry := decodeBatchResultEntry(t, rec.Body.Bytes(), conRow.ID.String())
	if entry.OK || entry.Skipped {
		t.Errorf("concept entry = %+v, want ok:false skipped:false", entry)
	}
	if entry.Error != "internal error" {
		t.Errorf("concept entry error = %q, want %q", entry.Error, "internal error")
	}
	for _, id := range store.resolved {
		if id == conRow.ID {
			t.Errorf("Resolve was called for the unwired concept %s", id)
		}
	}
	// Control, same request: the goal on the same unwired handler still takes
	// the legacy resolve-only path, so the fail-closed carve-out is per-type.
	assertBatchResultOK(t, rec.Body.Bytes(), goalRow.ID.String())
}
