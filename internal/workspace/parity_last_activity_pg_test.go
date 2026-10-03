//go:build integration

package workspace_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
)

// [F1003-06] PG side of the two-backend parity pair for "UpsertRepo stamps
// last_activity to now on every call" — see
// internal/storage/sqlite/workspace_last_activity_test.go for the SQLite
// twin. Before the fix, sql/queries/workspace.sql's UpsertRepo bound
// last_activity to the never-set Go field `$8`/EXCLUDED.last_activity (both
// INSERT VALUES and ON CONFLICT SET), so every PG UpsertRepo call wrote
// NULL. Fixed to use NOW() unconditionally in both branches (same pattern
// as the adjacent updated_at = NOW()).
//
// Uses the same openTestPgPool harness as
// store_postgres_syncrepo_test.go / store_postgres_test.go — not a second PG
// test harness for this package.
func TestUpsertRepo_LastActivityStrictlyAdvances_PG(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := workspace.NewStore(pool, &wsID)

	repoName := "wbt-la-happy-pg"

	// INSERT branch: a brand-new repo's single call must itself produce a
	// non-NULL last_activity. A fix that only patches the ON CONFLICT SET
	// clause and leaves the INSERT VALUES list binding the old (now-removed)
	// LastActivity parameter would pass the "call twice" assertion below by
	// luck (the 2nd call's UPDATE branch overwrites the 1st call's bad NULL)
	// but fail this one-call check.
	first, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: repoName})
	if err != nil {
		t.Fatalf("first UpsertRepo (insert branch): %v", err)
	}
	t.Cleanup(func() {
		if _, delErr := pool.Exec(ctx, `DELETE FROM repos WHERE id = $1`, first.ID); delErr != nil {
			t.Logf("cleanup repo: %v", delErr)
		}
	})
	if !first.LastActivity.Valid {
		t.Fatal("first UpsertRepo (insert branch): last_activity is NULL, want non-NULL")
	}

	time.Sleep(10 * time.Millisecond) // ensure NOW() advances between calls

	second, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: repoName})
	if err != nil {
		t.Fatalf("second UpsertRepo (update branch): %v", err)
	}
	if !second.LastActivity.Valid {
		t.Fatal("second UpsertRepo (update branch): last_activity is NULL, want non-NULL")
	}
	if !second.LastActivity.Time.After(first.LastActivity.Time) {
		t.Fatalf("second last_activity (%v) is not strictly after first (%v)",
			second.LastActivity.Time, first.LastActivity.Time)
	}
}

// TestActiveRepos_OrdersByLastActivity_PG proves the fix drives real DESC
// ordering, not just "stops writing NULL": upserting repo B after repo A
// (both new) must sort B before A. If last_activity were still NULL for
// either, NULLS LAST would push it to the bottom regardless of insert
// order — this distinguishes "NULL" from "non-NULL but stale".
func TestActiveRepos_OrdersByLastActivity_PG(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := workspace.NewStore(pool, &wsID)

	stale, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: "wbt-la-stale-pg"})
	if err != nil {
		t.Fatalf("seed stale repo: %v", err)
	}
	t.Cleanup(func() {
		if _, delErr := pool.Exec(ctx, `DELETE FROM repos WHERE id = $1`, stale.ID); delErr != nil {
			t.Logf("cleanup stale repo: %v", delErr)
		}
	})

	time.Sleep(10 * time.Millisecond)

	fresh, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: "wbt-la-fresh-pg"})
	if err != nil {
		t.Fatalf("seed fresh repo: %v", err)
	}
	t.Cleanup(func() {
		if _, delErr := pool.Exec(ctx, `DELETE FROM repos WHERE id = $1`, fresh.ID); delErr != nil {
			t.Logf("cleanup fresh repo: %v", delErr)
		}
	})

	repos, err := store.ActiveRepos(ctx)
	if err != nil {
		t.Fatalf("ActiveRepos: %v", err)
	}
	idx := map[string]int{}
	for i, r := range repos {
		idx[r.Name] = i
	}
	freshIdx, ok := idx["wbt-la-fresh-pg"]
	if !ok {
		t.Fatal("wbt-la-fresh-pg missing from ActiveRepos result")
	}
	staleIdx, ok := idx["wbt-la-stale-pg"]
	if !ok {
		t.Fatal("wbt-la-stale-pg missing from ActiveRepos result")
	}
	if freshIdx >= staleIdx {
		t.Fatalf("wbt-la-fresh-pg (idx=%d) does not sort before wbt-la-stale-pg (idx=%d)", freshIdx, staleIdx)
	}
}
