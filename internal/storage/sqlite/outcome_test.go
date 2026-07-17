package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

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
		Result:         "success",
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
				Result:      "success",
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
		Result:        "success",
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
		Result:      "success",
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

	for _, result := range []string{"success", "failure", "regressed", "partial"} {
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
		Result:      "success",
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
			Result:         "success",
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
