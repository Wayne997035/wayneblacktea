package mergedprs_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/mergedprs"
	"github.com/jackc/pgx/v5/pgxpool"
)

// openTestPgPool returns the package-level singleton pool initialised in
// TestMain. Skip with -short flag.
func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode")
	}
	return testPgPool
}

// truncate clears the merged_prs_observed table between test cases.
func truncate(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `TRUNCATE merged_prs_observed`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// TestPgStore_UpsertIdempotent verifies that two Upserts with the same url
// result in exactly one row and the observed_at is refreshed on the second
// call (not duplicated).
func TestPgStore_UpsertIdempotent(t *testing.T) {
	pool := openTestPgPool(t)
	store := mergedprs.NewPgStore(pool, nil)
	ctx := context.Background()
	truncate(t, pool)

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
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM merged_prs_observed WHERE url=$1`, url).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("after first: count = %d, want 1", n)
	}
	var firstObserved time.Time
	if err := pool.QueryRow(ctx, `SELECT observed_at FROM merged_prs_observed WHERE url=$1`, url).Scan(&firstObserved); err != nil {
		t.Fatalf("first observed_at: %v", err)
	}

	// Sleep enough for NOW() to tick; PG TIMESTAMPTZ has microsecond resolution.
	time.Sleep(20 * time.Millisecond)

	if err := store.Upsert(ctx, p); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM merged_prs_observed WHERE url=$1`, url).Scan(&n); err != nil {
		t.Fatalf("count2: %v", err)
	}
	if n != 1 {
		t.Errorf("after second: count = %d, want 1 (idempotent on url)", n)
	}
	var secondObserved time.Time
	if err := pool.QueryRow(ctx, `SELECT observed_at FROM merged_prs_observed WHERE url=$1`, url).Scan(&secondObserved); err != nil {
		t.Fatalf("second observed_at: %v", err)
	}
	if !secondObserved.After(firstObserved) {
		t.Errorf("observed_at not refreshed: first=%v second=%v", firstObserved, secondObserved)
	}
}

// TestPgStore_PruneOlderThan verifies the TTL job: stale rows are deleted,
// fresh rows survive.
func TestPgStore_PruneOlderThan(t *testing.T) {
	pool := openTestPgPool(t)
	store := mergedprs.NewPgStore(pool, nil)
	ctx := context.Background()
	truncate(t, pool)

	// Seed 1 fresh (today) + 1 stale (31 days ago).
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
	// Manually backdate the stale row's observed_at so the prune cutoff (30d)
	// hits it. Upsert's NOW() default would otherwise mark both as fresh.
	const backdateQ = `UPDATE merged_prs_observed
		SET observed_at = NOW() - INTERVAL '31 days'
		WHERE url = $1`
	if _, err := pool.Exec(ctx, backdateQ, staleURL); err != nil {
		t.Fatalf("backdate stale: %v", err)
	}

	deleted, err := store.PruneOlderThan(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1 (stale row only)", deleted)
	}

	var freshCount, staleCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM merged_prs_observed WHERE url=$1`, freshURL).Scan(&freshCount); err != nil {
		t.Fatalf("fresh count: %v", err)
	}
	if freshCount != 1 {
		t.Errorf("fresh row survived = %v, want 1 (must survive prune)", freshCount)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM merged_prs_observed WHERE url=$1`, staleURL).Scan(&staleCount); err != nil {
		t.Fatalf("stale count: %v", err)
	}
	if staleCount != 0 {
		t.Errorf("stale row survived = %v, want 0 (must be pruned)", staleCount)
	}
}

// TestPgStore_RecentObservedSince verifies the read path returns only rows
// observed at-or-after the since cutoff, ordered by observed_at DESC.
func TestPgStore_RecentObservedSince(t *testing.T) {
	pool := openTestPgPool(t)
	store := mergedprs.NewPgStore(pool, nil)
	ctx := context.Background()
	truncate(t, pool)

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
