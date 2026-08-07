//go:build integration

package outcome_test

// ---------------------------------------------------------------------------
// PR #152 round 8, 80cf80b6 finding 3: GetLatestForEntity is workspace-scoped
// (store.go:418-424, clause at :421-422) but FinalizeDraft historically was
// not — PG's UPDATE keyed only on `WHERE id = $6 AND result = 'unknown'`
// (store.go:760 before this fix), so a draft belonging to one workspace
// could be finalized in place by a caller supplying a DIFFERENT (or no)
// workspace's params.WorkspaceID. The tests below pin the fix on the
// Postgres backend: a mismatched non-nil WorkspaceID must be a no-op/
// not-found (ErrDraftAlreadyFinalized), and the pre-existing legacy
// nil-WorkspaceID calling shape (every other FinalizeDraft test in
// store_test.go) must keep matching any row exactly as before.
// ---------------------------------------------------------------------------

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/google/uuid"
)

// TestStore_FinalizeDraft_CrossWorkspace_NotFound is the direct
// reproduction: a draft created under workspace A must not be finalizable
// by a call whose params.WorkspaceID is workspace B — the row must remain
// completely untouched and the call must report ErrDraftAlreadyFinalized
// (the same "row didn't satisfy my WHERE clause" signal already used for a
// concurrently-finalized row).
func TestStore_FinalizeDraft_CrossWorkspace_NotFound(t *testing.T) {
	pool := openTestPgPool(t)
	wsA := uuid.New()
	wsB := uuid.New()
	store := outcome.NewStore(pool, &wsA)
	ctx := context.Background()

	draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsA, EntityType: "task", EntityID: uuid.New(), Result: "unknown",
	})
	if err != nil {
		t.Fatalf("CreateOutcome draft: %v", err)
	}

	_, err = store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		WorkspaceID: &wsB,
		Result:      "success",
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

// TestStore_FinalizeDraft_LegacyNilWorkspace_StillMatchesAnyRow pins the
// preserved-behaviour guarantee explicitly: a FinalizeDraft call whose
// params.WorkspaceID is nil (the calling shape every pre-existing
// FinalizeDraft test in store_test.go already uses) must keep matching the
// target row regardless of that row's own workspace_id — the new predicate
// only excludes an EXPLICIT workspace mismatch, never an absent one.
func TestStore_FinalizeDraft_LegacyNilWorkspace_StillMatchesAnyRow(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: uuid.New(), Result: "unknown",
	})
	if err != nil {
		t.Fatalf("CreateOutcome draft: %v", err)
	}

	finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		// WorkspaceID deliberately left nil — legacy single-tenant calling shape.
		Result: "success",
		Notes:  "legacy caller, no workspace scoping",
	})
	if err != nil {
		t.Fatalf("FinalizeDraft with nil WorkspaceID against a workspace-scoped row: %v", err)
	}
	if finalized.Result != "success" {
		t.Errorf("Result = %q, want success", finalized.Result)
	}
	if finalized.Notes != "legacy caller, no workspace scoping" {
		t.Errorf("Notes = %q, want the call's own notes to have been written", finalized.Notes)
	}
}
