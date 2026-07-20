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
// SQL rather than through Store.Write. It exists so the PruneOlderThan tests
// below can set up fixtures at a specific generated_at without depending on
// Write's own logic. Store.Write itself is exercised directly by
// TestStore_Write.
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

// uuidSlicesEqual reports whether a and b contain the same UUIDs in the same
// order. Postgres UUID[] preserves insertion order, so exact-order comparison
// is the correct round-trip check here.
func uuidSlicesEqual(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestStore_Write exercises Store.Write directly against real Postgres,
// covering the source_decision_ids UUID[] column that json.Marshal/Unmarshal
// previously wrote as a malformed array literal (pgx rejected it at the
// Postgres wire protocol level, not just at scan time). Both the two-ID case
// and the nil case (generator.go never sets SourceDecisionIDs today — see
// EnsureSnapshot) are covered so a regression to the old []byte encoding
// path fails loudly here instead of silently in the Saturday cron.
func TestStore_Write(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := snapshot.NewStore(pool, &wsID)
	ctx := context.Background()

	id1, id2 := uuid.New(), uuid.New()

	tests := []struct {
		name    string
		ids     []uuid.UUID
		wantIDs []uuid.UUID
	}{
		{
			name:    "two decision ids round-trip",
			ids:     []uuid.UUID{id1, id2},
			wantIDs: []uuid.UUID{id1, id2},
		},
		{
			name:    "nil decision ids (production shape - generator.go never sets this field)",
			ids:     nil,
			wantIDs: []uuid.UUID{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug := "write-test-" + uuid.NewString()
			snap, err := store.Write(ctx, snapshot.WriteParams{
				Slug:              slug,
				WorkspaceID:       &wsID,
				SprintSummary:     "sprint",
				GapAnalysis:       "gap",
				SotaCatchupPct:    42,
				PendingSummary:    "pending",
				SourceDecisionIDs: tt.ids,
			})
			if err != nil {
				t.Fatalf("Write: %v", err)
			}
			if !uuidSlicesEqual(snap.SourceDecisionIDs, tt.wantIDs) {
				t.Errorf("Write returned SourceDecisionIDs = %v, want %v", snap.SourceDecisionIDs, tt.wantIDs)
			}

			// Round-trip through LatestFresh confirms the UUID[] column was
			// actually persisted correctly by Postgres, not just accepted by
			// the driver on the way in.
			fresh, err := store.LatestFresh(ctx, slug, time.Minute)
			if err != nil {
				t.Fatalf("LatestFresh: %v", err)
			}
			if !uuidSlicesEqual(fresh.SourceDecisionIDs, tt.wantIDs) {
				t.Errorf("LatestFresh SourceDecisionIDs = %v, want %v", fresh.SourceDecisionIDs, tt.wantIDs)
			}
		})
	}
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
