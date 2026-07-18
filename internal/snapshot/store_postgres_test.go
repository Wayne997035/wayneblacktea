package snapshot_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// openTestPgPool returns the package-level singleton pool initialised in
// TestMain. Skip with -short: testcontainers requires Docker and adds ~5-10s.
func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return testPgPool
}

// insertSnapshotFixture inserts a project_status_snapshots row directly via
// SQL rather than through Store.Write. Write's source_decision_ids handling
// is broken against real Postgres (json.Marshal'd []byte passed into a
// native UUID[] column — "malformed array literal" from pgx, discovered by
// this test's first draft; see PruneOlderThan discovered-but-not-touched
// note). Bypassing Write here keeps this commit's scope to the pruner only.
func insertSnapshotFixture(t *testing.T, pool *pgxpool.Pool, wsID uuid.UUID, slug string, generatedAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	const q = `INSERT INTO project_status_snapshots
		(id, slug, workspace_id, generated_at, source)
		VALUES ($1, $2, $3, $4, 'auto-status-snapshot')`
	if _, err := pool.Exec(context.Background(), q, id, slug, wsID, generatedAt); err != nil {
		t.Fatalf("insert snapshot fixture: %v", err)
	}
	return id
}

func snapshotExists(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM project_status_snapshots WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatalf("count project_status_snapshots: %v", err)
	}
	return n == 1
}

// TestStore_PruneOlderThan_Expired verifies that a snapshot older than the
// cutoff is deleted.
func TestStore_PruneOlderThan_Expired(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := snapshot.NewStore(pool, &wsID)
	ctx := context.Background()

	id := insertSnapshotFixture(t, pool, wsID, "expired-slug", time.Now().Add(-200*24*time.Hour))

	cutoff := time.Now().Add(-180 * 24 * time.Hour)
	n, err := store.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if n < 1 {
		t.Errorf("rows deleted = %d, want >= 1", n)
	}
	if snapshotExists(t, pool, id) {
		t.Error("expired snapshot survived prune")
	}
}

// TestStore_PruneOlderThan_NotExpired verifies that a fresh snapshot is NOT
// deleted.
func TestStore_PruneOlderThan_NotExpired(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := snapshot.NewStore(pool, &wsID)
	ctx := context.Background()

	id := insertSnapshotFixture(t, pool, wsID, "fresh-slug", time.Now().Add(-5*24*time.Hour))

	cutoff := time.Now().Add(-180 * 24 * time.Hour)
	if _, err := store.PruneOlderThan(ctx, cutoff); err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if !snapshotExists(t, pool, id) {
		t.Error("fresh snapshot was pruned")
	}
}

// TestStore_PruneOlderThan_Boundary verifies the cutoff comparison is
// strict-less-than: a snapshot exactly at the cutoff survives, one 1 second
// before the cutoff is deleted.
func TestStore_PruneOlderThan_Boundary(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := snapshot.NewStore(pool, &wsID)
	ctx := context.Background()

	cutoff := time.Now().Add(-180 * 24 * time.Hour)

	atCutoff := insertSnapshotFixture(t, pool, wsID, "at-cutoff-slug", cutoff)
	justBefore := insertSnapshotFixture(t, pool, wsID, "just-before-cutoff-slug", cutoff.Add(-1*time.Second))

	if _, err := store.PruneOlderThan(ctx, cutoff); err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if !snapshotExists(t, pool, atCutoff) {
		t.Error("snapshot exactly at cutoff was pruned (want: survive)")
	}
	if snapshotExists(t, pool, justBefore) {
		t.Error("snapshot just before cutoff survived (want: pruned)")
	}
}
