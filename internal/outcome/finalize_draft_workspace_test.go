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
// not-found (ErrDraftAlreadyFinalized) — this is the guard's real coverage,
// see TestStore_FinalizeDraft_CrossWorkspace_NotFound's doc comment — and
// the pre-existing legacy nil-WorkspaceID calling shape (every other
// FinalizeDraft test in store_test.go) must keep matching any row exactly
// as before, which is a separate, guard-independent guarantee (see
// TestStore_FinalizeDraft_NilWorkspaceParam_LegacyCallingShapeUnaffected's
// own scope note).
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

// TestStore_FinalizeDraft_NilWorkspaceParam_LegacyCallingShapeUnaffected pins
// the preserved-behaviour guarantee explicitly: a FinalizeDraft call whose
// params.WorkspaceID is nil (the calling shape every pre-existing
// FinalizeDraft test in store_test.go already uses) must keep matching the
// target row regardless of that row's own workspace_id — the new predicate
// only excludes an EXPLICIT workspace mismatch, never an absent one.
//
// Scope note (round 9, coverage audit): this test does NOT exercise the
// guard's actual comparison arm — with params.WorkspaceID == nil, the
// predicate's first disjunct (`$9::uuid IS NULL`) is unconditionally true,
// so the whole `($9::uuid IS NULL OR workspace_id = $9)` clause holds
// identically whether or not the `workspace_id = $9` arm — or the guard as a
// whole — exists at all. Mutation-proven: replacing the predicate with
// `(1=1 OR $9::uuid IS NULL OR workspace_id = $9)` (i.e. deleting the guard)
// leaves this test green. This test's real job is guarding the LEGACY
// nil-workspace calling path specifically (so a future change to the nil
// branch doesn't regress every pre-existing single-tenant caller) — it is
// NOT coverage for the workspace-scoping guard itself. That guard's actual
// enforcement (the `workspace_id = $9` arm, evaluated with a real non-nil
// $9) is exercised and mutation-proven by
// TestStore_FinalizeDraft_CrossWorkspace_NotFound above, which supplies a
// non-nil, MISMATCHED WorkspaceID and — same mutation applied — correctly
// goes red (verified by re-running both tests against the `1=1 OR` variant:
// only CrossWorkspace_NotFound fails).
func TestStore_FinalizeDraft_NilWorkspaceParam_LegacyCallingShapeUnaffected(t *testing.T) {
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
