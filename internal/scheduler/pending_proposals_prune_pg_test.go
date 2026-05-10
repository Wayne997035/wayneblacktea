package scheduler

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// schedulerSkipMigrations are .up.sql files the prune-test runner MUST skip;
// matches the proposal package's batchSkipMigrations.
var schedulerSkipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true, // psql `\set` metacommand
}

// openSchedulerTestPgPool starts a throwaway Postgres container with full
// migrations applied and returns a pgxpool ready for use. The container is
// terminated and the pool is closed via t.Cleanup. Skips with -short.
func openSchedulerTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}

	ctx := context.Background()
	container, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_sched_test"),
		tcpostgres.WithUsername("wbt"),
		tcpostgres.WithPassword("wbt"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if tErr := container.Terminate(ctx); tErr != nil {
			t.Logf("terminate container: %v", tErr)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	applyAllUpMigrations(t, ctx, pool)
	return pool
}

func applyAllUpMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	entries, err := migrationfs.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded migrations dir: %v", err)
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
		if schedulerSkipMigrations[name] {
			continue
		}
		body, err := migrationfs.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}

// TestScheduler_DailyPendingProposalsPrune_DeletesOnlyExpiredRows seeds the
// pending_proposals table with a mix of in-window + out-of-window rows then
// runs the prune. Asserts:
//   - resolved (accepted/rejected) rows older than 90 days are deleted
//   - pending decision rows older than 180 days are deleted
//   - other pending types older than 180 days are KEPT (user intent)
//   - in-window rows are KEPT
func TestScheduler_DailyPendingProposalsPrune_DeletesOnlyExpiredRows(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()

	// Seed rows with explicit created_at / resolved_at so we can assert
	// deterministic prune behaviour. Helper builds INSERT for clarity.
	type seed struct {
		id         uuid.UUID
		typ        string
		status     string
		createdAt  time.Time
		resolvedAt *time.Time
		shouldKeep bool
		label      string
	}
	now := time.Now().UTC()
	resolved100Days := now.AddDate(0, 0, -100)    // outside 90d
	resolved10Days := now.AddDate(0, 0, -10)      // inside 90d
	pendingDecision200 := now.AddDate(0, 0, -200) // outside 180d
	pendingDecision30 := now.AddDate(0, 0, -30)   // inside 180d
	pendingGoal400 := now.AddDate(0, 0, -400)     // outside 180d but type=goal MUST be kept

	rt100 := resolved100Days
	rt10 := resolved10Days

	seeds := []seed{
		{uuid.New(), "decision", "accepted", resolved100Days, &rt100, false, "accepted decision >90d"},
		{uuid.New(), "concept", "rejected", resolved100Days, &rt100, false, "rejected concept >90d"},
		{uuid.New(), "decision", "accepted", resolved10Days, &rt10, true, "accepted decision <90d"},
		{uuid.New(), "decision", "pending", pendingDecision200, nil, false, "pending decision >180d"},
		{uuid.New(), "decision", "pending", pendingDecision30, nil, true, "pending decision <180d"},
		{uuid.New(), "goal", "pending", pendingGoal400, nil, true, "pending goal >180d (user intent)"},
		{uuid.New(), "concept", "pending", pendingGoal400, nil, true, "pending concept >180d (user intent)"},
	}

	for _, s := range seeds {
		var resolvedArg any
		if s.resolvedAt != nil {
			resolvedArg = *s.resolvedAt
		}
		_, err := pool.Exec(ctx, `INSERT INTO pending_proposals
			(id, type, payload, status, created_at, resolved_at)
			VALUES ($1, $2, '{}'::jsonb, $3, $4, $5)`,
			s.id, s.typ, s.status, s.createdAt, resolvedArg)
		if err != nil {
			t.Fatalf("seed %s: %v", s.label, err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", s.id)
		})
	}

	sc := &Scheduler{disciplinePool: pool}
	sc.runDailyPendingProposalsPrune()

	// Assert each seed: kept rows still exist, dropped rows are gone.
	for _, s := range seeds {
		var count int
		err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM pending_proposals WHERE id = $1", s.id).Scan(&count)
		if err != nil {
			t.Fatalf("count %s: %v", s.label, err)
		}
		if s.shouldKeep && count != 1 {
			t.Errorf("%s: count = %d, want 1 (should be kept)", s.label, count)
		}
		if !s.shouldKeep && count != 0 {
			t.Errorf("%s: count = %d, want 0 (should be deleted)", s.label, count)
		}
	}
}

// TestScheduler_PendingProposalsPrune_EmptyTableNoPanic verifies the prune
// query is safe to run against an empty table — production may go days with
// no rows to drop.
func TestScheduler_PendingProposalsPrune_EmptyTableNoPanic(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	sc := &Scheduler{disciplinePool: pool}
	// Must not panic on zero-row delete.
	sc.runDailyPendingProposalsPrune()
}
