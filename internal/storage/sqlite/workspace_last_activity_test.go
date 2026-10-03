package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/workspace"
)

// [F1003-06] SQLite side of the two-backend parity pair for "UpsertRepo
// stamps last_activity to now on every call" — see
// internal/workspace/parity_last_activity_pg_test.go for the PG twin.
// SQLite already stamped last_activity correctly before this sprint
// (internal/storage/sqlite/workspace.go's sqliteNowMillis()); this test
// pins that behaviour as a regression guard, not a bug fix — a future
// accidental revert of workspace.go's `now` binding must turn this red.
//
// SQLite stores last_activity as a millisecond-resolution TEXT timestamp
// (sqliteMillisLayout = "2006-01-02T15:04:05.000Z", internal/storage/sqlite/decision.go).
// Two calls inside the same millisecond would compare equal, not strictly
// greater, so this test sleeps past that resolution between calls — same
// requirement the PG twin documents for a different reason (wall-clock
// granularity, not storage format).
func TestWorkspaceStore_UpsertRepo_LastActivityStrictlyAdvances_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openWorkspaceStore(t, ":memory:", "")
	ctx := context.Background()

	repoName := "wbt-la-sqlite"

	// INSERT branch: a brand-new repo's first call must itself produce a
	// non-NULL last_activity — this exercises the VALUES clause, not just
	// the ON CONFLICT UPDATE SET branch.
	first, err := s.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: repoName})
	if err != nil {
		t.Fatalf("first UpsertRepo (insert branch): %v", err)
	}
	if !first.LastActivity.Valid {
		t.Fatal("first UpsertRepo (insert branch): last_activity is NULL, want non-NULL")
	}

	time.Sleep(15 * time.Millisecond) // > sqliteMillisLayout's ms resolution

	second, err := s.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: repoName})
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

// TestWorkspaceStore_ActiveRepos_OrdersByLastActivity_SQLite is the SQLite
// twin of the PG "ordering under NULLS LAST" exception path: upserting repo
// B after repo A must sort B first under ORDER BY last_activity DESC.
func TestWorkspaceStore_ActiveRepos_OrdersByLastActivity_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openWorkspaceStore(t, ":memory:", "")
	ctx := context.Background()

	if _, err := s.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: "wbt-la-stale-sqlite"}); err != nil {
		t.Fatalf("seed stale repo: %v", err)
	}
	time.Sleep(15 * time.Millisecond)
	if _, err := s.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: "wbt-la-fresh-sqlite"}); err != nil {
		t.Fatalf("seed fresh repo: %v", err)
	}

	repos, err := s.ActiveRepos(ctx)
	if err != nil {
		t.Fatalf("ActiveRepos: %v", err)
	}
	idx := map[string]int{}
	for i, r := range repos {
		idx[r.Name] = i
	}
	freshIdx, ok := idx["wbt-la-fresh-sqlite"]
	if !ok {
		t.Fatal("wbt-la-fresh-sqlite missing from ActiveRepos result")
	}
	staleIdx, ok := idx["wbt-la-stale-sqlite"]
	if !ok {
		t.Fatal("wbt-la-stale-sqlite missing from ActiveRepos result")
	}
	if freshIdx >= staleIdx {
		t.Fatalf("wbt-la-fresh-sqlite (idx=%d) does not sort before wbt-la-stale-sqlite (idx=%d)", freshIdx, staleIdx)
	}
}
