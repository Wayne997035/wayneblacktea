package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
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

// insertOutcomeWithIDAndCreatedAt inserts an outcomes row with an explicit
// id, result, and created_at, bypassing CreateOutcome (which always assigns
// its own timestamp) so tests can construct exact created_at ties. Uses
// DB.ExecContext (exported for exactly this kind of fixture insert — see its
// doc comment) rather than production write paths.
func insertOutcomeWithIDAndCreatedAt(
	t *testing.T, db *wbtsqlite.DB, id, wsID uuid.UUID, entityType string, entityID uuid.UUID, result, createdAt string,
) {
	t.Helper()
	err := db.ExecContext(
		context.Background(),
		`INSERT INTO outcomes (id, workspace_id, entity_type, entity_id, result, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
		id.String(), wsID.String(), entityType, entityID.String(), result, createdAt,
	)
	if err != nil {
		t.Fatalf("insert outcome with explicit id and created_at: %v", err)
	}
}

// TestSQLiteOutcomeStore_GetLatestForEntity_CreatedAtTieBreak is the SQLite
// twin of TestStore_GetLatestForEntity_CreatedAtTieBreak (internal/outcome/
// store_test.go) — see its doc comment for the full failure chain this is a
// regression test for. The expected winner (idGreater) is derived from the
// ORDER BY created_at DESC, id DESC contract itself, matching the tie-break
// rule migrations/sqlite/000074_outcomes_supersession.up.sql's dedup step
// uses (TestMigration000074_Dedup_SQLite_CreatedAtTieBreak) — not from
// running the query once and recording what it happened to return.
func TestSQLiteOutcomeStore_GetLatestForEntity_CreatedAtTieBreak(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()
	const sameCreatedAt = "2026-01-01T00:00:00.000Z"

	idA, idB := uuid.New(), uuid.New()
	idLesser, idGreater := idA, idB
	if idLesser.String() > idGreater.String() {
		idLesser, idGreater = idGreater, idLesser
	}

	// Mirrors the actual repro shape (one terminal row, one draft) — but the
	// tie-break must hold regardless of which result value lands on which id.
	insertOutcomeWithIDAndCreatedAt(t, db, idLesser, wsID, "task", entityID, outcomeResultSuccess, sameCreatedAt)
	insertOutcomeWithIDAndCreatedAt(t, db, idGreater, wsID, "task", entityID, "unknown", sameCreatedAt)

	got, err := store.GetLatestForEntity(ctx, &wsID, "task", entityID)
	if err != nil {
		t.Fatalf("GetLatestForEntity: %v", err)
	}
	if got.ID != idGreater {
		t.Errorf("expected tie-break winner (greater id) %s, got %s — "+
			"created_at tie must resolve via ORDER BY created_at DESC, id DESC",
			idGreater, got.ID)
	}
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

// TestSQLiteOutcomeStore_FinalizeDraft_RelatedRuleIDs_UnionsNotReplaces is
// the SQLite twin of outcome package's TestStore_FinalizeDraft_
// RelatedRuleIDs_UnionsNotReplaces (append-semantics redesign — see that
// test's comment for why the direction flipped from the old "replace, not
// union" contract PR #152 round 3 Minor 3 originally pinned). A draft seeded
// with ruleA, then enriched with ruleB, must end up with EXACTLY
// [ruleA, ruleB] — existing first, new appended, no duplicates.
func TestSQLiteOutcomeStore_FinalizeDraft_RelatedRuleIDs_UnionsNotReplaces(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()
	ruleA, ruleB := uuid.New(), uuid.New()

	draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID:    &wsID,
		EntityType:     "task",
		EntityID:       entityID,
		Result:         "unknown",
		RelatedRuleIDs: []uuid.UUID{ruleA},
	})
	if err != nil {
		t.Fatalf("CreateOutcome draft with ruleA: %v", err)
	}
	if len(draft.RelatedRuleIDs) != 1 || draft.RelatedRuleIDs[0] != ruleA {
		t.Fatalf("precondition failed: draft.RelatedRuleIDs = %v, want [%s]", draft.RelatedRuleIDs, ruleA)
	}

	// Enrich (Result stays "unknown", so the row is still eligible for a
	// SECOND FinalizeDraft call afterward) supplying ONLY the new ruleB. If
	// this were a whole-array REPLACE (the pre-redesign contract), the
	// result would become EXACTLY [ruleB], losing ruleA — that's precisely
	// what distinguishes union from replace here; a test that resupplied
	// ruleA alongside ruleB would not.
	enriched, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		Result:         "unknown",
		RelatedRuleIDs: []uuid.UUID{ruleB},
	})
	if err != nil {
		t.Fatalf("FinalizeDraft (enrich) with ruleB: %v", err)
	}
	if len(enriched.RelatedRuleIDs) != 2 || enriched.RelatedRuleIDs[0] != ruleA || enriched.RelatedRuleIDs[1] != ruleB {
		t.Fatalf("RelatedRuleIDs = %v, want EXACTLY [%s, %s] (union: existing ruleA preserved, ruleB appended)",
			enriched.RelatedRuleIDs, ruleA, ruleB)
	}

	// A follow-up finalize resupplying the now-already-present ruleA must
	// not duplicate it.
	finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		Result:         outcomeResultSuccess,
		RelatedRuleIDs: []uuid.UUID{ruleA},
	})
	if err != nil {
		t.Fatalf("FinalizeDraft (finalize) resupplying ruleA: %v", err)
	}
	if len(finalized.RelatedRuleIDs) != 2 || finalized.RelatedRuleIDs[0] != ruleA || finalized.RelatedRuleIDs[1] != ruleB {
		t.Errorf("RelatedRuleIDs = %v, want still EXACTLY [%s, %s] (no duplicate of ruleA)",
			finalized.RelatedRuleIDs, ruleA, ruleB)
	}
}

// TestSQLiteOutcomeStore_FinalizeDraft_AppendSemantics_NotesNeverRemoved is
// the SQLite twin of the direct M-1/threat-model reproduction: a call
// carrying near-empty notes (a single space) against a draft with real
// postmortem content must not remove that content — it can only be appended
// after it.
func TestSQLiteOutcomeStore_FinalizeDraft_AppendSemantics_NotesNeverRemoved(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()
	const realPostmortem = "real postmortem: root cause was a missing index"

	draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "unknown",
		Notes: realPostmortem,
	})
	if err != nil {
		t.Fatalf("CreateOutcome draft: %v", err)
	}

	finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		Result: outcomeResultSuccess,
		Notes:  " ", // the attack: a single space
	})
	if err != nil {
		t.Fatalf("FinalizeDraft: %v", err)
	}
	if !strings.Contains(finalized.Notes, realPostmortem) {
		t.Fatalf("real postmortem content was REMOVED — got Notes = %q, want it to still contain %q",
			finalized.Notes, realPostmortem)
	}
	if finalized.Notes != realPostmortem+"\n\n " {
		t.Errorf("Notes = %q, want exactly %q (existing + separator + attack text appended)",
			finalized.Notes, realPostmortem+"\n\n ")
	}
}

// TestSQLiteOutcomeStore_FinalizeDraft_AppendSemantics_MetricsOnlyAddsNewKeys
// verifies the SQLite Go-side JSON merge: an existing key's value can never
// be overwritten, but a genuinely new key is still admitted.
func TestSQLiteOutcomeStore_FinalizeDraft_AppendSemantics_MetricsOnlyAddsNewKeys(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()

	draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "unknown",
		Metrics: []byte(`{"a":9,"b":2}`),
	})
	if err != nil {
		t.Fatalf("CreateOutcome draft: %v", err)
	}

	finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		Result:  outcomeResultSuccess,
		Metrics: []byte(`{"a":1,"c":5}`),
	})
	if err != nil {
		t.Fatalf("FinalizeDraft: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(finalized.Metrics, &got); err != nil {
		t.Fatalf("Metrics is not valid JSON (%q): %v", finalized.Metrics, err)
	}
	if v, ok := got["a"]; !ok || v != float64(9) {
		t.Errorf(`Metrics["a"] = %v, want 9 (existing value must survive, new value 1 must be ignored)`, v)
	}
	if v, ok := got["b"]; !ok || v != float64(2) {
		t.Errorf(`Metrics["b"] = %v, want 2 (untouched existing key must survive)`, v)
	}
	if v, ok := got["c"]; !ok || v != float64(5) {
		t.Errorf(`Metrics["c"] = %v, want 5 (genuinely new key must be added)`, v)
	}
	if len(got) != 3 {
		t.Errorf("Metrics has %d keys, want exactly 3 (a, b, c) — got %v", len(got), got)
	}
}

// TestSQLiteOutcomeStore_FinalizeDraft_AppendSemantics_WorkSessionIDSetOnce
// verifies the SQLite twin of the set-once rule, both directions.
func TestSQLiteOutcomeStore_FinalizeDraft_AppendSemantics_WorkSessionIDSetOnce(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()

	t.Run("writes when existing is NULL", func(t *testing.T) {
		entityID := uuid.New()
		sessionA := uuid.New()
		draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
			WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "unknown",
		})
		if err != nil {
			t.Fatalf("CreateOutcome draft: %v", err)
		}
		finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
			Result: outcomeResultSuccess, WorkSessionID: &sessionA,
		})
		if err != nil {
			t.Fatalf("FinalizeDraft: %v", err)
		}
		if finalized.WorkSessionID == nil || *finalized.WorkSessionID != sessionA {
			t.Errorf("WorkSessionID = %v, want %s (first write into a NULL column must succeed)", finalized.WorkSessionID, sessionA)
		}
	})

	t.Run("cannot be re-pointed once set", func(t *testing.T) {
		entityID := uuid.New()
		sessionA, sessionB := uuid.New(), uuid.New()
		draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
			WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "unknown",
			WorkSessionID: &sessionA,
		})
		if err != nil {
			t.Fatalf("CreateOutcome draft: %v", err)
		}
		finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
			Result: outcomeResultSuccess, WorkSessionID: &sessionB,
		})
		if err != nil {
			t.Fatalf("FinalizeDraft: %v", err)
		}
		if finalized.WorkSessionID == nil || *finalized.WorkSessionID != sessionA {
			t.Errorf("WorkSessionID = %v, want unchanged %s (already-set session must never be re-pointed by a later call)",
				finalized.WorkSessionID, sessionA)
		}
	})
}

// TestSQLiteOutcomeStore_FinalizeDraft_UpdatedAt_BumpsOnlyOnRealWrite is the
// SQLite twin of the Store-level updated_at proof.
func TestSQLiteOutcomeStore_FinalizeDraft_UpdatedAt_BumpsOnlyOnRealWrite(t *testing.T) {
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
	if !draft.UpdatedAt.Equal(draft.CreatedAt) {
		t.Errorf("freshly-created draft: UpdatedAt = %v, want equal to CreatedAt %v (never modified yet)", draft.UpdatedAt, draft.CreatedAt)
	}

	time.Sleep(2 * time.Millisecond)
	finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		Result: outcomeResultSuccess, Notes: "shipped",
	})
	if err != nil {
		t.Fatalf("FinalizeDraft: %v", err)
	}
	if !finalized.UpdatedAt.After(draft.CreatedAt) {
		t.Errorf("UpdatedAt = %v, want strictly after CreatedAt %v (a real write must bump it)", finalized.UpdatedAt, draft.CreatedAt)
	}
}

// TestRecordExecutionResult_SQLite_AppendSemantics_ByteIdenticalRetryIsIdempotent_UpdatedAtUnchanged
// is the RecordExecutionResult-level (not just Store-level) proof that a
// no-op path never touches updated_at, on the SQLite backend.
func TestRecordExecutionResult_SQLite_AppendSemantics_ByteIdenticalRetryIsIdempotent_UpdatedAtUnchanged(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()

	first, action, _, err := outcome.RecordExecutionResult(ctx, store, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID,
		Result: "unknown", Notes: "still investigating",
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult (seed with content): %v", err)
	}
	if action != outcome.ActionCreated {
		t.Fatalf("seed action = %q, want created", action)
	}

	time.Sleep(2 * time.Millisecond)
	enriched, action, _, err := outcome.RecordExecutionResult(ctx, store, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID,
		Result: "unknown", Notes: "verified in prod",
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult (enrich): %v", err)
	}
	if action != outcome.ActionDraftEnriched {
		t.Fatalf("enrich action = %q, want draft_enriched", action)
	}
	if enriched.Notes != "still investigating\n\nverified in prod" {
		t.Fatalf("Notes = %q, want the append to have happened", enriched.Notes)
	}
	if !enriched.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("enrich UpdatedAt = %v, want strictly after seed UpdatedAt %v", enriched.UpdatedAt, first.UpdatedAt)
	}

	time.Sleep(2 * time.Millisecond)
	retried, action, _, err := outcome.RecordExecutionResult(ctx, store, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID,
		Result: "unknown", Notes: "verified in prod", // byte-identical retry
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult (retry): %v", err)
	}
	if action != outcome.ActionReplayedIdempotent {
		t.Fatalf("retry action = %q, want replayed_idempotent", action)
	}
	if retried.Notes != enriched.Notes {
		t.Errorf("retry Notes = %q, want unchanged %q (no duplicate append)", retried.Notes, enriched.Notes)
	}
	if !retried.UpdatedAt.Equal(enriched.UpdatedAt) {
		t.Errorf("retry UpdatedAt = %v, want unchanged %v (no-op path must never bump it)", retried.UpdatedAt, enriched.UpdatedAt)
	}
}

// TestRecordExecutionResult_SQLite_AppendSemantics_GenuinelyNewInfoStillWrites
// is the SQLite twin of the mandatory reverse-protection guard: after a
// byte-identical retry is correctly a no-op, a follow-up call carrying
// genuinely new information in EACH append-only field must still write and
// report ActionDraftEnriched.
func TestRecordExecutionResult_SQLite_AppendSemantics_GenuinelyNewInfoStillWrites(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()
	ruleA := uuid.New()

	seeded, action, _, err := outcome.RecordExecutionResult(ctx, store, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID,
		Result: "unknown", Notes: "first note", Metrics: []byte(`{"a":1}`), RelatedRuleIDs: []uuid.UUID{ruleA},
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult (seed): %v", err)
	}
	if action != outcome.ActionCreated {
		t.Fatalf("seed action = %q, want created", action)
	}

	time.Sleep(2 * time.Millisecond) // ensure UpdatedAt strictly advances past the seed's timestamp
	sessionID := uuid.New()
	ruleB := uuid.New()
	enriched, action, _, err := outcome.RecordExecutionResult(ctx, store, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID,
		Result:         "unknown",
		Notes:          "second note",
		Metrics:        []byte(`{"a":9,"b":2}`),
		RelatedRuleIDs: []uuid.UUID{ruleB},
		WorkSessionID:  &sessionID,
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult (enrich with new info in every field): %v", err)
	}
	if action != outcome.ActionDraftEnriched {
		t.Fatalf("action = %q, want draft_enriched — a call with genuinely new content in every field must write (M-2a guard)", action)
	}
	if enriched.Notes != "first note\n\nsecond note" {
		t.Errorf("Notes = %q, want the second note appended", enriched.Notes)
	}
	var gotMetrics map[string]any
	if err := json.Unmarshal(enriched.Metrics, &gotMetrics); err != nil {
		t.Fatalf("Metrics not valid JSON: %v", err)
	}
	if gotMetrics["a"] != float64(1) {
		t.Errorf(`Metrics["a"] = %v, want 1 (existing value must survive the repeated key)`, gotMetrics["a"])
	}
	if gotMetrics["b"] != float64(2) {
		t.Errorf(`Metrics["b"] = %v, want 2 (new key must be written)`, gotMetrics["b"])
	}
	if len(enriched.RelatedRuleIDs) != 2 || enriched.RelatedRuleIDs[0] != ruleA || enriched.RelatedRuleIDs[1] != ruleB {
		t.Errorf("RelatedRuleIDs = %v, want [%s, %s]", enriched.RelatedRuleIDs, ruleA, ruleB)
	}
	if enriched.WorkSessionID == nil || *enriched.WorkSessionID != sessionID {
		t.Errorf("WorkSessionID = %v, want %s (was NULL, must be written)", enriched.WorkSessionID, sessionID)
	}
	if !enriched.UpdatedAt.After(seeded.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want strictly after seed UpdatedAt %v", enriched.UpdatedAt, seeded.UpdatedAt)
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

// ---------------------------------------------------------------------------
// PR #152 round 4 Major (finding F3) + Critical/Major (F1/F6 parity). SQLite
// never had F1's NULL bug: migrations/sqlite/000063_outcomes_rule_link.up.sql
// declares `related_rule_ids TEXT NOT NULL DEFAULT '[]'`, unlike PG's
// nullable UUID[] with no DEFAULT — so this backend structurally cannot
// reproduce F1. F3 (SeedDraft leaving updated_at unset) and F6 (dedup
// coverage) DO apply here and are pinned below for backend-security-
// design.md §6.5 parity.
// ---------------------------------------------------------------------------

// TestSQLiteOutcomeStore_SeedDraft_UpdatedAtEqualsCreatedAt is F3's fix
// reproduction: before this fix, SeedDraft's INSERT omitted updated_at
// entirely, and — because migrations/sqlite/000075's column is nullable at
// the SQLite schema level (ALTER TABLE cannot retroactively add a NOT NULL
// DEFAULT without a full table rebuild) — every seeded draft's updated_at
// scanned back as the Go zero time.Time (0001-01-01), not CreatedAt.
func TestSQLiteOutcomeStore_SeedDraft_UpdatedAtEqualsCreatedAt(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()

	draft, created, err := store.SeedDraft(ctx, &wsID, "task", entityID)
	if err != nil {
		t.Fatalf("SeedDraft: %v", err)
	}
	if !created {
		t.Fatal("expected created=true for a fresh entity")
	}
	if draft.UpdatedAt.IsZero() {
		t.Fatal("seeded draft: UpdatedAt is the zero time — SeedDraft's INSERT did not supply a value")
	}
	if !draft.UpdatedAt.Equal(draft.CreatedAt) {
		t.Errorf("seeded draft: UpdatedAt = %v, want equal to CreatedAt %v (never modified yet)",
			draft.UpdatedAt, draft.CreatedAt)
	}
}

// TestSQLiteOutcomeStore_SeedDraft_FinalizeDraft_RelatedRuleIDs_ProductionPath
// mirrors the PG production-path test (TestStore_SeedDraft_FinalizeDraft_
// RelatedRuleIDs_ProductionPath) for backend-security-design.md §6.5 parity:
// the actual complete_task -> record_outcome call sequence, starting from
// SeedDraft rather than CreateOutcome, must round-trip related_rule_ids
// correctly. SQLite never had F1's bug, but every other FinalizeDraft test
// in this file also happens to seed via CreateOutcome, so this closes the
// same "SeedDraft path specifically" coverage gap the PG twin does.
func TestSQLiteOutcomeStore_SeedDraft_FinalizeDraft_RelatedRuleIDs_ProductionPath(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()
	ruleA, ruleB := uuid.New(), uuid.New()

	draft, created, err := store.SeedDraft(ctx, &wsID, "task", entityID)
	if err != nil {
		t.Fatalf("SeedDraft: %v", err)
	}
	if !created {
		t.Fatal("expected created=true for a fresh entity")
	}
	if len(draft.RelatedRuleIDs) != 0 {
		t.Fatalf("precondition: freshly-seeded draft.RelatedRuleIDs = %v, want empty", draft.RelatedRuleIDs)
	}

	finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		Result:         outcomeResultSuccess,
		RelatedRuleIDs: []uuid.UUID{ruleA, ruleB},
	})
	if err != nil {
		t.Fatalf("FinalizeDraft: %v", err)
	}
	if len(finalized.RelatedRuleIDs) != 2 || finalized.RelatedRuleIDs[0] != ruleA || finalized.RelatedRuleIDs[1] != ruleB {
		t.Fatalf("RelatedRuleIDs = %v, want EXACTLY [%s, %s]", finalized.RelatedRuleIDs, ruleA, ruleB)
	}
}

// TestSQLiteOutcomeStore_FinalizeDraft_RelatedRuleIDs_DedupMatrix is F6's
// SQLite-side half of the combined test matrix (existing empty / existing
// has-value crossed with incoming has-a-duplicate / incoming has-none). No
// "existing NULL" case: unlike PG, this column is `NOT NULL DEFAULT '[]'`
// (migrations/sqlite/000063), so a NULL existing value is not reachable
// through any write path this store exposes. unionRelatedRuleIDs
// (outcome.go) already dedupes both operands via a `seen` map, so every
// case here is expected to pass without a code change — the point is
// closing the zero-coverage gap the dispatch flagged (a no-op dedup
// passthrough left the suite green), mirroring the PG matrix's structure.
func TestSQLiteOutcomeStore_FinalizeDraft_RelatedRuleIDs_DedupMatrix(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()

	ruleX := uuid.New()
	ruleY, ruleZ := uuid.New(), uuid.New()

	tests := []struct {
		name        string
		existingIDs []uuid.UUID
		incoming    []uuid.UUID
		want        []uuid.UUID
	}{
		{
			name:        "existing empty array, incoming has an internal duplicate",
			existingIDs: []uuid.UUID{},
			incoming:    []uuid.UUID{ruleY, ruleY, ruleZ},
			want:        []uuid.UUID{ruleY, ruleZ},
		},
		{
			name:        "existing empty array, incoming has no duplicates",
			existingIDs: []uuid.UUID{},
			incoming:    []uuid.UUID{ruleY, ruleZ},
			want:        []uuid.UUID{ruleY, ruleZ},
		},
		{
			name:        "existing already has a value, incoming has an internal duplicate",
			existingIDs: []uuid.UUID{ruleX},
			incoming:    []uuid.UUID{ruleY, ruleY},
			want:        []uuid.UUID{ruleX, ruleY},
		},
		{
			name:        "existing already has a value, incoming has no duplicates",
			existingIDs: []uuid.UUID{ruleX},
			incoming:    []uuid.UUID{ruleY, ruleZ},
			want:        []uuid.UUID{ruleX, ruleY, ruleZ},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entityID := uuid.New()
			draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
				WorkspaceID:    &wsID,
				EntityType:     "task",
				EntityID:       entityID,
				Result:         "unknown",
				RelatedRuleIDs: tc.existingIDs,
			})
			if err != nil {
				t.Fatalf("CreateOutcome (seed existing state): %v", err)
			}

			finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
				Result:         outcomeResultSuccess,
				RelatedRuleIDs: tc.incoming,
			})
			if err != nil {
				t.Fatalf("FinalizeDraft: %v", err)
			}
			if len(finalized.RelatedRuleIDs) != len(tc.want) {
				t.Fatalf("RelatedRuleIDs = %v, want %v", finalized.RelatedRuleIDs, tc.want)
			}
			for i, id := range tc.want {
				if finalized.RelatedRuleIDs[i] != id {
					t.Errorf("RelatedRuleIDs[%d] = %s, want %s (full: got %v, want %v)",
						i, finalized.RelatedRuleIDs[i], id, finalized.RelatedRuleIDs, tc.want)
				}
			}
		})
	}
}

// TestSQLiteOutcomeStore_FinalizeDraft_RelatedRuleIDs_CumulativeCapTruncates
// is the SQLite twin of
// TestStore_FinalizeDraft_RelatedRuleIDs_CumulativeCapTruncates
// (internal/outcome/store_test.go) — PR #152 round 5 Major (M-R5-1)
// guarantee A. Same scenario, same assertions, against the SQLite backend:
// 6 batches of 20 fresh distinct UUIDs (120 candidates, deliberately past
// the 100 cap) applied via repeated FinalizeDraft calls against the SAME
// draft (Result stays "unknown" throughout) must never let
// related_rule_ids grow past outcome.MaxRelatedRuleIDsTotal, and batches
// 1-5 (which fill the cap exactly) must survive intact while batch 6 is
// entirely dropped.
func TestSQLiteOutcomeStore_FinalizeDraft_RelatedRuleIDs_CumulativeCapTruncates(t *testing.T) {
	db := openOutcomeDB(t)
	store := wbtsqlite.NewOutcomeStore(db)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()

	draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID,
		EntityType:  "task",
		EntityID:    entityID,
		Result:      "unknown",
	})
	if err != nil {
		t.Fatalf("CreateOutcome draft: %v", err)
	}

	const batchSize = 20
	const numBatches = 6 // 6*20=120 candidates, deliberately past the 100 cap
	batches := newRuleIDBatches(numBatches, batchSize)

	var last outcome.Outcome
	for b, batch := range batches {
		last, err = store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
			Result:         "unknown",
			RelatedRuleIDs: batch,
		})
		if err != nil {
			t.Fatalf("FinalizeDraft batch %d: %v", b, err)
		}
		wantLen := (b + 1) * batchSize
		if wantLen > outcome.MaxRelatedRuleIDsTotal {
			wantLen = outcome.MaxRelatedRuleIDsTotal
		}
		if len(last.RelatedRuleIDs) != wantLen {
			t.Fatalf("after batch %d: len(RelatedRuleIDs) = %d, want %d (cap=%d)",
				b, len(last.RelatedRuleIDs), wantLen, outcome.MaxRelatedRuleIDsTotal)
		}
	}

	if len(last.RelatedRuleIDs) != outcome.MaxRelatedRuleIDsTotal {
		t.Fatalf("final len(RelatedRuleIDs) = %d, want exactly the cap %d — cumulative growth was NOT bounded",
			len(last.RelatedRuleIDs), outcome.MaxRelatedRuleIDsTotal)
	}

	want := concatRuleIDBatches(batches[:5])
	if len(want) != outcome.MaxRelatedRuleIDsTotal {
		t.Fatalf("test bug: want slice has %d entries, expected %d", len(want), outcome.MaxRelatedRuleIDsTotal)
	}
	for i, id := range want {
		if last.RelatedRuleIDs[i] != id {
			t.Errorf("RelatedRuleIDs[%d] = %s, want %s (batches 1-5 must survive intact, in order)",
				i, last.RelatedRuleIDs[i], id)
		}
	}
	for _, dropped := range batches[5] {
		if slices.Contains(last.RelatedRuleIDs, dropped) {
			t.Errorf("batch 6 entry %s survived truncation, want entirely dropped (cap was already full)", dropped)
		}
	}

	reread, err := store.GetOutcomeByID(ctx, draft.ID, &wsID)
	if err != nil {
		t.Fatalf("GetOutcomeByID: %v", err)
	}
	if len(reread.RelatedRuleIDs) != outcome.MaxRelatedRuleIDsTotal {
		t.Errorf("reread len(RelatedRuleIDs) = %d, want %d (persisted value must match returned value)",
			len(reread.RelatedRuleIDs), outcome.MaxRelatedRuleIDsTotal)
	}
}

// newRuleIDBatches builds n batches of size fresh distinct UUIDs each, for
// TestSQLiteOutcomeStore_FinalizeDraft_RelatedRuleIDs_CumulativeCapTruncates.
// Extracted to keep that test's cyclomatic complexity under gocyclo's limit.
func newRuleIDBatches(n, size int) [][]uuid.UUID {
	batches := make([][]uuid.UUID, n)
	for b := range batches {
		batch := make([]uuid.UUID, size)
		for i := range batch {
			batch[i] = uuid.New()
		}
		batches[b] = batch
	}
	return batches
}

// concatRuleIDBatches flattens batches in order into a single slice.
func concatRuleIDBatches(batches [][]uuid.UUID) []uuid.UUID {
	var out []uuid.UUID
	for _, b := range batches {
		out = append(out, b...)
	}
	return out
}
