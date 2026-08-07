package sqlite_test

// ---------------------------------------------------------------------------
// PR #152 round 8, 80cf80b6 finding 3: SQLite counterpart of
// internal/outcome/finalize_draft_workspace_test.go — GetLatestForEntity is
// workspace-scoped (outcome.go:458-470, clause at :468) but FinalizeDraft
// historically was not — the SELECT keyed only on `WHERE id = ?1 AND
// result = 'unknown'` and the UPDATE only on `WHERE id = ?7 AND
// result = 'unknown'` (before this fix), so a draft belonging to one
// workspace could be finalized in place by a caller supplying a different
// (or no) workspace's params.WorkspaceID.
// ---------------------------------------------------------------------------

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// TestSQLiteOutcomeStore_FinalizeDraft_CrossWorkspace_NotFound is the direct
// reproduction: a draft created under workspace A must not be finalizable
// by a call whose params.WorkspaceID is workspace B.
func TestSQLiteOutcomeStore_FinalizeDraft_CrossWorkspace_NotFound(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsA := uuid.New()
	wsB := uuid.New()

	draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsA, EntityType: "task", EntityID: uuid.New(), Result: "unknown",
	})
	if err != nil {
		t.Fatalf("CreateOutcome draft: %v", err)
	}

	_, err = store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		WorkspaceID: &wsB,
		Result:      outcomeResultSuccess,
		Notes:       "attacker-supplied write from a different workspace",
	})
	if !errors.Is(err, outcome.ErrDraftAlreadyFinalized) {
		t.Fatalf("cross-workspace FinalizeDraft: got err=%v, want ErrDraftAlreadyFinalized (no-op/not-found)", err)
	}

	got, err := store.GetOutcomeByID(ctx, draft.ID, &wsA)
	if err != nil {
		t.Fatalf("GetOutcomeByID: %v", err)
	}
	if got.Result != "unknown" {
		t.Errorf("draft Result = %q, want unchanged 'unknown' — cross-workspace call must not finalize it", got.Result)
	}
	if got.Notes != "" {
		t.Errorf("draft Notes = %q, want unchanged empty — cross-workspace call must not write its notes", got.Notes)
	}
}

// TestSQLiteOutcomeStore_FinalizeDraft_LegacyNilWorkspace_StillMatchesAnyRow
// pins the preserved-behaviour guarantee: a FinalizeDraft call whose
// params.WorkspaceID is nil (the calling shape every pre-existing
// FinalizeDraft test in outcome_test.go already uses) must keep matching
// the target row regardless of that row's own workspace_id.
func TestSQLiteOutcomeStore_FinalizeDraft_LegacyNilWorkspace_StillMatchesAnyRow(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()

	draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: uuid.New(), Result: "unknown",
	})
	if err != nil {
		t.Fatalf("CreateOutcome draft: %v", err)
	}

	finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		// WorkspaceID deliberately left nil — legacy single-tenant calling shape.
		Result: outcomeResultSuccess,
		Notes:  "legacy caller, no workspace scoping",
	})
	if err != nil {
		t.Fatalf("FinalizeDraft with nil WorkspaceID against a workspace-scoped row: %v", err)
	}
	if finalized.Result != outcomeResultSuccess {
		t.Errorf("Result = %q, want success", finalized.Result)
	}
	if finalized.Notes != "legacy caller, no workspace scoping" {
		t.Errorf("Notes = %q, want the call's own notes to have been written", finalized.Notes)
	}
}
