package sqlite_test

import (
	"context"
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
