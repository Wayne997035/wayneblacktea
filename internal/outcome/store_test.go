//go:build integration

package outcome_test

import (
	"context"
	"flag"
	"log"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// skipMigrations are migration files that cannot be run by pgx because they
// contain psql metacommands.
var skipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true,
}

var testPgPool *pgxpool.Pool

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if testing.Short() {
		return m.Run()
	}
	ctx := context.Background()
	c, err := tcpostgres.Run(
		ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_test"),
		tcpostgres.WithUsername("wbt"),
		tcpostgres.WithPassword("wbt"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Fatalf("start postgres container: %v", err)
	}
	defer func() { _ = c.Terminate(ctx) }()

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Printf("get connection string: %v", err)
		return 1
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Printf("pgxpool.New: %v", err)
		return 1
	}
	defer pool.Close()

	applyAllUpMigrationsOnce(ctx, pool)

	testPgPool = pool
	return m.Run()
}

func applyAllUpMigrationsOnce(ctx context.Context, pool *pgxpool.Pool) {
	entries, err := migrationfs.FS.ReadDir(".")
	if err != nil {
		log.Fatalf("read embedded migrations dir: %v", err)
	}
	var ups []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		ups = append(ups, name)
	}
	sort.Strings(ups)

	for _, name := range ups {
		if skipMigrations[name] {
			log.Printf("applyAllUpMigrations: skipping %s (psql-metacommand-only file)", name)
			continue
		}
		body, err := migrationfs.FS.ReadFile(name)
		if err != nil {
			log.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			log.Fatalf("apply %s: %v", name, err)
		}
	}
}

func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return testPgPool
}

// TestStore_CreateOutcome verifies CreateOutcome happy path and error cases.
func TestStore_CreateOutcome(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	entityID := uuid.New()

	tests := []struct {
		name    string
		params  outcome.CreateOutcomeParams
		wantErr bool
	}{
		{
			name: "happy path success",
			params: outcome.CreateOutcomeParams{
				WorkspaceID: &wsID,
				EntityType:  "task",
				EntityID:    entityID,
				Result:      "success",
				Metrics:     []byte(`{"duration_ms":1200}`),
				Notes:       "completed ahead of schedule",
			},
		},
		{
			name: "happy path failure with no metrics or notes",
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
				Notes:       "performance metrics declined",
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
				t.Fatalf("unexpected error: %v", err)
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

// TestStore_CreateOutcome_WorkSessionID verifies work_session_id (wbt-2.0
// P2.4) round-trips through CreateOutcome + GetOutcomeByID, and that omitting
// it (nil) is a pure regression — no error, WorkSessionID stays nil.
func TestStore_CreateOutcome_WorkSessionID(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

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

// TestStore_GetOutcomeByID verifies GetOutcomeByID happy path and not-found case.
func TestStore_GetOutcomeByID(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	entityID := uuid.New()
	created, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID,
		EntityType:  "project",
		EntityID:    entityID,
		Result:      "partial",
		Notes:       "75% of milestones hit",
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
		otherWS := uuid.New()
		_, err := store.GetOutcomeByID(ctx, created.ID, &otherWS)
		if err == nil {
			t.Fatal("expected ErrNotFound for wrong workspace, got nil")
		}
	})
}

// TestStore_CreateAndListEvaluation verifies evaluation creation and listing.
func TestStore_CreateAndListEvaluation(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

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
			Analysis:               "root cause: spec ambiguity",
			Lessons:                []byte(`["clarify acceptance criteria upfront"]`),
			ImprovementSuggestions: []byte(`["add definition of done checklist"]`),
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

	t.Run("empty_lessons_defaults", func(t *testing.T) {
		eval2, err := store.CreateEvaluation(ctx, outcome.CreateEvaluationParams{
			WorkspaceID: &wsID,
			OutcomeID:   o.ID,
			Analysis:    "minimal evaluation",
		})
		if err != nil {
			t.Fatalf("CreateEvaluation with nil lessons: %v", err)
		}
		if eval2.ID == uuid.Nil {
			t.Error("expected non-nil evaluation ID")
		}
	})

	t.Run("list_wrong_workspace_returns_empty", func(t *testing.T) {
		other := uuid.New()
		evals, err := store.ListEvaluationsByOutcomeID(ctx, o.ID, &other)
		if err != nil {
			t.Fatalf("ListEvaluationsByOutcomeID: %v", err)
		}
		if len(evals) != 0 {
			t.Errorf("expected 0 evaluations for wrong workspace, got %d", len(evals))
		}
	})
}

// TestStore_ListFailedOutcomes verifies that only failure/regressed outcomes are returned.
func TestStore_ListFailedOutcomes(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	entityID := uuid.New()

	// Insert one success, one failure, one regressed.
	for _, result := range []string{"success", "failure", "regressed"} {
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
	if len(got) < 2 {
		t.Fatalf("expected at least 2 failed outcomes, got %d", len(got))
	}
	for _, o := range got {
		if o.Result != "failure" && o.Result != "regressed" {
			t.Errorf("unexpected result %q in failed outcomes", o.Result)
		}
	}
}

// TestStore_PruneOlderThan verifies that PruneOlderThan removes old outcomes
// and their evaluations but leaves newer rows untouched.
func TestStore_PruneOlderThan(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	entityID := uuid.New()

	// Insert a recent outcome.
	recent, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID,
		EntityType:  "task",
		EntityID:    entityID,
		Result:      "success",
		Notes:       "recent",
	})
	if err != nil {
		t.Fatalf("CreateOutcome (recent): %v", err)
	}

	// Prune with a cutoff in the past (nothing should be deleted).
	oldCutoff := time.Now().Add(-365 * 24 * time.Hour)
	n, err := store.PruneOlderThan(ctx, oldCutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan (old cutoff): %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 pruned rows, got %d", n)
	}

	// Prune with a future cutoff (recent row should be pruned).
	futureCutoff := time.Now().Add(1 * time.Minute)
	n, err = store.PruneOlderThan(ctx, futureCutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan (future cutoff): %v", err)
	}
	if n == 0 {
		t.Error("expected at least 1 pruned row, got 0")
	}

	// Verify the recent outcome is gone.
	_, err = store.GetOutcomeByID(ctx, recent.ID, &wsID)
	if err == nil {
		t.Error("expected ErrNotFound after prune, got nil")
	}
}

// TestStore_ExistsForEntity mirrors TestSQLiteOutcomeStore_ExistsForEntity —
// verifies the existence check used by complete_task's idempotent
// draft-outcome seeding against a real Postgres backend (indexes
// idx_outcomes_entity_id / idx_outcomes_workspace_entity, migration 000054).
func TestStore_ExistsForEntity(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

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

// TestStore_RelatedRuleIDs verifies that the related_rule_ids UUID[] column
// introduced in migration 000063 round-trips correctly through the PG store.
func TestStore_RelatedRuleIDs(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

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
