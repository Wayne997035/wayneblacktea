package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// outcomeResultSuccess is the terminal result these tests assert on. Declared
// here rather than repeated inline so the value stays in one place.
const outcomeResultSuccess = "success"

func openOutcomeDB(t *testing.T) *wbtsqlite.DB {
	t.Helper()
	db, err := wbtsqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestApplyColumnUpgrades_Idempotent verifies MAJOR-4: calling Open() on an
// existing SQLite database (which already has related_rule_ids from schema.sql)
// succeeds without "duplicate column name" errors. This is the key invariant of
// applyColumnUpgrades — it must be safe on both fresh and pre-existing DBs.
func TestApplyColumnUpgrades_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "upgrade-test.db")
	ctx := context.Background()

	// First open: creates DB with schema.sql (includes related_rule_ids).
	db1, err := wbtsqlite.Open(ctx, "file:"+dbPath, "")
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	_ = db1.Close()

	// Second open: applyColumnUpgrades runs again — must not fail with
	// "duplicate column name: related_rule_ids".
	db2, err := wbtsqlite.Open(ctx, "file:"+dbPath, "")
	if err != nil {
		t.Fatalf("second Open (idempotent upgrade): %v", err)
	}
	defer func() { _ = db2.Close() }()

	// Confirm the column exists and is usable (insert + select round-trip).
	store := wbtsqlite.NewOutcomeStore(db2)
	wsID := uuid.New()
	rule1 := uuid.New()
	o, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID:    &wsID,
		EntityType:     "task",
		EntityID:       uuid.New(),
		Result:         outcomeResultSuccess,
		RelatedRuleIDs: []uuid.UUID{rule1},
	})
	if err != nil {
		t.Fatalf("CreateOutcome after idempotent open: %v", err)
	}
	if len(o.RelatedRuleIDs) != 1 || o.RelatedRuleIDs[0] != rule1 {
		t.Errorf("RelatedRuleIDs round-trip failed: got %v, want [%s]", o.RelatedRuleIDs, rule1)
	}
}

// TestSQLiteOutcomeStore_CreateOutcome verifies CreateOutcome happy paths and
// edge cases.
func TestSQLiteOutcomeStore_CreateOutcome(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()

	entityID := uuid.New()
	wsID := uuid.New()

	tests := []struct {
		name    string
		params  outcome.CreateOutcomeParams
		wantErr bool
	}{
		{
			name: "happy path with all fields",
			params: outcome.CreateOutcomeParams{
				WorkspaceID: &wsID,
				EntityType:  "task",
				EntityID:    entityID,
				Result:      outcomeResultSuccess,
				Metrics:     []byte(`{"duration_ms":500}`),
				Notes:       "completed on time",
			},
		},
		{
			name: "no metrics or notes",
			params: outcome.CreateOutcomeParams{
				WorkspaceID: &wsID,
				EntityType:  "decision",
				EntityID:    entityID,
				Result:      "failure",
			},
		},
		{
			name: "regressed result",
			params: outcome.CreateOutcomeParams{
				WorkspaceID: &wsID,
				EntityType:  "sprint",
				EntityID:    entityID,
				Result:      "regressed",
				Notes:       "velocity dropped",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o, err := store.CreateOutcome(ctx, tc.params)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateOutcome: %v", err)
			}
			if o.ID == uuid.Nil {
				t.Error("expected non-nil ID")
			}
			if o.EntityType != tc.params.EntityType {
				t.Errorf("entity_type: got %q, want %q", o.EntityType, tc.params.EntityType)
			}
			if o.Result != tc.params.Result {
				t.Errorf("result: got %q, want %q", o.Result, tc.params.Result)
			}
		})
	}
}

// TestSQLiteOutcomeStore_WorkSessionID mirrors
// TestStore_CreateOutcome_WorkSessionID (Postgres) — verifies work_session_id
// (wbt-2.0 P2.4) round-trips through CreateOutcome + GetOutcomeByID, and that
// omitting it (nil) is a pure regression.
func TestSQLiteOutcomeStore_WorkSessionID(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()

	sessionID := uuid.New()
	o, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID:   &wsID,
		EntityType:    "task",
		EntityID:      uuid.New(),
		Result:        outcomeResultSuccess,
		WorkSessionID: &sessionID,
	})
	if err != nil {
		t.Fatalf("CreateOutcome: %v", err)
	}
	if o.WorkSessionID == nil || *o.WorkSessionID != sessionID {
		t.Errorf("WorkSessionID: got %v, want %s", o.WorkSessionID, sessionID)
	}

	got, err := store.GetOutcomeByID(ctx, o.ID, &wsID)
	if err != nil {
		t.Fatalf("GetOutcomeByID: %v", err)
	}
	if got.WorkSessionID == nil || *got.WorkSessionID != sessionID {
		t.Errorf("reread WorkSessionID: got %v, want %s", got.WorkSessionID, sessionID)
	}

	// Regression: omitting WorkSessionID (nil) must not error and must stay nil.
	noSession, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID,
		EntityType:  "task",
		EntityID:    uuid.New(),
		Result:      outcomeResultSuccess,
	})
	if err != nil {
		t.Fatalf("CreateOutcome without WorkSessionID: %v", err)
	}
	if noSession.WorkSessionID != nil {
		t.Errorf("expected nil WorkSessionID when omitted, got %v", noSession.WorkSessionID)
	}
}

// TestSQLiteOutcomeStore_GetOutcomeByID verifies found, not-found, and
// wrong-workspace cases.
func TestSQLiteOutcomeStore_GetOutcomeByID(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()

	wsID := uuid.New()
	entityID := uuid.New()
	created, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID,
		EntityType:  "project",
		EntityID:    entityID,
		Result:      "partial",
		Notes:       "half done",
	})
	if err != nil {
		t.Fatalf("CreateOutcome: %v", err)
	}

	t.Run("found", func(t *testing.T) {
		got, err := store.GetOutcomeByID(ctx, created.ID, &wsID)
		if err != nil {
			t.Fatalf("GetOutcomeByID: %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("id mismatch: got %s, want %s", got.ID, created.ID)
		}
		if got.Notes != created.Notes {
			t.Errorf("notes mismatch: got %q, want %q", got.Notes, created.Notes)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		_, err := store.GetOutcomeByID(ctx, uuid.New(), &wsID)
		if err == nil {
			t.Fatal("expected ErrNotFound, got nil")
		}
	})

	t.Run("wrong_workspace", func(t *testing.T) {
		other := uuid.New()
		_, err := store.GetOutcomeByID(ctx, created.ID, &other)
		if err == nil {
			t.Fatal("expected ErrNotFound for wrong workspace, got nil")
		}
	})
}

// TestSQLiteOutcomeStore_CreateAndListEvaluation verifies evaluation round-trip
// and workspace-scoped list.
func TestSQLiteOutcomeStore_CreateAndListEvaluation(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()

	wsID := uuid.New()
	entityID := uuid.New()
	o, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID,
		EntityType:  "task",
		EntityID:    entityID,
		Result:      "failure",
	})
	if err != nil {
		t.Fatalf("CreateOutcome: %v", err)
	}

	t.Run("create_and_list", func(t *testing.T) {
		eval, err := store.CreateEvaluation(ctx, outcome.CreateEvaluationParams{
			WorkspaceID:            &wsID,
			OutcomeID:              o.ID,
			Analysis:               "missing requirements",
			Lessons:                []byte(`["document requirements first"]`),
			ImprovementSuggestions: []byte(`["add spec review step"]`),
		})
		if err != nil {
			t.Fatalf("CreateEvaluation: %v", err)
		}
		if eval.ID == uuid.Nil {
			t.Error("expected non-nil evaluation ID")
		}
		if eval.OutcomeID != o.ID {
			t.Errorf("outcome_id mismatch: got %s, want %s", eval.OutcomeID, o.ID)
		}

		evals, err := store.ListEvaluationsByOutcomeID(ctx, o.ID, &wsID)
		if err != nil {
			t.Fatalf("ListEvaluationsByOutcomeID: %v", err)
		}
		if len(evals) != 1 {
			t.Fatalf("expected 1 evaluation, got %d", len(evals))
		}
	})

	t.Run("nil_lessons_defaults_to_empty_array", func(t *testing.T) {
		eval2, err := store.CreateEvaluation(ctx, outcome.CreateEvaluationParams{
			WorkspaceID: &wsID,
			OutcomeID:   o.ID,
			Analysis:    "minimal eval",
		})
		if err != nil {
			t.Fatalf("CreateEvaluation nil lessons: %v", err)
		}
		if string(eval2.Lessons) != "[]" {
			t.Errorf("expected lessons '[]', got %q", eval2.Lessons)
		}
	})

	t.Run("wrong_workspace_returns_empty", func(t *testing.T) {
		other := uuid.New()
		evals, err := store.ListEvaluationsByOutcomeID(ctx, o.ID, &other)
		if err != nil {
			t.Fatalf("ListEvaluationsByOutcomeID: %v", err)
		}
		if len(evals) != 0 {
			t.Errorf("expected 0 evaluations, got %d", len(evals))
		}
	})
}

// TestSQLiteOutcomeStore_ListFailedOutcomes verifies that only failure/regressed
// results are returned.
func TestSQLiteOutcomeStore_ListFailedOutcomes(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()

	wsID := uuid.New()
	entityID := uuid.New()

	for _, result := range []string{outcomeResultSuccess, "failure", "regressed", "partial"} {
		_, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
			WorkspaceID: &wsID,
			EntityType:  "task",
			EntityID:    entityID,
			Result:      result,
		})
		if err != nil {
			t.Fatalf("CreateOutcome(%s): %v", result, err)
		}
	}

	got, err := store.ListFailedOutcomes(ctx, &wsID, 10)
	if err != nil {
		t.Fatalf("ListFailedOutcomes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 failed outcomes (failure+regressed), got %d", len(got))
	}
	for _, o := range got {
		if o.Result != "failure" && o.Result != "regressed" {
			t.Errorf("unexpected result %q in failed outcomes", o.Result)
		}
	}
}

// TestSQLiteOutcomeStore_PruneOlderThan verifies that old rows are deleted and
// recent rows are preserved.
func TestSQLiteOutcomeStore_PruneOlderThan(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()

	wsID := uuid.New()
	entityID := uuid.New()

	recent, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID,
		EntityType:  "task",
		EntityID:    entityID,
		Result:      outcomeResultSuccess,
		Notes:       "recent outcome",
	})
	if err != nil {
		t.Fatalf("CreateOutcome: %v", err)
	}

	// Prune with past cutoff — nothing should be deleted.
	n, err := store.PruneOlderThan(ctx, time.Now().Add(-365*24*time.Hour))
	if err != nil {
		t.Fatalf("PruneOlderThan (past cutoff): %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 pruned rows, got %d", n)
	}

	// Verify still exists.
	_, err = store.GetOutcomeByID(ctx, recent.ID, &wsID)
	if err != nil {
		t.Fatalf("outcome should still exist after past-cutoff prune: %v", err)
	}

	// Prune with future cutoff — recent row should be deleted.
	n, err = store.PruneOlderThan(ctx, time.Now().Add(1*time.Minute))
	if err != nil {
		t.Fatalf("PruneOlderThan (future cutoff): %v", err)
	}
	if n == 0 {
		t.Error("expected at least 1 pruned row")
	}

	// Verify gone.
	_, err = store.GetOutcomeByID(ctx, recent.ID, &wsID)
	if err == nil {
		t.Error("expected ErrNotFound after prune, got nil")
	}
}

// TestSQLiteOutcomeStore_ExistsForEntity verifies the existence check used by
// complete_task's idempotent draft-outcome seeding: false before any outcome
// exists, true after one is created, workspace-scoped false for a different
// workspace, and unscoped (nil workspaceID) true regardless of workspace.
func TestSQLiteOutcomeStore_ExistsForEntity(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()

	wsID := uuid.New()
	entityID := uuid.New()

	t.Run("false before any outcome exists", func(t *testing.T) {
		exists, err := store.ExistsForEntity(ctx, &wsID, "task", entityID)
		if err != nil {
			t.Fatalf("ExistsForEntity: %v", err)
		}
		if exists {
			t.Error("expected false before any outcome is created")
		}
	})

	if _, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID,
		EntityType:  "task",
		EntityID:    entityID,
		Result:      "unknown",
	}); err != nil {
		t.Fatalf("CreateOutcome: %v", err)
	}

	t.Run("true after outcome created, same workspace", func(t *testing.T) {
		exists, err := store.ExistsForEntity(ctx, &wsID, "task", entityID)
		if err != nil {
			t.Fatalf("ExistsForEntity: %v", err)
		}
		if !exists {
			t.Error("expected true after outcome is created for the same workspace")
		}
	})

	t.Run("false for a different entity_type", func(t *testing.T) {
		exists, err := store.ExistsForEntity(ctx, &wsID, "decision", entityID)
		if err != nil {
			t.Fatalf("ExistsForEntity: %v", err)
		}
		if exists {
			t.Error("expected false for entity_type=decision when only a task outcome exists")
		}
	})

	t.Run("false for a different workspace", func(t *testing.T) {
		other := uuid.New()
		exists, err := store.ExistsForEntity(ctx, &other, "task", entityID)
		if err != nil {
			t.Fatalf("ExistsForEntity: %v", err)
		}
		if exists {
			t.Error("expected false when scoped to a different workspace")
		}
	})

	t.Run("true when unscoped (nil workspaceID)", func(t *testing.T) {
		exists, err := store.ExistsForEntity(ctx, nil, "task", entityID)
		if err != nil {
			t.Fatalf("ExistsForEntity: %v", err)
		}
		if !exists {
			t.Error("expected true when workspaceID is nil (unscoped)")
		}
	})
}

// TestSQLiteOutcomeStore_RelatedRuleIDs verifies JSON round-trip for
// related_rule_ids (empty and populated cases — migration 000063).
func TestSQLiteOutcomeStore_RelatedRuleIDs(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()

	wsID := uuid.New()
	entityID := uuid.New()

	t.Run("empty related_rule_ids round-trips as empty slice", func(t *testing.T) {
		o, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
			WorkspaceID:    &wsID,
			EntityType:     "task",
			EntityID:       entityID,
			Result:         outcomeResultSuccess,
			RelatedRuleIDs: []uuid.UUID{},
		})
		if err != nil {
			t.Fatalf("CreateOutcome: %v", err)
		}
		if o.RelatedRuleIDs == nil {
			t.Error("RelatedRuleIDs should not be nil (empty slice expected)")
		}
		if len(o.RelatedRuleIDs) != 0 {
			t.Errorf("expected 0 related rule IDs, got %d", len(o.RelatedRuleIDs))
		}
	})

	t.Run("populated related_rule_ids round-trips correctly", func(t *testing.T) {
		rule1, rule2 := uuid.New(), uuid.New()
		o, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
			WorkspaceID:    &wsID,
			EntityType:     "decision",
			EntityID:       entityID,
			Result:         "failure",
			RelatedRuleIDs: []uuid.UUID{rule1, rule2},
		})
		if err != nil {
			t.Fatalf("CreateOutcome with rule IDs: %v", err)
		}
		if len(o.RelatedRuleIDs) != 2 {
			t.Fatalf("expected 2 related rule IDs, got %d", len(o.RelatedRuleIDs))
		}
		// Verify both IDs are preserved (order-independent).
		got := make(map[uuid.UUID]bool, len(o.RelatedRuleIDs))
		for _, id := range o.RelatedRuleIDs {
			got[id] = true
		}
		if !got[rule1] {
			t.Errorf("rule1 %v not found in RelatedRuleIDs", rule1)
		}
		if !got[rule2] {
			t.Errorf("rule2 %v not found in RelatedRuleIDs", rule2)
		}
	})

	t.Run("nil related_rule_ids treated as empty", func(t *testing.T) {
		o, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
			WorkspaceID:    &wsID,
			EntityType:     "sprint",
			EntityID:       entityID,
			Result:         "partial",
			RelatedRuleIDs: nil,
		})
		if err != nil {
			t.Fatalf("CreateOutcome nil rule IDs: %v", err)
		}
		if len(o.RelatedRuleIDs) != 0 {
			t.Errorf("expected 0 related rule IDs for nil input, got %d", len(o.RelatedRuleIDs))
		}
	})
}

// ---------------------------------------------------------------------------
// GetLatestForEntity / FinalizeDraft / SeedDraft (migration 000074, decision
// 80c1e8ae — outcome lifecycle convergence, arch-r2 A13)
// ---------------------------------------------------------------------------

// TestSQLiteOutcomeStore_GetLatestForEntity verifies not-found, found (most
// recent of several), and workspace-scoping behaviour.
func TestSQLiteOutcomeStore_GetLatestForEntity(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()

	t.Run("not_found_before_any_outcome", func(t *testing.T) {
		_, err := store.GetLatestForEntity(ctx, &wsID, "task", entityID)
		if !errors.Is(err, outcome.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	first, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "unknown",
	})
	if err != nil {
		t.Fatalf("CreateOutcome first: %v", err)
	}
	time.Sleep(2 * time.Millisecond) // ensure created_at strictly advances
	second, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: outcomeResultSuccess,
		SupersedesID: &first.ID,
	})
	if err != nil {
		t.Fatalf("CreateOutcome second: %v", err)
	}

	t.Run("returns_most_recent", func(t *testing.T) {
		got, err := store.GetLatestForEntity(ctx, &wsID, "task", entityID)
		if err != nil {
			t.Fatalf("GetLatestForEntity: %v", err)
		}
		if got.ID != second.ID {
			t.Errorf("expected latest ID %s, got %s", second.ID, got.ID)
		}
		if got.SupersedesID == nil || *got.SupersedesID != first.ID {
			t.Errorf("expected SupersedesID %s, got %v", first.ID, got.SupersedesID)
		}
	})

	t.Run("wrong_workspace_not_found", func(t *testing.T) {
		other := uuid.New()
		_, err := store.GetLatestForEntity(ctx, &other, "task", entityID)
		if !errors.Is(err, outcome.ErrNotFound) {
			t.Fatalf("expected ErrNotFound for wrong workspace, got %v", err)
		}
	})
}

// TestSQLiteOutcomeStore_FinalizeDraft_HappyPath verifies a draft transitions
// to a terminal result IN PLACE — same ID, no second row.
func TestSQLiteOutcomeStore_FinalizeDraft_HappyPath(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()

	draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "unknown",
	})
	if err != nil {
		t.Fatalf("CreateOutcome draft: %v", err)
	}

	finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		Result: outcomeResultSuccess,
		Notes:  "shipped",
	})
	if err != nil {
		t.Fatalf("FinalizeDraft: %v", err)
	}
	if finalized.ID != draft.ID {
		t.Errorf("FinalizeDraft must reuse the same row ID: got %s, want %s", finalized.ID, draft.ID)
	}
	if finalized.Result != outcomeResultSuccess {
		t.Errorf("Result = %q, want success", finalized.Result)
	}
	if finalized.Notes != "shipped" {
		t.Errorf("Notes = %q, want shipped", finalized.Notes)
	}

	all, err := store.ListRecentOutcomes(ctx, &wsID, "task", 10)
	if err != nil {
		t.Fatalf("ListRecentOutcomes: %v", err)
	}
	count := 0
	for _, o := range all {
		if o.EntityID == entityID {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 row for the entity after finalize, got %d", count)
	}
}

// TestSQLiteOutcomeStore_FinalizeDraft_AlreadyFinalized verifies the
// WHERE result='unknown' guard: finalizing an already-terminal row returns
// outcome.ErrDraftAlreadyFinalized instead of silently overwriting it.
func TestSQLiteOutcomeStore_FinalizeDraft_AlreadyFinalized(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()

	draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "unknown",
	})
	if err != nil {
		t.Fatalf("CreateOutcome draft: %v", err)
	}
	if _, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{Result: outcomeResultSuccess}); err != nil {
		t.Fatalf("first FinalizeDraft: %v", err)
	}

	_, err = store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{Result: "failure"})
	if !errors.Is(err, outcome.ErrDraftAlreadyFinalized) {
		t.Fatalf("expected ErrDraftAlreadyFinalized, got %v", err)
	}

	// The first finalize's result must be untouched by the rejected second call.
	got, err := store.GetOutcomeByID(ctx, draft.ID, &wsID)
	if err != nil {
		t.Fatalf("GetOutcomeByID: %v", err)
	}
	if got.Result != outcomeResultSuccess {
		t.Errorf("result must remain 'success' (first finalize), got %q — second call must not silently overwrite", got.Result)
	}
}

// TestSQLiteOutcomeStore_FinalizeDraft_MergeSemantics_PreservesExistingFieldsWhenEmpty
// is the SQLite counterpart of the M-1a regression reproduction in
// internal/outcome/store_test.go (PR #152 second round): a draft carrying
// real notes/metrics/related_rule_ids/work_session_id, finalized by a call
// that supplies ONLY entity identity + result, must keep all four existing
// fields — the old blanket UPDATE would have blanked every one of them.
// This also exercises the encodeUUIDSlice("[]" vs nil) trap documented on
// FinalizeDraft: RelatedRuleIDs must survive even though the SQLite column
// is a TEXT-encoded JSON array, not a native array type like PG's uuid[].
func TestSQLiteOutcomeStore_FinalizeDraft_MergeSemantics_PreservesExistingFieldsWhenEmpty(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()
	sessionID := uuid.New()
	ruleID := uuid.New()

	draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID:    &wsID,
		EntityType:     "task",
		EntityID:       entityID,
		Result:         "unknown",
		Notes:          "real postmortem content the attacker wants gone",
		Metrics:        []byte(`{"duration_ms":4200}`),
		RelatedRuleIDs: []uuid.UUID{ruleID},
		WorkSessionID:  &sessionID,
	})
	if err != nil {
		t.Fatalf("CreateOutcome draft: %v", err)
	}

	// The attack: finalize with a terminal result and NOTHING else.
	finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		Result: outcomeResultSuccess,
	})
	if err != nil {
		t.Fatalf("FinalizeDraft: %v", err)
	}
	if finalized.ID != draft.ID {
		t.Errorf("FinalizeDraft must reuse the same row ID: got %s, want %s", finalized.ID, draft.ID)
	}
	if finalized.Result != outcomeResultSuccess {
		t.Errorf("Result = %q, want success", finalized.Result)
	}
	if finalized.Notes != "real postmortem content the attacker wants gone" {
		t.Errorf("Notes erased by empty-param finalize: got %q, want preserved", finalized.Notes)
	}
	if string(finalized.Metrics) != `{"duration_ms":4200}` {
		t.Errorf("Metrics erased by empty-param finalize: got %q, want preserved", finalized.Metrics)
	}
	if len(finalized.RelatedRuleIDs) != 1 || finalized.RelatedRuleIDs[0] != ruleID {
		t.Errorf("RelatedRuleIDs erased by empty-param finalize: got %v, want preserved [%s]", finalized.RelatedRuleIDs, ruleID)
	}
	if finalized.WorkSessionID == nil || *finalized.WorkSessionID != sessionID {
		t.Errorf("WorkSessionID erased by empty-param finalize: got %v, want preserved %s", finalized.WorkSessionID, sessionID)
	}
}

// TestSQLiteOutcomeStore_SeedDraft_CreatesOnce verifies the sequential
// happy path: first call creates, second call is a no-op read returning the
// same row.
func TestSQLiteOutcomeStore_SeedDraft_CreatesOnce(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()

	first, created, err := store.SeedDraft(ctx, &wsID, "task", entityID)
	if err != nil {
		t.Fatalf("SeedDraft first: %v", err)
	}
	if !created {
		t.Error("expected created=true on first SeedDraft call")
	}
	if first.Result != "unknown" {
		t.Errorf("Result = %q, want unknown", first.Result)
	}

	second, created2, err := store.SeedDraft(ctx, &wsID, "task", entityID)
	if err != nil {
		t.Fatalf("SeedDraft second: %v", err)
	}
	if created2 {
		t.Error("expected created=false on second SeedDraft call")
	}
	if second.ID != first.ID {
		t.Errorf("second call must return the same draft row: got %s, want %s", second.ID, first.ID)
	}
}

// TestSQLiteOutcomeStore_SeedDraft_SkipsWhenTerminalOutcomeExists verifies
// the pre-existing ExistsForEntity semantics are preserved: SeedDraft must
// NOT add a redundant unknown draft on top of an already-terminal outcome
// (this is exactly the production duplication bug — 2 entities found with
// both an unknown draft AND a terminal outcome coexisting).
func TestSQLiteOutcomeStore_SeedDraft_SkipsWhenTerminalOutcomeExists(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()

	terminal, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: outcomeResultSuccess,
	})
	if err != nil {
		t.Fatalf("CreateOutcome terminal: %v", err)
	}

	got, created, err := store.SeedDraft(ctx, &wsID, "task", entityID)
	if err != nil {
		t.Fatalf("SeedDraft: %v", err)
	}
	if created {
		t.Error("expected created=false when a terminal outcome already exists")
	}
	if got.ID != terminal.ID {
		t.Errorf("expected the existing terminal row back, got a different ID: %s vs %s", got.ID, terminal.ID)
	}
	if got.Result != outcomeResultSuccess {
		t.Errorf("must not have altered the existing terminal result, got %q", got.Result)
	}
}

// TestSeedDraftOutcome_ConcurrentSeedDraft_NoDuplicateDraft is the direct
// race-safety proof for the TOCTOU the dispatch named explicitly: concurrent
// complete_task calls on the same task racing to seed the first draft.
// GetLatestForEntity and the guarded INSERT are two separate statements
// (not wrapped in one transaction), so even with SQLite's single-connection
// serialization (db.go SetMaxOpenConns(1)) goroutines CAN genuinely
// interleave between the read and the write — each individual statement is
// atomic, but the two-statement sequence as a whole is not. The correctness
// guarantee comes from the partial unique index idx_outcomes_one_open_draft
// rejecting every INSERT past the first for the same entity via
// ON CONFLICT DO NOTHING, not from any application-level lock.
func TestSeedDraftOutcome_ConcurrentSeedDraft_NoDuplicateDraft(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()

	const n = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	createdCount := make([]bool, n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start // release all goroutines together to maximize interleaving
			_, created, err := store.SeedDraft(ctx, &wsID, "task", entityID)
			createdCount[idx] = created
			errs[idx] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d SeedDraft error: %v", i, err)
		}
	}

	trueCount := 0
	for _, c := range createdCount {
		if c {
			trueCount++
		}
	}
	if trueCount != 1 {
		t.Errorf("expected exactly 1 goroutine to win created=true, got %d", trueCount)
	}

	all, err := store.ListRecentOutcomes(ctx, &wsID, "task", 100)
	if err != nil {
		t.Fatalf("ListRecentOutcomes: %v", err)
	}
	rowCount := 0
	for _, o := range all {
		if o.EntityID == entityID {
			rowCount++
		}
	}
	if rowCount != 1 {
		t.Errorf("expected exactly 1 outcome row for the entity after %d concurrent SeedDraft calls, got %d — TOCTOU not closed", n, rowCount)
	}
}
