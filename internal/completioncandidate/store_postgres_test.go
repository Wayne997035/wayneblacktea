//go:build integration

package completioncandidate_test

import (
	"context"
	"flag"
	"log"
	"os"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

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
	// postgres:16-alpine is intentional — completioncandidate uses minimal DDL,
	// not the full migration stack (no pgvector extension needed).
	c, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
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

	if err := applyPgSchema(ctx, pool); err != nil {
		log.Printf("apply schema: %v", err)
		return 1
	}

	testPgPool = pool
	return m.Run()
}

// openTestPgPool returns the package-level singleton pool initialised in TestMain.
// Skip with -short flag: testcontainers requires Docker and adds ~5-10 s.
func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return testPgPool
}

// applyPgSchema creates the minimal schema needed by PgStore tests.
func applyPgSchema(ctx context.Context, pool *pgxpool.Pool) error {
	const ddl = `
	CREATE TABLE IF NOT EXISTS tasks (
		id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
		workspace_id UUID,
		title        TEXT        NOT NULL DEFAULT '',
		status       TEXT        NOT NULL DEFAULT 'pending',
		updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
	);

	CREATE TABLE IF NOT EXISTS work_sessions (
		id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
		workspace_id UUID        NOT NULL,
		repo_name    TEXT        NOT NULL DEFAULT '',
		status       TEXT        NOT NULL DEFAULT 'in_progress',
		completed_at TIMESTAMPTZ
	);

	CREATE TABLE IF NOT EXISTS work_session_tasks (
		session_id UUID NOT NULL,
		task_id    UUID NOT NULL,
		role       TEXT NOT NULL DEFAULT 'primary',
		created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		PRIMARY KEY (session_id, task_id)
	);

	CREATE TABLE IF NOT EXISTS activity_log (
		id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
		workspace_id UUID,
		actor        TEXT        NOT NULL DEFAULT 'system',
		action       TEXT        NOT NULL,
		notes        TEXT,
		created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
	);

	CREATE TABLE IF NOT EXISTS completion_candidates (
		id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
		workspace_id       UUID,
		task_id            UUID        NOT NULL,
		repo_name          TEXT,
		reason             TEXT        NOT NULL,
		evidence_refs      TEXT[]      NOT NULL DEFAULT '{}',
		confidence         TEXT        NOT NULL CHECK (confidence IN ('high','medium','low')),
		suggested_artifact TEXT,
		status             TEXT        NOT NULL DEFAULT 'pending'
		                               CHECK (status IN ('pending','accepted','rejected','auto_applied')),
		detected_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
		resolved_at        TIMESTAMPTZ
	);
	CREATE UNIQUE INDEX IF NOT EXISTS idx_completion_candidates_task_reason
		ON completion_candidates(task_id, reason);
	`
	_, err := pool.Exec(ctx, ddl)
	return err
}

// insertPgTask inserts a minimal tasks row for PG tests.
func insertPgTask(t *testing.T, pool *pgxpool.Pool, wsID uuid.UUID, taskID uuid.UUID, status string, updatedAt time.Time) {
	t.Helper()
	const q = `INSERT INTO tasks (id, workspace_id, title, status, updated_at)
		VALUES ($1,$2,'test task',$3,$4)`
	if _, err := pool.Exec(context.Background(), q, taskID, wsID, status, updatedAt); err != nil {
		t.Fatalf("insertPgTask %s: %v", taskID, err)
	}
}

// insertPgSession inserts a work_sessions row for PG tests.
func insertPgSession(t *testing.T, pool *pgxpool.Pool, wsID, sessID uuid.UUID, completedAt time.Time) {
	t.Helper()
	const q = `INSERT INTO work_sessions (id, workspace_id, repo_name, status, completed_at)
		VALUES ($1,$2,'test-repo','completed',$3)`
	if _, err := pool.Exec(context.Background(), q, sessID, wsID, completedAt); err != nil {
		t.Fatalf("insertPgSession %s: %v", sessID, err)
	}
}

// insertPgSessionTask links a task to a session in work_session_tasks.
func insertPgSessionTask(t *testing.T, pool *pgxpool.Pool, sessID, taskID uuid.UUID) {
	t.Helper()
	const q = `INSERT INTO work_session_tasks (session_id, task_id) VALUES ($1,$2)`
	if _, err := pool.Exec(context.Background(), q, sessID, taskID); err != nil {
		t.Fatalf("insertPgSessionTask sess=%s task=%s: %v", sessID, taskID, err)
	}
}

// TestCandidateStore_PG_DetectStaleInProgress verifies that DetectAndUpsert
// creates a candidate for an in_progress task stale for > threshold hours.
func TestCandidateStore_PG_DetectStaleInProgress(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := completioncandidate.NewPgStore(pool, &wsID)
	ctx := context.Background()

	// Insert a task that was last updated 25 hours ago → stale for default 24h threshold.
	taskID := uuid.New()
	staleAt := time.Now().UTC().Add(-25 * time.Hour)
	insertPgTask(t, pool, wsID, taskID, "in_progress", staleAt)

	candidates, err := store.DetectAndUpsert(ctx, completioncandidate.DetectParams{
		WorkspaceID:    &wsID,
		StaleThreshold: 24 * time.Hour,
		LookbackDays:   7,
	})
	if err != nil {
		t.Fatalf("DetectAndUpsert: %v", err)
	}

	// Find the stale_in_progress candidate for our task.
	found := false
	for _, c := range candidates {
		if c.TaskID == taskID && c.Reason == completioncandidate.ReasonStaleInProgress {
			found = true
			if c.Confidence != completioncandidate.ConfidenceMedium {
				t.Errorf("confidence: got %s, want medium", c.Confidence)
			}
			if c.Status != completioncandidate.StatusPending {
				t.Errorf("status: got %s, want pending", c.Status)
			}
		}
	}
	if !found {
		t.Errorf("expected stale_in_progress candidate for task %s, not found in %d candidates", taskID, len(candidates))
	}
}

// TestCandidateStore_PG_FinishWorkGap verifies that DetectAndUpsert creates a
// candidate when a completed work session has a still-open linked task.
func TestCandidateStore_PG_FinishWorkGap(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := completioncandidate.NewPgStore(pool, &wsID)
	ctx := context.Background()

	// Insert task with status=in_progress (not completed).
	taskID := uuid.New()
	insertPgTask(t, pool, wsID, taskID, "in_progress", time.Now().UTC().Add(-1*time.Hour))

	// Insert completed session from 2 days ago and link the task.
	sessID := uuid.New()
	insertPgSession(t, pool, wsID, sessID, time.Now().UTC().Add(-48*time.Hour))
	insertPgSessionTask(t, pool, sessID, taskID)

	candidates, err := store.DetectAndUpsert(ctx, completioncandidate.DetectParams{
		WorkspaceID:    &wsID,
		StaleThreshold: 24 * time.Hour,
		LookbackDays:   7,
	})
	if err != nil {
		t.Fatalf("DetectAndUpsert: %v", err)
	}

	found := false
	for _, c := range candidates {
		if c.TaskID == taskID && c.Reason == completioncandidate.ReasonFinishWorkGap {
			found = true
			if c.Confidence != completioncandidate.ConfidenceHigh {
				t.Errorf("confidence: got %s, want high", c.Confidence)
			}
		}
	}
	if !found {
		t.Errorf("expected finish_work_gap candidate for task %s, not found in %d candidates", taskID, len(candidates))
	}
}

// TestCandidateStore_PG_UpsertAndList verifies the basic round-trip on Postgres.
func TestCandidateStore_PG_UpsertAndList(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := completioncandidate.NewPgStore(pool, &wsID)
	ctx := context.Background()

	taskID := uuid.New()
	c, err := store.UpsertCandidate(ctx, completioncandidate.UpsertParams{
		WorkspaceID:  &wsID,
		TaskID:       taskID,
		Reason:       completioncandidate.ReasonStaleInProgress,
		Confidence:   completioncandidate.ConfidenceMedium,
		EvidenceRefs: []string{"pg evidence"},
	})
	if err != nil {
		t.Fatalf("UpsertCandidate: %v", err)
	}
	if c.TaskID != taskID {
		t.Errorf("task_id mismatch: got %s, want %s", c.TaskID, taskID)
	}

	list, err := store.ListPendingCandidates(ctx, &wsID)
	if err != nil {
		t.Fatalf("ListPendingCandidates: %v", err)
	}
	if len(list) < 1 {
		t.Errorf("expected at least 1 pending candidate, got %d", len(list))
	}
}

// TestCandidateStore_PG_PruneResolved verifies PruneResolved deletes old resolved rows.
func TestCandidateStore_PG_PruneResolved(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := completioncandidate.NewPgStore(pool, &wsID)
	ctx := context.Background()

	taskID := uuid.New()
	c, err := store.UpsertCandidate(ctx, completioncandidate.UpsertParams{
		WorkspaceID:  &wsID,
		TaskID:       taskID,
		Reason:       completioncandidate.ReasonStaleInProgress,
		Confidence:   completioncandidate.ConfidenceMedium,
		EvidenceRefs: []string{"pg prune test"},
	})
	if err != nil {
		t.Fatalf("UpsertCandidate: %v", err)
	}

	// Manually mark resolved 31 days ago.
	oldTS := time.Now().UTC().AddDate(0, 0, -31)
	_, err = pool.Exec(ctx,
		`UPDATE completion_candidates SET status='rejected', resolved_at=$1 WHERE id=$2`,
		oldTS, c.ID,
	)
	if err != nil {
		t.Fatalf("manual resolve: %v", err)
	}

	n, err := store.PruneResolved(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("PruneResolved: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row deleted, got %d", n)
	}
}
