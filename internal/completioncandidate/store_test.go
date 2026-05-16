package completioncandidate_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/google/uuid"

	_ "modernc.org/sqlite"
)

// openTestDB opens an in-memory SQLite DB with the completion_candidates schema.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := applySchema(db); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

// applySchema applies the minimum DDL needed for completioncandidate tests.
func applySchema(db *sql.DB) error {
	_, err := db.ExecContext(context.Background(), `
		CREATE TABLE IF NOT EXISTS tasks (
			id           TEXT PRIMARY KEY,
			workspace_id TEXT,
			title        TEXT NOT NULL DEFAULT '',
			status       TEXT NOT NULL DEFAULT 'pending',
			updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		);
		CREATE TABLE IF NOT EXISTS work_sessions (
			id           TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			repo_name    TEXT NOT NULL DEFAULT '',
			status       TEXT NOT NULL DEFAULT 'in_progress',
			completed_at TEXT
		);
		CREATE TABLE IF NOT EXISTS work_session_tasks (
			session_id TEXT NOT NULL,
			task_id    TEXT NOT NULL,
			role       TEXT NOT NULL DEFAULT 'primary',
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			PRIMARY KEY (session_id, task_id)
		);
		CREATE TABLE IF NOT EXISTS activity_log (
			id           TEXT PRIMARY KEY,
			workspace_id TEXT,
			actor        TEXT NOT NULL DEFAULT 'system',
			action       TEXT NOT NULL,
			notes        TEXT,
			created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		);
		CREATE TABLE IF NOT EXISTS completion_candidates (
			id                 TEXT PRIMARY KEY,
			workspace_id       TEXT,
			task_id            TEXT NOT NULL,
			repo_name          TEXT,
			reason             TEXT NOT NULL,
			evidence_refs      TEXT NOT NULL DEFAULT '[]',
			confidence         TEXT NOT NULL CHECK (confidence IN ('high','medium','low')),
			suggested_artifact TEXT,
			status             TEXT NOT NULL DEFAULT 'pending'
			                       CHECK (status IN ('pending','accepted','rejected','auto_applied')),
			detected_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
			resolved_at        TEXT
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_completion_candidates_task_reason
			ON completion_candidates(task_id, reason);
	`)
	return err
}

// insertTask seeds a task row for tests.
func insertTask(t *testing.T, db *sql.DB, id, status, updatedAt string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO tasks (id, title, status, updated_at) VALUES (?,?,?,?)`,
		id, "test task", status, updatedAt,
	)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}
}

// TestCandidateStore_UpsertAndList verifies basic upsert + list flow.
func TestCandidateStore_UpsertAndList(t *testing.T) {
	db := openTestDB(t)
	store := completioncandidate.NewSQLiteStore(db, "")
	ctx := context.Background()

	taskID := uuid.New()
	insertTask(t, db, taskID.String(), "in_progress", "2020-01-01T00:00:00Z")

	c, err := store.UpsertCandidate(ctx, completioncandidate.UpsertParams{
		TaskID:       taskID,
		Reason:       completioncandidate.ReasonStaleInProgress,
		Confidence:   completioncandidate.ConfidenceMedium,
		EvidenceRefs: []string{"stale since 2020-01-01"},
	})
	if err != nil {
		t.Fatalf("UpsertCandidate: %v", err)
	}
	if c.ID == uuid.Nil {
		t.Error("expected non-nil candidate ID")
	}
	if c.TaskID != taskID {
		t.Errorf("task_id: got %s, want %s", c.TaskID, taskID)
	}
	if c.Status != completioncandidate.StatusPending {
		t.Errorf("status: got %s, want pending", c.Status)
	}
	if len(c.EvidenceRefs) != 1 {
		t.Errorf("evidence_refs: got %d, want 1", len(c.EvidenceRefs))
	}

	// ListPendingCandidates should return the upserted row.
	list, err := store.ListPendingCandidates(ctx, nil)
	if err != nil {
		t.Fatalf("ListPendingCandidates: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 candidate, got %d", len(list))
	}
	if list[0].TaskID != taskID {
		t.Errorf("list[0].TaskID: got %s, want %s", list[0].TaskID, taskID)
	}
}

// TestCandidateStore_PruneResolved verifies that resolved rows older than the
// threshold are deleted by PruneResolved.
func TestCandidateStore_PruneResolved(t *testing.T) {
	db := openTestDB(t)
	store := completioncandidate.NewSQLiteStore(db, "")
	ctx := context.Background()

	taskID := uuid.New()
	insertTask(t, db, taskID.String(), "completed", "2020-01-01T00:00:00Z")

	// Upsert a candidate then manually set status=rejected and resolved_at to 31 days ago.
	c, err := store.UpsertCandidate(ctx, completioncandidate.UpsertParams{
		TaskID:       taskID,
		Reason:       completioncandidate.ReasonStaleInProgress,
		Confidence:   completioncandidate.ConfidenceMedium,
		EvidenceRefs: []string{"old evidence"},
	})
	if err != nil {
		t.Fatalf("UpsertCandidate: %v", err)
	}

	// Set resolved_at to 31 days ago and status=rejected directly in DB.
	oldTS := time.Now().UTC().AddDate(0, 0, -31).Format(time.RFC3339)
	if _, err := db.ExecContext(ctx,
		`UPDATE completion_candidates SET status='rejected', resolved_at=? WHERE id=?`,
		oldTS, c.ID.String(),
	); err != nil {
		t.Fatalf("manual resolve: %v", err)
	}

	n, err := store.PruneResolved(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("PruneResolved: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row deleted, got %d", n)
	}

	// Verify the row is gone.
	list, err := store.ListPendingCandidates(ctx, nil)
	if err != nil {
		t.Fatalf("ListPendingCandidates after prune: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 candidates after prune, got %d", len(list))
	}
}

// TestCandidateStore_UpsertIdempotent verifies that upserting the same
// (task_id, reason) twice results in exactly 1 row.
func TestCandidateStore_UpsertIdempotent(t *testing.T) {
	db := openTestDB(t)
	store := completioncandidate.NewSQLiteStore(db, "")
	ctx := context.Background()

	taskID := uuid.New()
	insertTask(t, db, taskID.String(), "in_progress", "2020-01-01T00:00:00Z")

	p := completioncandidate.UpsertParams{
		TaskID:       taskID,
		Reason:       completioncandidate.ReasonStaleInProgress,
		Confidence:   completioncandidate.ConfidenceMedium,
		EvidenceRefs: []string{"first upsert"},
	}

	c1, err := store.UpsertCandidate(ctx, p)
	if err != nil {
		t.Fatalf("first UpsertCandidate: %v", err)
	}

	p.EvidenceRefs = []string{"second upsert"}
	c2, err := store.UpsertCandidate(ctx, p)
	if err != nil {
		t.Fatalf("second UpsertCandidate: %v", err)
	}

	// The IDs should be the same — ON CONFLICT reuses the existing row.
	if c1.ID != c2.ID {
		t.Errorf("expected same ID on idempotent upsert: got %s and %s", c1.ID, c2.ID)
	}

	// Only 1 row should exist.
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM completion_candidates`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row, got %d", count)
	}

	// detected_at should be refreshed (c2.DetectedAt >= c1.DetectedAt).
	if c2.DetectedAt.Before(c1.DetectedAt) {
		t.Errorf("detected_at not refreshed: c2.DetectedAt=%v < c1.DetectedAt=%v", c2.DetectedAt, c1.DetectedAt)
	}
}

// TestCandidateStore_UpsertCandidate_RequiresTaskID verifies validation.
func TestCandidateStore_UpsertCandidate_RequiresTaskID(t *testing.T) {
	db := openTestDB(t)
	store := completioncandidate.NewSQLiteStore(db, "")
	ctx := context.Background()

	_, err := store.UpsertCandidate(ctx, completioncandidate.UpsertParams{
		TaskID:     uuid.Nil, // missing
		Reason:     completioncandidate.ReasonStaleInProgress,
		Confidence: completioncandidate.ConfidenceMedium,
	})
	if err == nil {
		t.Error("expected error for nil task_id, got nil")
	}
}

// TestCandidateStore_UpsertCandidate_InvalidReason verifies that unknown reasons
// are rejected before hitting the DB.
func TestCandidateStore_UpsertCandidate_InvalidReason(t *testing.T) {
	db := openTestDB(t)
	store := completioncandidate.NewSQLiteStore(db, "")
	ctx := context.Background()

	_, err := store.UpsertCandidate(ctx, completioncandidate.UpsertParams{
		TaskID:     uuid.New(),
		Reason:     "unknown_reason",
		Confidence: completioncandidate.ConfidenceMedium,
	})
	if err == nil {
		t.Error("expected error for unknown reason, got nil")
	}
}

// TestCandidateStore_WorkspaceIsolation verifies that a store scoped to
// workspace B cannot see candidates created by a store scoped to workspace A,
// even when both stores share the same underlying *sql.DB.
func TestCandidateStore_WorkspaceIsolation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	const wsA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const wsB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	storeA := completioncandidate.NewSQLiteStore(db, wsA)
	storeB := completioncandidate.NewSQLiteStore(db, wsB)

	// Seed a candidate using storeA — it will be tagged workspace_id = wsA.
	_, err := storeA.UpsertCandidate(ctx, completioncandidate.UpsertParams{
		TaskID:     uuid.New(),
		Reason:     completioncandidate.ReasonStaleInProgress,
		Confidence: completioncandidate.ConfidenceMedium,
	})
	if err != nil {
		t.Fatalf("UpsertCandidate (ws-A): %v", err)
	}

	// Workspace B store must see zero pending candidates from workspace A.
	bCandidates, err := storeB.ListPendingCandidates(ctx, nil)
	if err != nil {
		t.Fatalf("ListPendingCandidates (ws-B): %v", err)
	}
	if len(bCandidates) != 0 {
		t.Errorf("cross-workspace: ws-B store must see 0 candidates from ws-A, got %d", len(bCandidates))
	}

	// Workspace A store must see its own candidate.
	aCandidates, err := storeA.ListPendingCandidates(ctx, nil)
	if err != nil {
		t.Fatalf("ListPendingCandidates (ws-A): %v", err)
	}
	if len(aCandidates) != 1 {
		t.Errorf("ws-A store must see 1 candidate, got %d", len(aCandidates))
	}
}

// TestCandidateStore_PruneResolved_NotOldEnough verifies that rows younger than
// the threshold are NOT deleted.
func TestCandidateStore_PruneResolved_NotOldEnough(t *testing.T) {
	db := openTestDB(t)
	store := completioncandidate.NewSQLiteStore(db, "")
	ctx := context.Background()

	taskID := uuid.New()
	insertTask(t, db, taskID.String(), "completed", "2020-01-01T00:00:00Z")

	c, err := store.UpsertCandidate(ctx, completioncandidate.UpsertParams{
		TaskID:       taskID,
		Reason:       completioncandidate.ReasonStaleInProgress,
		Confidence:   completioncandidate.ConfidenceMedium,
		EvidenceRefs: []string{"recent"},
	})
	if err != nil {
		t.Fatalf("UpsertCandidate: %v", err)
	}

	// Set resolved_at to just 1 day ago — within the 30-day threshold.
	recentTS := time.Now().UTC().AddDate(0, 0, -1).Format(time.RFC3339)
	if _, err := db.ExecContext(ctx,
		`UPDATE completion_candidates SET status='rejected', resolved_at=? WHERE id=?`,
		recentTS, c.ID.String(),
	); err != nil {
		t.Fatalf("manual resolve: %v", err)
	}

	n, err := store.PruneResolved(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("PruneResolved: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 rows deleted (too recent), got %d", n)
	}
}
