package handler_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// A handler built WITHOUT WithGoalProjectAccept must fail CLOSED for
// TypeDecision: a resolve-only fallback would flip the proposal to accepted
// with no decisions row behind it, and Resolve only matches status='pending',
// so that loss is permanent. TypeGoal/TypeProject/TypeKnowledge keep their
// legacy resolve-only fallback — the carve-out is decision-only. These cases
// need no DB: a fake proposal store records every Resolve call, and "zero
// Resolve calls" is the assertion.

func unwiredProposalRow(typ proposal.Type, payload []byte) db.PendingProposal {
	return db.PendingProposal{
		ID:      uuid.New(),
		Type:    string(typ),
		Status:  proposalPending,
		Payload: payload,
	}
}

func unwiredHandler(store *fakeProposalStore) *handler.ProposalHandler {
	return handler.NewProposalHandler(store, &fakeProposalLearningStore{})
}

func TestConfirmProposal_TypeDecision_AdapterNotWired_FailsClosed(t *testing.T) {
	// [F205-03] an unwired handler must not resolve a TypeDecision proposal.
	row := unwiredProposalRow(proposal.TypeDecision, decisionPayload(t, "unwired-single"))
	store := newFakeProposalStore(row)

	rec := postConfirm(unwiredHandler(store), row.ID, acceptBody)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := strings.TrimSpace(rec.Body.String()); got != internalErrorBody {
		t.Errorf("body = %s, want %s", got, internalErrorBody)
	}
	if len(store.resolved) != 0 {
		t.Errorf("Resolve calls = %v, want none (an unwired TypeDecision must not be resolved)", store.resolved)
	}
	if store.byID[row.ID].Status != proposalPending {
		t.Errorf("proposal status = %q, want still %q", store.byID[row.ID].Status, proposalPending)
	}
}

func TestConfirmBatch_TypeDecision_AdapterNotWired_FailsClosed(t *testing.T) {
	decRow := unwiredProposalRow(proposal.TypeDecision, decisionPayload(t, "unwired-batch"))
	goalRow := unwiredProposalRow(proposal.TypeGoal, mustMarshal(t, map[string]any{"title": "unwired-batch-goal", "area": "career"}))
	store := newFakeProposalStore(decRow, goalRow)

	rec := postConfirmBatch(t, unwiredHandler(store), batchActionAccept, decRow.ID, goalRow.ID)

	entry := decodeBatchResultEntry(t, rec.Body.Bytes(), decRow.ID.String())
	if entry.OK || entry.Skipped {
		t.Errorf("decision entry = %+v, want ok:false skipped:false", entry)
	}
	if entry.Error != "internal error" {
		t.Errorf("decision entry error = %q, want %q", entry.Error, "internal error")
	}
	for _, id := range store.resolved {
		if id == decRow.ID {
			t.Errorf("Resolve was called for the unwired decision %s", id)
		}
	}
	// Control, same request: the goal on the same unwired handler still takes
	// the legacy resolve-only path, so the fail-closed carve-out is per-type.
	assertBatchResultOK(t, rec.Body.Bytes(), goalRow.ID.String())
}

func TestConfirmProposal_TypeGoal_AdapterNotWired_StillResolveOnly(t *testing.T) {
	row := unwiredProposalRow(proposal.TypeGoal, mustMarshal(t, map[string]any{"title": "unwired-goal", "area": "career"}))
	store := newFakeProposalStore(row)

	rec := postConfirm(unwiredHandler(store), row.ID, acceptBody)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 (legacy resolve-only), got %d: %s", rec.Code, rec.Body.String())
	}
	if len(store.resolved) != 1 || store.resolved[0] != row.ID {
		t.Errorf("Resolve calls = %v, want exactly [%s]", store.resolved, row.ID)
	}
	if store.byID[row.ID].Status != proposalAccepted {
		t.Errorf("proposal status = %q, want %q", store.byID[row.ID].Status, proposalAccepted)
	}
}
