//go:build integration

package outcome_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"log/slog"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// TestStore_GetOutcomeByID_NullRelatedRuleIDs_ScansAsEmptySlice is the PR
// #152 round 5 Minor (3rd army) regression test for scanOutcomeRow's nil ->
// []uuid.UUID{} conversion (store.go, "if relatedRuleIDs == nil {
// relatedRuleIDs = []uuid.UUID{} }"): before this test existed, deleting
// that entire branch left the full PG test suite green, meaning the
// conversion had zero coverage. Uses insertDraftWithRelatedRuleIDs's
// includeColumn=false path (already defined below for the NullAndDedupMatrix
// test) to construct a row with a genuinely NULL related_rule_ids column —
// bypassing CreateOutcome/FinalizeDraft, which both always write an
// explicit '{}' and so can never reproduce NULL — reproducing rows written
// by any pre-fix or legacy path.
func TestStore_GetOutcomeByID_NullRelatedRuleIDs_ScansAsEmptySlice(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	id := uuid.New()
	entityID := uuid.New()
	insertDraftWithRelatedRuleIDs(ctx, t, pool, id, wsID, entityID, "task", nil, false)

	got, err := store.GetOutcomeByID(ctx, id, &wsID)
	if err != nil {
		t.Fatalf("GetOutcomeByID: %v", err)
	}
	if got.RelatedRuleIDs == nil {
		t.Error("RelatedRuleIDs = nil, want a non-nil empty slice (NULL column must scan as [], not nil — " +
			"omitempty serializes both identically, but a nil-unsafe consumer using range/append is still at risk)")
	}
	if len(got.RelatedRuleIDs) != 0 {
		t.Errorf("RelatedRuleIDs = %v, want empty", got.RelatedRuleIDs)
	}
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

// ---------------------------------------------------------------------------
// GetLatestForEntity / FinalizeDraft / SeedDraft / RecordExecutionResult
// (migration 000074, decision 80c1e8ae — outcome lifecycle convergence,
// arch-r2 A13). Mirrors internal/storage/sqlite/outcome_test.go's coverage
// against a real Postgres backend (backend-security-design.md §6.5: dual-
// backend logic MUST have both a SQLite test AND a testcontainers PG test).
// ---------------------------------------------------------------------------

// TestStore_GetLatestForEntity verifies not-found, found (most recent of
// several), and workspace-scoping behaviour against real Postgres.
func TestStore_GetLatestForEntity(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

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
	time.Sleep(2 * time.Millisecond)
	second, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "success",
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
// its own timestamp) so tests can construct exact created_at ties. Runs
// directly against the pool, not a transaction — every caller uses a fresh
// uuid.New() entityID, so there is no cross-test collision to roll back.
func insertOutcomeWithIDAndCreatedAt(
	ctx context.Context, t *testing.T, pool *pgxpool.Pool,
	id, wsID uuid.UUID, entityType string, entityID uuid.UUID, result, createdAt string,
) {
	t.Helper()
	_, err := pool.Exec(
		ctx,
		`INSERT INTO outcomes (id, workspace_id, entity_type, entity_id, result, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)`,
		id, wsID, entityType, entityID, result, createdAt,
	)
	if err != nil {
		t.Fatalf("insert outcome with explicit id and created_at: %v", err)
	}
}

// TestStore_GetLatestForEntity_CreatedAtTieBreak is the regression test for
// the security-review-reproduced bug: GetLatestForEntity's ORDER BY had no
// tie-break, so two rows sharing a bit-identical created_at made "the
// latest outcome" for an entity non-deterministic. In the reproduced
// failure chain, that let a newly-seeded 'unknown' draft resolve as older
// than a prior terminal row, so the draft became permanently unreachable —
// and every subsequent record_outcome call for the entity then hard-failed
// against idx_outcomes_one_open_draft with a raw constraint-name leak (see
// TestHandleRecordOutcome_StoreError_SanitizesInternalDetails for that half
// of the fix).
//
// The expected winner (idGreater) is derived from the ORDER BY created_at
// DESC, id DESC contract itself — the same tie-break rule migration 000074's
// dedup step uses (TestMigration000074_Dedup_Postgres_CreatedAtTieBreak) —
// not from running the query once and recording what it happened to return.
func TestStore_GetLatestForEntity_CreatedAtTieBreak(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	entityID := uuid.New()
	const sameCreatedAt = "2026-01-01T00:00:00Z"

	idA, idB := uuid.New(), uuid.New()
	idLesser, idGreater := idA, idB
	if idLesser.String() > idGreater.String() {
		idLesser, idGreater = idGreater, idLesser
	}

	// Mirrors the actual repro shape (one terminal row, one draft) — but the
	// tie-break must hold regardless of which result value lands on which id,
	// which is why idLesser/idGreater are picked independently of role.
	insertOutcomeWithIDAndCreatedAt(ctx, t, pool, idLesser, wsID, "task", entityID, "success", sameCreatedAt)
	insertOutcomeWithIDAndCreatedAt(ctx, t, pool, idGreater, wsID, "task", entityID, "unknown", sameCreatedAt)

	store := outcome.NewStore(pool, &wsID)
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

// TestStore_FinalizeDraft_HappyPath verifies a draft transitions to a
// terminal result IN PLACE against real Postgres — same ID, no second row.
func TestStore_FinalizeDraft_HappyPath(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	entityID := uuid.New()
	draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "unknown",
	})
	if err != nil {
		t.Fatalf("CreateOutcome draft: %v", err)
	}

	finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		Result: "success",
		Notes:  "shipped",
	})
	if err != nil {
		t.Fatalf("FinalizeDraft: %v", err)
	}
	if finalized.ID != draft.ID {
		t.Errorf("FinalizeDraft must reuse the same row ID: got %s, want %s", finalized.ID, draft.ID)
	}
	if finalized.Result != "success" {
		t.Errorf("Result = %q, want success", finalized.Result)
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

// TestStore_FinalizeDraft_AlreadyFinalized verifies the WHERE result='unknown'
// guard against real Postgres: finalizing an already-terminal row returns
// outcome.ErrDraftAlreadyFinalized instead of silently overwriting it.
func TestStore_FinalizeDraft_AlreadyFinalized(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	entityID := uuid.New()
	draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "unknown",
	})
	if err != nil {
		t.Fatalf("CreateOutcome draft: %v", err)
	}
	if _, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{Result: "success"}); err != nil {
		t.Fatalf("first FinalizeDraft: %v", err)
	}

	_, err = store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{Result: "failure"})
	if !errors.Is(err, outcome.ErrDraftAlreadyFinalized) {
		t.Fatalf("expected ErrDraftAlreadyFinalized, got %v", err)
	}

	got, err := store.GetOutcomeByID(ctx, draft.ID, &wsID)
	if err != nil {
		t.Fatalf("GetOutcomeByID: %v", err)
	}
	if got.Result != "success" {
		t.Errorf("result must remain 'success' (first finalize), got %q — second call must not silently overwrite", got.Result)
	}
}

// TestStore_FinalizeDraft_MergeSemantics_PreservesExistingFieldsWhenEmpty is
// the M-1a regression reproduction against real Postgres (PR #152 second
// round): a draft carrying real notes/metrics/related_rule_ids/
// work_session_id, finalized by a call that supplies ONLY entity identity +
// result (exactly what a prompt-injected record_outcome(result="success")
// with nothing else would send) must keep all four existing fields — the
// old blanket UPDATE would have blanked every one of them.
func TestStore_FinalizeDraft_MergeSemantics_PreservesExistingFieldsWhenEmpty(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

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
		Result: "success",
	})
	if err != nil {
		t.Fatalf("FinalizeDraft: %v", err)
	}
	if finalized.ID != draft.ID {
		t.Errorf("FinalizeDraft must reuse the same row ID: got %s, want %s", finalized.ID, draft.ID)
	}
	if finalized.Result != "success" {
		t.Errorf("Result = %q, want success", finalized.Result)
	}
	if finalized.Notes != "real postmortem content the attacker wants gone" {
		t.Errorf("Notes erased by empty-param finalize: got %q, want preserved", finalized.Notes)
	}
	// Compare semantically, not byte-for-byte: the column is jsonb, and
	// Postgres re-serialises it in its own canonical form (`{"duration_ms":
	// 4200}` — note the space), so the exact bytes that went in are NOT what
	// comes back out. The requirement here is "the draft's existing metrics
	// content survived an empty-param finalize", which is a property of the
	// decoded value; asserting on the encoded bytes would be asserting on
	// Postgres's formatting choices instead. The SQLite twin of this test can
	// compare bytes because that column is TEXT and round-trips verbatim.
	var gotMetrics map[string]any
	if err := json.Unmarshal(finalized.Metrics, &gotMetrics); err != nil {
		t.Fatalf("Metrics is not valid JSON after finalize (%q): %v", finalized.Metrics, err)
	}
	if v, ok := gotMetrics["duration_ms"]; !ok || v != float64(4200) {
		t.Errorf("Metrics erased by empty-param finalize: got %q, want the draft's duration_ms=4200 preserved", finalized.Metrics)
	}
	if len(finalized.RelatedRuleIDs) != 1 || finalized.RelatedRuleIDs[0] != ruleID {
		t.Errorf("RelatedRuleIDs erased by empty-param finalize: got %v, want preserved [%s]", finalized.RelatedRuleIDs, ruleID)
	}
	if finalized.WorkSessionID == nil || *finalized.WorkSessionID != sessionID {
		t.Errorf("WorkSessionID erased by empty-param finalize: got %v, want preserved %s", finalized.WorkSessionID, sessionID)
	}
}

// TestStore_FinalizeDraft_RelatedRuleIDs_UnionsNotReplaces is the append-
// semantics redesign's replacement for the old "replace, not union" contract
// (PR #152 round 3 Minor 3 originally pinned the opposite direction — see
// this test's SQLite twin and the package-level design note on why the
// direction flipped: the "replace, not union" property that Minor 3 guarded
// against a REGRESSION TOWARD is now the deliberately shipped behavior, and
// what this test guards is the NEW invariant — existing linked rules can
// never be silently dropped by a later record_outcome/finish_work call).
// A draft seeded with ruleA, then enriched with ruleB, must end up with
// EXACTLY [ruleA, ruleB] — existing first, new appended, no duplicates.
func TestStore_FinalizeDraft_RelatedRuleIDs_UnionsNotReplaces(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

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
	// SECOND FinalizeDraft call afterward — WHERE result='unknown' — unlike
	// a terminal finalize, which can only ever happen once per row)
	// supplying ONLY the new ruleB. If this were a whole-array REPLACE
	// (the pre-redesign contract), the result would become EXACTLY [ruleB],
	// losing ruleA — that's precisely what distinguishes union from replace
	// here; a test that resupplied ruleA alongside ruleB would not.
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
		Result:         "success",
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

// TestStore_FinalizeDraft_RelatedRuleIDs_ConcurrentEnrich_BothSurvive is the
// regression test for 🔴 C-DBI-1 (five-army repro): two concurrent enrich
// calls (Result stays "unknown" on every call so WHERE result='unknown'
// keeps matching for both, mirroring the real record_outcome enrich
// workflow) against the SAME draft, each supplying a DISJOINT set of
// related_rule_ids, must both survive — Postgres serializes the two
// UPDATEs via the row lock, so whichever goroutine loses the race gets its
// SET clause re-evaluated by EvalPlanQual against the winner's
// already-committed row. Before the fix (a WITH CTE reading the same
// table), the loser's union computed against a STALE pre-commit snapshot
// of related_rule_ids and its final assignment silently overwrote the
// winner's entire array instead of unioning with it. See the fix's
// mutation self-proof below (flip the SET clause back to the CTE form and
// this test goes red) and the FinalizeDraft doc comment in store.go for
// the EPQ mechanism.
func TestStore_FinalizeDraft_RelatedRuleIDs_ConcurrentEnrich_BothSurvive(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

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

	const n = 10
	ids := make([]uuid.UUID, n)
	for i := range n {
		ids[i] = uuid.New()
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
				Result:         "unknown",
				RelatedRuleIDs: []uuid.UUID{ids[idx]},
			})
			errs[idx] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d FinalizeDraft error: %v", i, err)
		}
	}

	// Re-read from a fresh query rather than trusting any single goroutine's
	// RETURNING value — the whole point of this test is that a losing
	// goroutine's own RETURNING could (pre-fix) show a truncated array while
	// a DIFFERENT concurrent writer's commit is what actually landed.
	final, err := store.GetOutcomeByID(ctx, draft.ID, &wsID)
	if err != nil {
		t.Fatalf("GetOutcomeByID: %v", err)
	}

	if len(final.RelatedRuleIDs) != n {
		t.Fatalf("final RelatedRuleIDs has %d entries, want exactly %d (one per concurrent caller) — a concurrent write was lost",
			len(final.RelatedRuleIDs), n)
	}
	for _, id := range ids {
		if !slices.Contains(final.RelatedRuleIDs, id) {
			t.Errorf("related_rule_id %s from a concurrent call is missing from the final row — overwritten by another concurrent writer", id)
		}
	}
}

// TestStore_FinalizeDraft_AppendSemantics_NotesNeverRemoved is the direct
// reproduction of the M-1/threat-model attack this redesign closes: a call
// carrying near-empty notes (a single space — passes any "is it non-empty"
// content check) against a draft with real postmortem content must NOT
// remove that content. Under append semantics the attack text can only be
// appended after the real content, never replace it.
func TestStore_FinalizeDraft_AppendSemantics_NotesNeverRemoved(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

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
		Result: "success",
		Notes:  " ", // the attack: a single space, non-empty enough to pass a naive content check
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

// TestStore_FinalizeDraft_AppendSemantics_MetricsOnlyAddsNewKeys verifies
// the metrics field's append-only merge: an existing key's value can never
// be overwritten by a later call, even though a genuinely new key is still
// admitted. Metrics is compared semantically (unmarshal), not byte-for-byte
// — see TestStore_FinalizeDraft_MergeSemantics_PreservesExistingFieldsWhenEmpty's
// comment on why: Postgres re-serialises jsonb in its own canonical form.
func TestStore_FinalizeDraft_AppendSemantics_MetricsOnlyAddsNewKeys(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	entityID := uuid.New()
	draft, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "unknown",
		Metrics: []byte(`{"a":9,"b":2}`),
	})
	if err != nil {
		t.Fatalf("CreateOutcome draft: %v", err)
	}

	finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		Result:  "success",
		Metrics: []byte(`{"a":1,"c":5}`), // "a" already exists (must be ignored), "c" is new (must be added)
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

// TestStore_FinalizeDraft_AppendSemantics_WorkSessionIDSetOnce verifies the
// set-once rule from both directions: writable while NULL, immutable once
// set (no later call — regardless of the ID it supplies — can re-point it).
func TestStore_FinalizeDraft_AppendSemantics_WorkSessionIDSetOnce(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

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
			Result: "success", WorkSessionID: &sessionA,
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
			Result: "success", WorkSessionID: &sessionB,
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

// TestStore_FinalizeDraft_UpdatedAt_BumpsOnlyOnRealWrite verifies migration
// 000075's audit-trail invariant directly against the store: a FinalizeDraft
// call that reaches the store (i.e. genuinely writes) bumps updated_at
// strictly forward. The complementary half of this invariant — that a no-op
// RecordExecutionResult path (ActionDraftPreserved / ActionReplayedIdempotent)
// never even calls the store, so updated_at can't change — is covered by
// TestRecordExecutionResult_PG_AppendSemantics_ByteIdenticalRetryIsIdempotent
// below (it isn't observable at the Store level alone, since the store has
// no way to no-op FinalizeDraft — that decision is lifecycle.go's job).
func TestStore_FinalizeDraft_UpdatedAt_BumpsOnlyOnRealWrite(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

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

	time.Sleep(2 * time.Millisecond) // ensure a strictly-later timestamp is observable
	finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		Result: "success", Notes: "shipped",
	})
	if err != nil {
		t.Fatalf("FinalizeDraft: %v", err)
	}
	if !finalized.UpdatedAt.After(draft.CreatedAt) {
		t.Errorf("UpdatedAt = %v, want strictly after CreatedAt %v (a real write must bump it)", finalized.UpdatedAt, draft.CreatedAt)
	}
}

// TestStore_SeedDraft_UpdatedAtEqualsCreatedAt is F3's PG-side pin: a
// freshly-seeded draft's UpdatedAt must equal its CreatedAt (never yet
// modified in place — migration 000075's invariant, see domain.go's
// UpdatedAt doc comment). PG's SeedDraft INSERT relies on the column's
// `DEFAULT NOW()` (migration 000075) rather than an explicit value, unlike
// SQLite (which cannot add a NOT NULL DEFAULT retroactively and so must
// supply updated_at on every INSERT — see F3's SQLite-side fix and its own
// twin of this test). This test exists so a future DEFAULT NOW() removal on
// the PG column would be caught here, not just observed as a symptom in the
// SQLite backend.
func TestStore_SeedDraft_UpdatedAtEqualsCreatedAt(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	entityID := uuid.New()
	draft, created, err := store.SeedDraft(ctx, &wsID, "task", entityID)
	if err != nil {
		t.Fatalf("SeedDraft: %v", err)
	}
	if !created {
		t.Fatal("expected created=true for a fresh entity")
	}
	if !draft.UpdatedAt.Equal(draft.CreatedAt) {
		t.Errorf("seeded draft: UpdatedAt = %v, want equal to CreatedAt %v (never modified yet)",
			draft.UpdatedAt, draft.CreatedAt)
	}
}

// TestStore_SeedDraft_CreatesOnce verifies the sequential happy path against
// real Postgres: first call creates, second call is a no-op read.
func TestStore_SeedDraft_CreatesOnce(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	entityID := uuid.New()
	first, created, err := store.SeedDraft(ctx, &wsID, "task", entityID)
	if err != nil {
		t.Fatalf("SeedDraft first: %v", err)
	}
	if !created {
		t.Error("expected created=true on first SeedDraft call")
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

// TestStore_SeedDraft_SkipsWhenTerminalOutcomeExists mirrors the SQLite twin
// (TestSQLiteOutcomeStore_SeedDraft_SkipsWhenTerminalOutcomeExists): SeedDraft
// must not add a redundant unknown draft on top of an already-terminal
// outcome against real Postgres either.
func TestStore_SeedDraft_SkipsWhenTerminalOutcomeExists(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	entityID := uuid.New()
	terminal, err := store.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "success",
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
}

// TestSeedDraft_ConcurrentPGConnections_NoDuplicateDraft is the Postgres
// counterpart of the SQLite concurrency proof
// (TestSeedDraftOutcome_ConcurrentSeedDraft_NoDuplicateDraft). Unlike SQLite
// (single-connection, db.go SetMaxOpenConns(1)), pgxpool genuinely runs
// concurrent connections, so this is the strongest available proof that
// idx_outcomes_one_open_draft (migration 000074) — not any Go-level lock —
// is what closes the concurrent complete_task TOCTOU the dispatch named.
func TestSeedDraft_ConcurrentPGConnections_NoDuplicateDraft(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

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
			<-start
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

// TestRecordExecutionResult_PG_Convergence exercises RecordExecutionResult
// end-to-end against real Postgres, covering all four LifecycleAction
// branches in one deterministic sequence — mirrors the SQLite-backed MCP
// handler tests in internal/mcp/tools_outcome_lifecycle_test.go, but at the
// outcome package's own seam boundary (no MCP layer involved).
func TestRecordExecutionResult_PG_Convergence(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()
	entityID := uuid.New()

	// 1. No prior outcome -> ActionCreated.
	created, action, _, err := outcome.RecordExecutionResult(ctx, store, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "unknown",
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult (create): %v", err)
	}
	if action != outcome.ActionCreated {
		t.Fatalf("action = %q, want created", action)
	}

	// 2. Prior outcome is a draft -> ActionFinalizedDraft, same ID.
	finalized, action, _, err := outcome.RecordExecutionResult(ctx, store, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "success", Notes: "done",
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult (finalize): %v", err)
	}
	if action != outcome.ActionFinalizedDraft {
		t.Fatalf("action = %q, want finalized_draft", action)
	}
	if finalized.ID != created.ID {
		t.Fatalf("finalize must reuse draft row ID: got %s, want %s", finalized.ID, created.ID)
	}

	// 3. Identical replay -> ActionReplayedIdempotent, same ID, no new row.
	replayed, action, _, err := outcome.RecordExecutionResult(ctx, store, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "success", Notes: "done",
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult (replay): %v", err)
	}
	if action != outcome.ActionReplayedIdempotent {
		t.Fatalf("action = %q, want replayed_idempotent", action)
	}
	if replayed.ID != finalized.ID {
		t.Fatalf("replay must return the same row ID: got %s, want %s", replayed.ID, finalized.ID)
	}

	// 4. Different terminal result -> ActionSuperseded, new row, linked back.
	superseded, action, _, err := outcome.RecordExecutionResult(ctx, store, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID, Result: "regressed", Notes: "actually broke prod",
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult (supersede): %v", err)
	}
	if action != outcome.ActionSuperseded {
		t.Fatalf("action = %q, want superseded", action)
	}
	if superseded.ID == finalized.ID {
		t.Fatal("supersede must create a NEW row, not reuse the prior ID")
	}
	if superseded.SupersedesID == nil || *superseded.SupersedesID != finalized.ID {
		t.Fatalf("SupersedesID = %v, want %s", superseded.SupersedesID, finalized.ID)
	}

	// The prior (superseded) row must remain untouched — audit trail intact.
	priorStillIntact, err := store.GetOutcomeByID(ctx, finalized.ID, &wsID)
	if err != nil {
		t.Fatalf("GetOutcomeByID(prior): %v", err)
	}
	if priorStillIntact.Result != "success" {
		t.Errorf("prior row's result must remain 'success' (not silently overwritten), got %q", priorStillIntact.Result)
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
	if count != 2 {
		t.Errorf("expected exactly 2 rows for the entity (finalized draft + 1 supersession), got %d", count)
	}
}

// TestRecordExecutionResult_PG_M1_UnknownAgainstDraftPreservesContent
// reproduces PR #152 second army finding M-1 against real Postgres,
// complementing the fast fake-backed unit test (lifecycle_test.go) and the
// SQLite-backed MCP-handler end-to-end test
// (internal/mcp/tools_outcome_lifecycle_test.go). RecordExecutionResult is
// dialect-agnostic orchestration with no SQL of its own, so this isn't
// required by backend-security-design.md §6.5 (logic doesn't differ between
// backends) — added anyway as belt-and-suspenders proof that the fix
// behaves identically against the real FinalizeDraft SQL on both engines,
// not just the fake spy.
func TestRecordExecutionResult_PG_M1_UnknownAgainstDraftPreservesContent(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	entityID := uuid.New()
	sessionID := uuid.New()
	ruleID := uuid.New()

	seeded, action, _, err := outcome.RecordExecutionResult(ctx, store, outcome.CreateOutcomeParams{
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
		t.Fatalf("RecordExecutionResult (seed draft): %v", err)
	}
	if action != outcome.ActionCreated {
		t.Fatalf("seed action = %q, want created", action)
	}

	// The attack: another call, still result="unknown", with none of the
	// original content.
	afterAttack, action, _, err := outcome.RecordExecutionResult(ctx, store, outcome.CreateOutcomeParams{
		WorkspaceID: &wsID,
		EntityType:  "task",
		EntityID:    entityID,
		Result:      "unknown",
	})
	if err != nil {
		t.Fatalf("RecordExecutionResult (attack): %v", err)
	}
	if action != outcome.ActionDraftPreserved {
		t.Errorf("attack action = %q, want draft_preserved", action)
	}
	assertM1DraftContentUnchanged(t, seeded, afterAttack, ruleID, sessionID)

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
		t.Errorf("expected exactly 1 row after unknown-against-unknown call, got %d", count)
	}
}

// assertM1DraftContentUnchanged asserts the M-1 invariant: the draft
// returned after the attack call is the SAME row as seeded, with every
// content field byte-identical to what was seeded — split out of
// TestRecordExecutionResult_PG_M1_UnknownAgainstDraftPreservesContent to
// keep that test's cyclomatic complexity under the gocyclo threshold.
func assertM1DraftContentUnchanged(t *testing.T, seeded, afterAttack outcome.Outcome, ruleID, sessionID uuid.UUID) {
	t.Helper()
	if afterAttack.ID != seeded.ID {
		t.Errorf("attack call must return the SAME draft row: got %s, want %s", afterAttack.ID, seeded.ID)
	}
	if afterAttack.Notes != seeded.Notes {
		t.Errorf("Notes erased: got %q, want unchanged %q", afterAttack.Notes, seeded.Notes)
	}
	if string(afterAttack.Metrics) != string(seeded.Metrics) {
		t.Errorf("Metrics erased: got %q, want unchanged %q", afterAttack.Metrics, seeded.Metrics)
	}
	if len(afterAttack.RelatedRuleIDs) != 1 || afterAttack.RelatedRuleIDs[0] != ruleID {
		t.Errorf("RelatedRuleIDs erased: got %v, want unchanged [%s]", afterAttack.RelatedRuleIDs, ruleID)
	}
	if afterAttack.WorkSessionID == nil || *afterAttack.WorkSessionID != sessionID {
		t.Errorf("WorkSessionID erased: got %v, want unchanged %s", afterAttack.WorkSessionID, sessionID)
	}
}

// TestRecordExecutionResult_PG_AppendSemantics_ByteIdenticalRetryIsIdempotent_UpdatedAtUnchanged
// is the RecordExecutionResult-level (not just Store-level) proof that a
// no-op path never touches updated_at, complementing
// TestStore_FinalizeDraft_UpdatedAt_BumpsOnlyOnRealWrite's proof that a real
// write does. A byte-identical retry of a content-bearing enrich call must
// be classified ActionReplayedIdempotent, never re-invoke FinalizeDraft
// (so updated_at cannot change), and must not duplicate the notes content.
func TestRecordExecutionResult_PG_AppendSemantics_ByteIdenticalRetryIsIdempotent_UpdatedAtUnchanged(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()
	entityID := uuid.New()

	params := outcome.CreateOutcomeParams{
		WorkspaceID: &wsID, EntityType: "task", EntityID: entityID,
		Result: "unknown", Notes: "still investigating",
	}
	first, action, _, err := outcome.RecordExecutionResult(ctx, store, params)
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
		Result: "unknown", Notes: "verified in prod", // byte-identical retry of the enrich call
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

// TestRecordExecutionResult_PG_AppendSemantics_GenuinelyNewInfoStillWrites is
// the mandatory reverse-protection guard the dispatch calls out explicitly:
// after establishing a byte-identical retry is correctly a no-op (previous
// test), a FOLLOW-UP call carrying genuinely new information in EACH of the
// four append-only fields must still write and report ActionDraftEnriched —
// this is what prevents the M-2a regression from being reintroduced by an
// isDraftIdempotentReplay that's too eager to call something "no new info".
func TestRecordExecutionResult_PG_AppendSemantics_GenuinelyNewInfoStillWrites(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()
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
		Notes:          "second note",           // genuinely new text
		Metrics:        []byte(`{"a":9,"b":2}`), // "a" repeats (must be ignored), "b" is new
		RelatedRuleIDs: []uuid.UUID{ruleB},      // genuinely new id
		WorkSessionID:  &sessionID,              // was NULL, now set
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

// ---------------------------------------------------------------------------
// Migration 000074 dedup (PR #152 second army finding M-2). Uses the same
// down-then-up-inside-a-rolled-back-transaction technique as
// internal/decision's TestMigration000073_Backfill_Postgres: down.sql drops
// idx_outcomes_one_open_draft + supersedes_id, the test seeds legacy
// duplicate rows that could only exist pre-000074 (the old code path
// unconditionally INSERTed with no uniqueness rule), then up.sql
// re-applies (dedup + index), all inside a transaction that's always rolled
// back so the shared pool's outcomes table is never actually mutated
// outside this test.
// ---------------------------------------------------------------------------

// readMigration000074Bodies loads both SQL bodies for migration 000074.
func readMigration000074Bodies(t *testing.T) (up, down []byte) {
	t.Helper()
	up, err := migrationfs.FS.ReadFile("000074_outcomes_supersession.up.sql")
	if err != nil {
		t.Fatalf("read migration 000074 up.sql: %v", err)
	}
	down, err = migrationfs.FS.ReadFile("000074_outcomes_supersession.down.sql")
	if err != nil {
		t.Fatalf("read migration 000074 down.sql: %v", err)
	}
	return up, down
}

// insertLegacyUnknownOutcomeTx inserts a pre-000074-shaped outcomes row (no
// supersedes_id column at that point) inside tx, with an explicit created_at
// so duplicate-group ordering is deterministic regardless of wall-clock test
// time. Returns the generated row id.
func insertLegacyUnknownOutcomeTx(
	ctx context.Context, t *testing.T, tx pgx.Tx, wsID uuid.UUID, entityType string, entityID uuid.UUID, createdAt, notes string,
) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := tx.QueryRow(
		ctx,
		`INSERT INTO outcomes (workspace_id, entity_type, entity_id, result, notes, created_at)
			VALUES ($1, $2, $3, 'unknown', $4, $5) RETURNING id`,
		wsID, entityType, entityID, notes, createdAt,
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert legacy outcome: %v", err)
	}
	return id
}

// TestMigration000074_Dedup_Postgres reproduces PR #152 second army finding
// M-2 against real Postgres and proves the fix: pre-existing duplicate
// result='unknown' rows for the same (workspace, entity_type, entity_id)
// must not abort CREATE UNIQUE INDEX. Mirrors the SQLite twin coverage
// (internal/storage/sqlite's TestMigration000074_Dedup_SQLite) —
// backend-security-design.md §6.5 dual-backend parity requires the same
// dedup SQL to be independently verified on both engines.
func TestMigration000074_Dedup_Postgres(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()

	upBody, downBody := readMigration000074Bodies(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			t.Logf("rollback tx: %v", rbErr)
		}
	})

	if _, err := tx.Exec(ctx, string(downBody)); err != nil {
		t.Fatalf("drop supersession column/index (down.sql): %v", err)
	}

	wsID := uuid.New()
	entityID := uuid.New()
	otherEntityID := uuid.New() // control: no duplicates, must survive untouched

	older := insertLegacyUnknownOutcomeTx(ctx, t, tx, wsID, "task", entityID, "2026-01-01T00:00:00Z", "older empty draft")
	newer := insertLegacyUnknownOutcomeTx(ctx, t, tx, wsID, "task", entityID, "2026-01-02T00:00:00Z", "newer draft with content")
	single := insertLegacyUnknownOutcomeTx(ctx, t, tx, wsID, "task", otherEntityID, "2026-01-01T00:00:00Z", "no dup")

	if _, err := tx.Exec(
		ctx, `INSERT INTO evaluations (workspace_id, outcome_id, analysis) VALUES ($1, $2, $3)`,
		wsID, older, "stale eval on the older dup",
	); err != nil {
		t.Fatalf("insert legacy evaluation: %v", err)
	}

	// Applying 000074 without the dedup step would fail here with a UNIQUE
	// constraint violation — the exact M-2 production symptom (migration
	// aborts mid-way, dirty version, server can't start). This re-applies
	// the FIXED migration body, which dedups first.
	if _, err := tx.Exec(ctx, string(upBody)); err != nil {
		t.Fatalf("re-apply migration 000074 (expected to succeed after dedup): %v", err)
	}

	var remaining uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM outcomes WHERE entity_id = $1 AND result = 'unknown'`, entityID).Scan(&remaining)
	if err != nil {
		t.Fatalf("expected exactly one surviving 'unknown' row for the duplicate entity: %v", err)
	}
	if remaining != newer {
		t.Errorf("expected the newer duplicate %s to survive, got %s (older %s should have been deleted)", newer, remaining, older)
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM outcomes WHERE id = $1`, older).Scan(&count); err != nil {
		t.Fatalf("count older duplicate: %v", err)
	}
	if count != 0 {
		t.Error("expected the older duplicate draft to be deleted by the dedup step")
	}

	if err := tx.QueryRow(ctx, `SELECT count(*) FROM evaluations WHERE outcome_id = $1`, older).Scan(&count); err != nil {
		t.Fatalf("count stale evaluation: %v", err)
	}
	if count != 0 {
		t.Error("expected the stale evaluation on the deleted duplicate to be cleaned up too")
	}

	if err := tx.QueryRow(ctx, `SELECT count(*) FROM outcomes WHERE id = $1`, single).Scan(&count); err != nil {
		t.Fatalf("count non-duplicate control row: %v", err)
	}
	if count != 1 {
		t.Error("expected the non-duplicate control row to survive untouched")
	}

	// The unique index now actually enforces uniqueness going forward.
	_, err = tx.Exec(
		ctx, `INSERT INTO outcomes (workspace_id, entity_type, entity_id, result) VALUES ($1, 'task', $2, 'unknown')`,
		wsID, entityID,
	)
	if err == nil {
		t.Error("expected the unique index to reject a second 'unknown' row post-migration")
	}
}

// insertLegacyUnknownOutcomeWithIDTx is insertLegacyUnknownOutcomeTx's twin
// for tests that need to control the row id explicitly instead of accepting
// the column's uuid_generate_v4() default — required for the created_at
// tie-break test below, where the expected survivor must be knowable ahead
// of time from the migration's ORDER BY created_at DESC, id DESC contract
// itself, not from observing which row a run happens to keep. Mirrors
// internal/storage/sqlite's insertLegacyUnknownOutcomeWithID.
func insertLegacyUnknownOutcomeWithIDTx(
	ctx context.Context, t *testing.T, tx pgx.Tx, id, wsID uuid.UUID, entityType string, entityID uuid.UUID, createdAt, notes string,
) {
	t.Helper()
	_, err := tx.Exec(
		ctx,
		`INSERT INTO outcomes (id, workspace_id, entity_type, entity_id, result, notes, created_at)
			VALUES ($1, $2, $3, $4, 'unknown', $5, $6)`,
		id, wsID, entityType, entityID, notes, createdAt,
	)
	if err != nil {
		t.Fatalf("insert legacy outcome with explicit id: %v", err)
	}
}

// TestMigration000074_Dedup_Postgres_CreatedAtTieBreak covers the case
// TestMigration000074_Dedup_Postgres above does not: two duplicate
// 'unknown' drafts for the same (workspace, entity_type, entity_id) whose
// created_at values are bit-for-bit identical, rather than merely close.
// When created_at ties, ORDER BY created_at DESC alone cannot pick a
// winner — the migration's dedup step (migrations/000074_outcomes_
// supersession.up.sql) resolves this with a second ORDER BY key, id DESC,
// which this test pins down directly. Mirrors the SQLite twin
// (TestMigration000074_Dedup_SQLite_CreatedAtTieBreak) — backend-security-
// design.md §6.5 dual-backend parity requires the same tie-break rule to be
// independently verified on both engines. The expected survivor (the row
// with the greater id, compared the same way Postgres's uuid type orders —
// byte-for-byte on the 16-byte value, which a lowercase-hex String()
// comparison reproduces because every character position is either a fixed
// hyphen or a same-case hex digit) is derived from that ORDER BY clause
// itself, not from observing what one run happens to produce.
func TestMigration000074_Dedup_Postgres_CreatedAtTieBreak(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()

	upBody, downBody := readMigration000074Bodies(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			t.Logf("rollback tx: %v", rbErr)
		}
	})

	if _, err := tx.Exec(ctx, string(downBody)); err != nil {
		t.Fatalf("drop supersession column/index (down.sql): %v", err)
	}

	wsID := uuid.New()
	entityID := uuid.New()
	const sameCreatedAt = "2026-01-01T00:00:00Z"

	// Label the two ids by string (== byte, for a fixed-format lowercase hex
	// UUID string) order so the expected survivor (idGreater) is known
	// before the migration runs, per the id DESC tie-break contract.
	idA, idB := uuid.New(), uuid.New()
	idLesser, idGreater := idA, idB
	if idLesser.String() > idGreater.String() {
		idLesser, idGreater = idGreater, idLesser
	}

	insertLegacyUnknownOutcomeWithIDTx(ctx, t, tx, idLesser, wsID, "task", entityID, sameCreatedAt, "tie-break loser")
	insertLegacyUnknownOutcomeWithIDTx(ctx, t, tx, idGreater, wsID, "task", entityID, sameCreatedAt, "tie-break winner")

	if _, err := tx.Exec(ctx, string(upBody)); err != nil {
		t.Fatalf("re-apply migration 000074 (expected to succeed after dedup): %v", err)
	}

	var remaining uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM outcomes WHERE entity_id = $1 AND result = 'unknown'`, entityID).Scan(&remaining)
	if err != nil {
		t.Fatalf("expected exactly one surviving 'unknown' row for the tied-created_at duplicate: %v", err)
	}
	if remaining != idGreater {
		t.Errorf("expected the row with the greater id (%s) to survive the created_at tie via "+
			"ORDER BY created_at DESC, id DESC, got %s", idGreater, remaining)
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM outcomes WHERE id = $1`, idLesser).Scan(&count); err != nil {
		t.Fatalf("count tie-break loser: %v", err)
	}
	if count != 0 {
		t.Error("expected the tie-break loser (lesser id, identical created_at) to be deleted by the dedup step")
	}
}

// ---------------------------------------------------------------------------
// PR #152 round 4 Critical (finding F1) + Major (finding F6): FinalizeDraft's
// related_rule_ids union SQL evaluated `nid <> ALL(related_rule_ids)` against
// a NULL existing column — SQL three-valued logic makes `x <> ALL(NULL)`
// NULL, not true, so array_agg returned NULL and the ENTIRE caller-supplied
// payload silently vanished. SeedDraft (the production complete_task ->
// record_outcome entry point) left related_rule_ids unset on INSERT, which
// defaults to NULL (migration 000063 has no DEFAULT), so every draft it
// seeded hit this exactly. Separately, F6: the SQL only filters ids already
// PRESENT in the existing column — it never deduplicates the caller-supplied
// array against ITSELF, a gap dedupeUUIDsPreserveOrder (store.go) closes but
// which had zero test coverage (swapping it for a no-op passthrough left the
// full integration suite green).
// ---------------------------------------------------------------------------

// TestStore_SeedDraft_FinalizeDraft_RelatedRuleIDs_ProductionPath is F1's
// primary acceptance criterion: the actual production call sequence
// (complete_task's SeedDraft, then a later record_outcome's FinalizeDraft
// carrying related_rule_ids) against real Postgres. Every existing
// FinalizeDraft test in this file seeds its draft via CreateOutcome, which
// already writes related_rule_ids = '{}' explicitly — that path never
// exercised the NULL the SeedDraft-created row actually has. This test
// starts from SeedDraft specifically to close that gap.
func TestStore_SeedDraft_FinalizeDraft_RelatedRuleIDs_ProductionPath(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

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

	// Direct raw-SQL check (not just the Go-layer view, which normalizes a
	// NULL scan to an empty slice either way — see scanOutcomeRow's `if
	// relatedRuleIDs == nil { relatedRuleIDs = []uuid.UUID{} }`, which would
	// mask this check if it only looked at draft.RelatedRuleIDs). This
	// directly verifies SeedDraft's INSERT wrote '{}'::uuid[], not left the
	// column NULL — the specific defense-in-depth line this PR added
	// alongside FinalizeDraft's COALESCE (SQL fix would mask a regression
	// here too, since it now tolerates NULL — this check isolates the
	// SeedDraft-side fix specifically).
	var isNull bool
	if err := pool.QueryRow(ctx, `SELECT related_rule_ids IS NULL FROM outcomes WHERE id = $1`, draft.ID).Scan(&isNull); err != nil {
		t.Fatalf("raw related_rule_ids NULL check: %v", err)
	}
	if isNull {
		t.Fatal("SeedDraft left related_rule_ids as SQL NULL — must write '{}'::uuid[] explicitly")
	}

	finalized, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
		Result:         "success",
		RelatedRuleIDs: []uuid.UUID{ruleA, ruleB},
	})
	if err != nil {
		t.Fatalf("FinalizeDraft: %v", err)
	}
	if len(finalized.RelatedRuleIDs) != 2 || finalized.RelatedRuleIDs[0] != ruleA || finalized.RelatedRuleIDs[1] != ruleB {
		t.Fatalf("RelatedRuleIDs = %v, want EXACTLY [%s, %s] — the SeedDraft->FinalizeDraft production "+
			"path must not silently drop caller-supplied rule links", finalized.RelatedRuleIDs, ruleA, ruleB)
	}

	// Re-read from a fresh query too, not just the RETURNING clause, to rule
	// out a bug where RETURNING reflects the pre-commit value.
	reread, err := store.GetOutcomeByID(ctx, draft.ID, &wsID)
	if err != nil {
		t.Fatalf("GetOutcomeByID: %v", err)
	}
	if len(reread.RelatedRuleIDs) != 2 || reread.RelatedRuleIDs[0] != ruleA || reread.RelatedRuleIDs[1] != ruleB {
		t.Errorf("reread RelatedRuleIDs = %v, want EXACTLY [%s, %s]", reread.RelatedRuleIDs, ruleA, ruleB)
	}
}

// insertDraftWithRelatedRuleIDs inserts an 'unknown' draft row directly via
// SQL, bypassing both CreateOutcome (which always writes an explicit '{}'
// for a nil slice — see store.go's CreateOutcome comment — and so can never
// reproduce a NULL column) and SeedDraft (fixed by this PR to also write
// '{}'). includeColumn=false omits related_rule_ids from the INSERT
// entirely, which — per migration 000063's `UUID[]` with no DEFAULT —
// leaves it genuinely NULL, reproducing rows written by the pre-fix
// SeedDraft and any other legacy write path.
func insertDraftWithRelatedRuleIDs(
	ctx context.Context, t *testing.T, pool *pgxpool.Pool,
	id, wsID, entityID uuid.UUID, entityType string,
	relatedRuleIDs []uuid.UUID, includeColumn bool,
) {
	t.Helper()
	if !includeColumn {
		_, err := pool.Exec(
			ctx,
			`INSERT INTO outcomes (id, workspace_id, entity_type, entity_id, result) VALUES ($1, $2, $3, $4, 'unknown')`,
			id, wsID, entityType, entityID,
		)
		if err != nil {
			t.Fatalf("insert draft with NULL related_rule_ids: %v", err)
		}
		return
	}
	_, err := pool.Exec(
		ctx,
		`INSERT INTO outcomes (id, workspace_id, entity_type, entity_id, result, related_rule_ids) VALUES ($1, $2, $3, $4, 'unknown', $5)`,
		id, wsID, entityType, entityID, relatedRuleIDs,
	)
	if err != nil {
		t.Fatalf("insert draft with explicit related_rule_ids %v: %v", relatedRuleIDs, err)
	}
}

// TestStore_FinalizeDraft_RelatedRuleIDs_NullAndDedupMatrix is the combined
// F1 + F6 test matrix the dispatch asked for in one place: existing column
// state (NULL / empty array / already has a value) crossed with whether the
// caller-supplied incoming array carries an internal duplicate. Every case
// asserts the exact expected union so both blind spots (NULL swallowing the
// whole payload, and an unduplicated incoming array producing a duplicate
// entry) are pinned by the same table.
func TestStore_FinalizeDraft_RelatedRuleIDs_NullAndDedupMatrix(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	ruleX := uuid.New() // pre-existing value, used only by the "has-value" cases
	ruleY, ruleZ := uuid.New(), uuid.New()

	tests := []struct {
		name               string
		existingIncludeCol bool        // false = column omitted from INSERT => genuinely NULL
		existingIDs        []uuid.UUID // ignored when existingIncludeCol is false
		incoming           []uuid.UUID
		want               []uuid.UUID
	}{
		{
			name:               "existing NULL, incoming has an internal duplicate",
			existingIncludeCol: false,
			incoming:           []uuid.UUID{ruleY, ruleY},
			want:               []uuid.UUID{ruleY},
		},
		{
			name:               "existing NULL, incoming has no duplicates",
			existingIncludeCol: false,
			incoming:           []uuid.UUID{ruleY, ruleZ},
			want:               []uuid.UUID{ruleY, ruleZ},
		},
		{
			name:               "existing empty array, incoming has an internal duplicate",
			existingIncludeCol: true,
			existingIDs:        []uuid.UUID{},
			incoming:           []uuid.UUID{ruleY, ruleY, ruleZ},
			want:               []uuid.UUID{ruleY, ruleZ},
		},
		{
			name:               "existing empty array, incoming has no duplicates",
			existingIncludeCol: true,
			existingIDs:        []uuid.UUID{},
			incoming:           []uuid.UUID{ruleY, ruleZ},
			want:               []uuid.UUID{ruleY, ruleZ},
		},
		{
			name:               "existing already has a value, incoming has an internal duplicate",
			existingIncludeCol: true,
			existingIDs:        []uuid.UUID{ruleX},
			incoming:           []uuid.UUID{ruleY, ruleY},
			want:               []uuid.UUID{ruleX, ruleY},
		},
		{
			name:               "existing already has a value, incoming has no duplicates",
			existingIncludeCol: true,
			existingIDs:        []uuid.UUID{ruleX},
			incoming:           []uuid.UUID{ruleY, ruleZ},
			want:               []uuid.UUID{ruleX, ruleY, ruleZ},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			entityID := uuid.New()
			insertDraftWithRelatedRuleIDs(ctx, t, pool, id, wsID, entityID, "task", tc.existingIDs, tc.existingIncludeCol)

			finalized, err := store.FinalizeDraft(ctx, id, outcome.CreateOutcomeParams{
				Result:         "success",
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

// TestStore_FinalizeDraft_RelatedRuleIDs_CumulativeCapTruncates reproduces
// PR #152 round 5 Major (M-R5-1) guarantee A end-to-end against a real
// Postgres backend: a draft enriched with maxRelatedRuleIDs-sized batches of
// FRESH distinct UUIDs across repeated FinalizeDraft calls (mirroring N
// separate record_outcome(result="unknown", related_rule_ids=[...20 new...])
// MCP calls against the SAME draft — Result stays "unknown" so the row
// remains eligible for a further FinalizeDraft each time) must never grow
// related_rule_ids past outcome.MaxRelatedRuleIDsTotal, no matter how many
// enrich calls arrive.
//
// 6 batches of 20 fresh UUIDs each (120 distinct candidates total) are
// applied — deliberately past the cap (100), not stopping AT the boundary,
// per the dispatch's "測試要真的堆到超過上限,不要只測邊界值" requirement.
// After 5 batches the union sits exactly at the cap (5*20=100); the 6th
// batch's 20 IDs must be entirely rejected by the truncation (existing
// entries always sort first in the union — see MaxRelatedRuleIDsTotal's doc
// comment — so once the cap is full there is no room left for ANY new
// entry, not even a partial slice of batch 6).
func TestStore_FinalizeDraft_RelatedRuleIDs_CumulativeCapTruncates(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

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
	batches := make([][]uuid.UUID, numBatches)
	for b := range numBatches {
		batch := make([]uuid.UUID, batchSize)
		for i := range batchSize {
			batch[i] = uuid.New()
		}
		batches[b] = batch
	}

	var last outcome.Outcome
	for b, batch := range batches {
		last, err = store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{
			Result:         "unknown",
			RelatedRuleIDs: batch,
		})
		if err != nil {
			t.Fatalf("FinalizeDraft batch %d: %v", b, err)
		}
		wantLen := min((b+1)*batchSize, outcome.MaxRelatedRuleIDsTotal)
		if len(last.RelatedRuleIDs) != wantLen {
			t.Fatalf("after batch %d: len(RelatedRuleIDs) = %d, want %d (cap=%d)",
				b, len(last.RelatedRuleIDs), wantLen, outcome.MaxRelatedRuleIDsTotal)
		}
	}

	if len(last.RelatedRuleIDs) != outcome.MaxRelatedRuleIDsTotal {
		t.Fatalf("final len(RelatedRuleIDs) = %d, want exactly the cap %d — cumulative growth was NOT bounded",
			len(last.RelatedRuleIDs), outcome.MaxRelatedRuleIDsTotal)
	}

	// The surviving 100 entries must be EXACTLY batches 1-5 concatenated in
	// order — batch 6 (the one that pushed past the cap) must be entirely
	// absent, and no earlier batch's entries may have been silently dropped
	// to make room for it.
	want := append(append(append(append(batches[0], batches[1]...), batches[2]...), batches[3]...), batches[4]...)
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

	// Re-read from a fresh query, not just the RETURNING clause, to rule out
	// a bug where RETURNING reflects a value the actual committed row
	// doesn't have.
	reread, err := store.GetOutcomeByID(ctx, draft.ID, &wsID)
	if err != nil {
		t.Fatalf("GetOutcomeByID: %v", err)
	}
	if len(reread.RelatedRuleIDs) != outcome.MaxRelatedRuleIDsTotal {
		t.Errorf("reread len(RelatedRuleIDs) = %d, want %d (persisted value must match RETURNING)",
			len(reread.RelatedRuleIDs), outcome.MaxRelatedRuleIDsTotal)
	}
}

// captureSlogWarn temporarily redirects the default slog logger to a buffer
// and returns it plus a restore func, so tests can assert on the EXACT
// warn-or-not behaviour of a call without depending on log level defaults.
func captureSlogWarn(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// insertDraftWithNotesAndRelatedRuleIDs inserts an 'unknown' draft row
// directly via SQL, letting tests seed a pre-existing related_rule_ids array
// that already exceeds outcome.MaxRelatedRuleIDsTotal — a state only
// reachable via data written before that cap existed (see
// outcome.CapRelatedRuleIDs's doc comment) — reproducing PR #152 round 6
// Major finding m-R6-3's exact repro shape (existing=150, cap=100).
func insertDraftWithNotesAndRelatedRuleIDs(
	ctx context.Context, t *testing.T, pool *pgxpool.Pool,
	id, wsID, entityID uuid.UUID, entityType string, relatedRuleIDs []uuid.UUID,
) {
	t.Helper()
	_, err := pool.Exec(
		ctx,
		`INSERT INTO outcomes (id, workspace_id, entity_type, entity_id, result, related_rule_ids) VALUES ($1, $2, $3, $4, 'unknown', $5)`,
		id, wsID, entityType, entityID, relatedRuleIDs,
	)
	if err != nil {
		t.Fatalf("insert draft with related_rule_ids: %v", err)
	}
}

// newDistinctRuleIDs returns n freshly generated, guaranteed-distinct UUIDs.
func newDistinctRuleIDs(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range n {
		out[i] = uuid.New()
	}
	return out
}

// TestStore_FinalizeDraft_RelatedRuleIDs_ExistingOverCap_NeverDropsExisting
// is PR #152 round 6 Major finding m-R6-3's fix verification: doc claims
// "an already-linked rule ID can never be silently removed by a later call
// exceeding the cap" — the second-army round 6 repro found this FALSE in a
// state only reachable via legacy data (existing already over the cap,
// 150 > 100). Two sub-cases, both against SEPARATELY seeded draft rows so
// each starts from the identical existing=150 state:
//   - a call supplying 1 new ID: before this fix, PG dropped 50 of the 150
//     existing entries to make room; after the fix, ALL 150 existing survive
//     and the 1 new ID is rejected instead.
//   - a notes-only call supplying ZERO new IDs: PG already got this right
//     (its CASE short-circuits when array_length($4) IS NULL) — pinned here
//     so a future change can't silently regress it.
//
// Also verifies m-R6-4 (the slog.Warn accuracy fix) in the same run: the
// "1 new ID" sub-case DID truncate something (the new ID got dropped) and
// must warn; the notes-only sub-case truncated NOTHING (existing untouched)
// and must NOT warn — reproducing the second-army finding that the OLD
// `preTruncateLen > cap` check alone fired a false "truncated" warning for
// the notes-only call even though final_count == pre_truncate_count.
func TestStore_FinalizeDraft_RelatedRuleIDs_ExistingOverCap_NeverDropsExisting(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	existing := newDistinctRuleIDs(outcome.MaxRelatedRuleIDsTotal + 50) // 150, cap=100

	t.Run("one new ID: all 150 existing survive, new ID dropped, warns", func(t *testing.T) {
		buf := captureSlogWarn(t)
		draftID := uuid.New()
		entityID := uuid.New()
		insertDraftWithNotesAndRelatedRuleIDs(ctx, t, pool, draftID, wsID, entityID, "task", existing)

		newID := uuid.New()
		got, err := store.FinalizeDraft(ctx, draftID, outcome.CreateOutcomeParams{
			Result:         "success",
			RelatedRuleIDs: []uuid.UUID{newID},
		})
		if err != nil {
			t.Fatalf("FinalizeDraft: %v", err)
		}
		if len(got.RelatedRuleIDs) != len(existing) {
			t.Fatalf("len(RelatedRuleIDs) = %d, want %d (ALL existing must survive)", len(got.RelatedRuleIDs), len(existing))
		}
		for i, id := range existing {
			if got.RelatedRuleIDs[i] != id {
				t.Fatalf("RelatedRuleIDs[%d] = %s, want %s — existing entry silently altered/reordered", i, got.RelatedRuleIDs[i], id)
			}
		}
		if slices.Contains(got.RelatedRuleIDs, newID) {
			t.Errorf("new ID %s survived even though the cap was already full by existing alone — want it dropped", newID)
		}
		if !strings.Contains(buf.String(), "related_rule_ids exceeded cumulative cap") {
			t.Errorf("expected a truncation warn (the new ID WAS dropped), got none: log=%q", buf.String())
		}
	})

	t.Run("notes-only, zero new IDs: all 150 existing survive, no warn", func(t *testing.T) {
		buf := captureSlogWarn(t)
		draftID := uuid.New()
		entityID := uuid.New()
		insertDraftWithNotesAndRelatedRuleIDs(ctx, t, pool, draftID, wsID, entityID, "task", existing)

		got, err := store.FinalizeDraft(ctx, draftID, outcome.CreateOutcomeParams{
			Result: "success",
			Notes:  "wrapping up, no new rule links this time",
		})
		if err != nil {
			t.Fatalf("FinalizeDraft: %v", err)
		}
		if len(got.RelatedRuleIDs) != len(existing) {
			t.Fatalf("len(RelatedRuleIDs) = %d, want %d (a notes-only call must never touch related_rule_ids)",
				len(got.RelatedRuleIDs), len(existing))
		}
		for i, id := range existing {
			if got.RelatedRuleIDs[i] != id {
				t.Fatalf("RelatedRuleIDs[%d] = %s, want %s — existing entry silently altered/reordered", i, got.RelatedRuleIDs[i], id)
			}
		}
		if strings.Contains(buf.String(), "related_rule_ids exceeded cumulative cap") {
			t.Errorf("m-R6-4 regression: warned about truncation even though final_count == pre_truncate_count (nothing was dropped): log=%q",
				buf.String())
		}
	})
}

// TestStore_FinalizeDraft_Notes_CumulativeCapTruncates is PR #152 round 6
// Major finding M-R6-2 guarantee B's verification: repeated enrich calls
// against the SAME draft must never grow Notes past
// outcome.MaxNotesTotalRunes, existing content must survive intact, and
// slog.Warn must fire exactly when — and only when — real truncation
// happens (mirrors TestStore_FinalizeDraft_RelatedRuleIDs_CumulativeCapTruncates's
// pattern for the analogous related_rule_ids cap).
func TestStore_FinalizeDraft_Notes_CumulativeCapTruncates(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()
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

	chunk := strings.Repeat("a", 2000)

	// Call 1: existing empty -> direct write, 2000 runes, well under the
	// 5,000-rune cap. Must not warn.
	buf := captureSlogWarn(t)
	got, err := store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{Result: "unknown", Notes: chunk})
	if err != nil {
		t.Fatalf("FinalizeDraft call 1: %v", err)
	}
	if got.Notes != chunk {
		t.Fatalf("call 1: Notes = %d runes, want the chunk written directly (existing was empty)", len([]rune(got.Notes)))
	}
	if strings.Contains(buf.String(), "notes exceeded cumulative cap") {
		t.Fatalf("call 1: unexpected truncation warn under the cap: log=%q", buf.String())
	}

	// Call 2: existing=2000, +separator(2)+2000 = 4002, still under the cap.
	buf = captureSlogWarn(t)
	got, err = store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{Result: "unknown", Notes: chunk})
	if err != nil {
		t.Fatalf("FinalizeDraft call 2: %v", err)
	}
	wantAfter2 := chunk + "\n\n" + chunk
	if got.Notes != wantAfter2 {
		t.Fatalf("call 2: Notes mismatch, got %d runes want %d runes", len([]rune(got.Notes)), len([]rune(wantAfter2)))
	}
	if strings.Contains(buf.String(), "notes exceeded cumulative cap") {
		t.Fatalf("call 2: unexpected truncation warn under the cap: log=%q", buf.String())
	}

	// Call 3: existing=4002, +separator(2)+2000 = 6004, EXCEEDS the 5,000
	// cap. Both prior chunks (4002 runes) plus their separator must survive
	// completely intact; only part of the third chunk is admitted.
	buf = captureSlogWarn(t)
	got, err = store.FinalizeDraft(ctx, draft.ID, outcome.CreateOutcomeParams{Result: "unknown", Notes: chunk})
	if err != nil {
		t.Fatalf("FinalizeDraft call 3: %v", err)
	}
	if len([]rune(got.Notes)) != outcome.MaxNotesTotalRunes {
		t.Fatalf("call 3: len(Notes) = %d, want exactly the cap %d — cumulative growth was NOT bounded",
			len([]rune(got.Notes)), outcome.MaxNotesTotalRunes)
	}
	wantPrefix := wantAfter2 + "\n\n"
	if !strings.HasPrefix(got.Notes, wantPrefix) {
		t.Fatalf("call 3: existing content + separator must survive completely intact as a prefix; got %q", got.Notes)
	}
	remaining := outcome.MaxNotesTotalRunes - len([]rune(wantPrefix))
	wantThirdChunkPrefix := chunk[:remaining]
	if !strings.HasSuffix(got.Notes, wantThirdChunkPrefix) {
		t.Errorf("call 3: want the surviving suffix to be exactly the first %d runes of the third chunk", remaining)
	}
	if !strings.Contains(buf.String(), "notes exceeded cumulative cap") {
		t.Errorf("call 3: expected a truncation warn (the merge WAS truncated), got none: log=%q", buf.String())
	}

	// Re-read from a fresh query, not just the RETURNING clause.
	reread, err := store.GetOutcomeByID(ctx, draft.ID, &wsID)
	if err != nil {
		t.Fatalf("GetOutcomeByID: %v", err)
	}
	if len([]rune(reread.Notes)) != outcome.MaxNotesTotalRunes {
		t.Errorf("reread len(Notes) = %d, want %d (persisted value must match RETURNING)",
			len([]rune(reread.Notes)), outcome.MaxNotesTotalRunes)
	}
}

// insertDraftWithNotes inserts an 'unknown' draft row directly via SQL,
// letting tests seed a pre-existing Notes value that already exceeds
// outcome.MaxNotesTotalRunes — a state only reachable via data written
// before that cap existed (see outcome.CapNotesTotal's doc comment),
// mirroring insertDraftWithNotesAndRelatedRuleIDs's role for
// related_rule_ids above.
func insertDraftWithNotes(
	ctx context.Context, t *testing.T, pool *pgxpool.Pool,
	id, wsID, entityID uuid.UUID, entityType, notes string,
) {
	t.Helper()
	_, err := pool.Exec(
		ctx,
		`INSERT INTO outcomes (id, workspace_id, entity_type, entity_id, result, notes) VALUES ($1, $2, $3, $4, 'unknown', $5)`,
		id, wsID, entityType, entityID, notes,
	)
	if err != nil {
		t.Fatalf("insert draft with notes: %v", err)
	}
}

// TestStore_FinalizeDraft_Notes_ExistingOverCap_NeverDropsExisting is the
// Notes-field twin of
// TestStore_FinalizeDraft_RelatedRuleIDs_ExistingOverCap_NeverDropsExisting
// above, exercising outcome.CapNotesTotal's "existing already over cap"
// branch through the REAL Postgres SQL (the pure-function equivalent is
// already covered by TestCapNotesTotal, internal/outcome/cap_test.go — this
// proves the SQL glue calls it correctly end to end).
func TestStore_FinalizeDraft_Notes_ExistingOverCap_NeverDropsExisting(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := outcome.NewStore(pool, &wsID)
	ctx := context.Background()

	existingNotes := strings.Repeat("x", outcome.MaxNotesTotalRunes+500) // 5,500 > cap 5,000

	buf := captureSlogWarn(t)
	draftID := uuid.New()
	entityID := uuid.New()
	insertDraftWithNotes(ctx, t, pool, draftID, wsID, entityID, "task", existingNotes)

	got, err := store.FinalizeDraft(ctx, draftID, outcome.CreateOutcomeParams{
		Result: "success",
		Notes:  "a fresh enrich call that should be entirely dropped",
	})
	if err != nil {
		t.Fatalf("FinalizeDraft: %v", err)
	}
	if got.Notes != existingNotes {
		t.Fatalf("Notes = %d runes, want the existing %d runes preserved EXACTLY, with the new segment dropped entirely",
			len([]rune(got.Notes)), len([]rune(existingNotes)))
	}
	if !strings.Contains(buf.String(), "notes exceeded cumulative cap") {
		t.Errorf("expected a truncation warn (the new segment WAS dropped), got none: log=%q", buf.String())
	}
}
