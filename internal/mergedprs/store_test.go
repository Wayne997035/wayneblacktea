package mergedprs_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/mergedprs"

	_ "modernc.org/sqlite"
)

// openTestDB opens an in-memory SQLite DB with the merged_prs_observed
// schema, mirroring migrations/000052_merged_prs_observed.up.sql. Follows
// the same minimal-DDL-in-test pattern as internal/completioncandidate.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const schema = `
		CREATE TABLE merged_prs_observed (
			id           TEXT PRIMARY KEY,
			workspace_id TEXT,
			repo         TEXT NOT NULL,
			url          TEXT NOT NULL,
			head_ref     TEXT,
			title        TEXT,
			body_excerpt TEXT,
			merged_at    TEXT NOT NULL,
			observed_at  TEXT NOT NULL
		);
		CREATE UNIQUE INDEX idx_merged_prs_observed_url ON merged_prs_observed(url);
	`
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

// countRows returns the row count for the given url.
func countRows(t *testing.T, db *sql.DB, url string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*) FROM merged_prs_observed WHERE url = ?`, url,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestSQLiteStore_UpsertIdempotent verifies that two Upserts with the same
// url result in exactly one row and observed_at is refreshed (not
// duplicated) — mirrors TestPgStore_UpsertIdempotent.
func TestSQLiteStore_UpsertIdempotent(t *testing.T) {
	db := openTestDB(t)
	store := mergedprs.NewSQLiteStore(db, "")
	ctx := context.Background()

	const url = "https://github.com/o/r/pull/901"
	p := mergedprs.UpsertParams{
		Repo:     "o/r",
		URL:      url,
		HeadRef:  "feature/x",
		Title:    "feat: x",
		MergedAt: time.Now().UTC(),
	}

	if err := store.Upsert(ctx, p); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}
	if n := countRows(t, db, url); n != 1 {
		t.Errorf("after first: count = %d, want 1", n)
	}
	var firstObserved string
	if err := db.QueryRowContext(ctx, `SELECT observed_at FROM merged_prs_observed WHERE url=?`, url).Scan(&firstObserved); err != nil {
		t.Fatalf("first observed_at: %v", err)
	}

	// Sleep enough for the millisecond-resolution timestamp to tick.
	time.Sleep(5 * time.Millisecond)

	if err := store.Upsert(ctx, p); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	if n := countRows(t, db, url); n != 1 {
		t.Errorf("after second: count = %d, want 1 (idempotent on url)", n)
	}
	var secondObserved string
	if err := db.QueryRowContext(ctx, `SELECT observed_at FROM merged_prs_observed WHERE url=?`, url).Scan(&secondObserved); err != nil {
		t.Fatalf("second observed_at: %v", err)
	}
	if secondObserved <= firstObserved {
		t.Errorf("observed_at not refreshed: first=%v second=%v", firstObserved, secondObserved)
	}
}

// TestSQLiteStore_PruneOlderThan verifies the TTL job: stale rows are
// deleted, fresh rows survive — mirrors TestPgStore_PruneOlderThan.
func TestSQLiteStore_PruneOlderThan(t *testing.T) {
	db := openTestDB(t)
	store := mergedprs.NewSQLiteStore(db, "")
	ctx := context.Background()

	freshURL := "https://github.com/o/r/pull/2001"
	staleURL := "https://github.com/o/r/pull/2002"

	if err := store.Upsert(ctx, mergedprs.UpsertParams{
		Repo: "o/r", URL: freshURL, MergedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("fresh upsert: %v", err)
	}
	if err := store.Upsert(ctx, mergedprs.UpsertParams{
		Repo: "o/r", URL: staleURL, MergedAt: time.Now().UTC().Add(-32 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("stale upsert: %v", err)
	}
	// Backdate the stale row's observed_at so the 30-day prune cutoff hits it
	// — Upsert's default observed_at=now would otherwise mark both fresh.
	staleObservedAt := time.Now().UTC().Add(-31 * 24 * time.Hour).Format("2006-01-02T15:04:05.000Z07:00")
	if _, err := db.ExecContext(
		ctx,
		`UPDATE merged_prs_observed SET observed_at = ? WHERE url = ?`, staleObservedAt, staleURL,
	); err != nil {
		t.Fatalf("backdate stale: %v", err)
	}

	deleted, err := store.PruneOlderThan(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (stale row only)", deleted)
	}
	if n := countRows(t, db, freshURL); n != 1 {
		t.Errorf("fresh row count = %d, want 1 (must survive prune)", n)
	}
	if n := countRows(t, db, staleURL); n != 0 {
		t.Errorf("stale row count = %d, want 0 (must be pruned)", n)
	}
}

// TestSQLiteStore_RecentObservedSince verifies the read path returns only
// rows observed at-or-after the since cutoff — mirrors
// TestPgStore_RecentObservedSince.
func TestSQLiteStore_RecentObservedSince(t *testing.T) {
	db := openTestDB(t)
	store := mergedprs.NewSQLiteStore(db, "")
	ctx := context.Background()

	urls := []string{"https://github.com/o/r/pull/30", "https://github.com/o/r/pull/31"}
	for _, u := range urls {
		if err := store.Upsert(ctx, mergedprs.UpsertParams{
			Repo: "o/r", URL: u, MergedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("upsert %s: %v", u, err)
		}
	}

	got, err := store.RecentObservedSince(ctx, time.Now().UTC().Add(-1*time.Hour), nil)
	if err != nil {
		t.Fatalf("RecentObservedSince: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d rows, want 2", len(got))
	}

	// Future cutoff → empty.
	got2, err := store.RecentObservedSince(ctx, time.Now().UTC().Add(1*time.Hour), nil)
	if err != nil {
		t.Fatalf("future since: %v", err)
	}
	if len(got2) != 0 {
		t.Errorf("future since: got %d rows, want 0", len(got2))
	}
}

// TestSQLiteStore_RecentObservedSince_WorkspaceIsolation verifies that a
// store scoped to workspace B does not see rows upserted by a store scoped
// to workspace A, even sharing the same underlying *sql.DB.
func TestSQLiteStore_RecentObservedSince_WorkspaceIsolation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	const wsA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const wsB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	storeA := mergedprs.NewSQLiteStore(db, wsA)
	storeB := mergedprs.NewSQLiteStore(db, wsB)

	if err := storeA.Upsert(ctx, mergedprs.UpsertParams{
		Repo: "o/r", URL: "https://github.com/o/r/pull/50", MergedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert (wsA): %v", err)
	}

	gotB, err := storeB.RecentObservedSince(ctx, time.Now().UTC().Add(-1*time.Hour), nil)
	if err != nil {
		t.Fatalf("RecentObservedSince (wsB): %v", err)
	}
	if len(gotB) != 0 {
		t.Errorf("cross-workspace: wsB store must see 0 rows from wsA, got %d", len(gotB))
	}

	gotA, err := storeA.RecentObservedSince(ctx, time.Now().UTC().Add(-1*time.Hour), nil)
	if err != nil {
		t.Fatalf("RecentObservedSince (wsA): %v", err)
	}
	if len(gotA) != 1 {
		t.Errorf("wsA store must see 1 row, got %d", len(gotA))
	}
}
