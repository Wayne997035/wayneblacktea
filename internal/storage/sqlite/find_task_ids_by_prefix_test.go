package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// TestResolveTaskIDPrefix_SQLite is F0930-17's SQLite store-level test:
// FindTaskIDsByPrefix's 0/1/>=2-row semantics, plus workspace scoping.
// Mirrors TestResolveTaskIDPrefix_PG (internal/gtd) — same assertions, same
// shape, the dual-backend parity this feature requires. Each case is its own
// top-level function (prefixCaseSQLite*), same gocyclo-budget reasoning as
// the PG twin.
func TestResolveTaskIDPrefix_SQLite(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"unique 8-char prefix returns exactly one candidate", prefixCaseSQLiteUnique},
		{"two tasks sharing a prefix return both candidates, ordered by id", prefixCaseSQLiteCollide},
		{"prefix matching zero tasks returns an empty (not nil-error) slice", prefixCaseSQLiteZero},
		{"workspace scoping: a prefix unique within another workspace is invisible here", prefixCaseSQLiteWorkspaceScoping},
	}
	for _, tc := range cases {
		run := tc.run
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); run(t) })
	}
}

func prefixCaseSQLiteUnique(t *testing.T) {
	ctx := context.Background()
	s := openMem(t, "")
	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "prefix-sqlite-unique"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	prefix := task.ID.String()[:8]

	got, err := s.FindTaskIDsByPrefix(ctx, prefix, 3)
	if err != nil {
		t.Fatalf("FindTaskIDsByPrefix: %v", err)
	}
	if len(got) != 1 || got[0].ID != task.ID {
		t.Fatalf("got %+v, want exactly [%s]", got, task.ID)
	}
	if got[0].Title != "prefix-sqlite-unique" {
		t.Errorf("Title = %q, want %q", got[0].Title, "prefix-sqlite-unique")
	}
}

func prefixCaseSQLiteCollide(t *testing.T) {
	ctx := context.Background()
	s := openMem(t, "")
	taskA, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "prefix-sqlite-collide-a"})
	if err != nil {
		t.Fatalf("CreateTask A: %v", err)
	}
	taskB, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "prefix-sqlite-collide-b"})
	if err != nil {
		t.Fatalf("CreateTask B: %v", err)
	}

	sharedPrefix := taskA.ID.String()[:8]
	// Force B's id to share A's 8-char prefix but stay distinct overall
	// — same rationale and construction as forceSharedPrefix's PG twin
	// (internal/gtd/find_task_ids_by_prefix_pg_test.go): a real UUID
	// collision on 8 hex chars is a ~1-in-4-billion event, and red line
	// #9 (no FK constraints anywhere) makes a raw id UPDATE safe.
	forcedB := forceSharedPrefixSQLite(t, taskB.ID, sharedPrefix)
	if err := s.DB().ExecContext(ctx, `UPDATE tasks SET id = ?1 WHERE id = ?2`, forcedB.String(), taskB.ID.String()); err != nil {
		t.Fatalf("forcing task B id: %v", err)
	}

	got, err := s.FindTaskIDsByPrefix(ctx, sharedPrefix, 3)
	if err != nil {
		t.Fatalf("FindTaskIDsByPrefix: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2: %+v", len(got), got)
	}
	gotIDs := map[uuid.UUID]bool{got[0].ID: true, got[1].ID: true}
	if !gotIDs[taskA.ID] || !gotIDs[forcedB] {
		t.Errorf("candidates = %v, want {%s, %s}", got, taskA.ID, forcedB)
	}
	if got[0].ID.String() > got[1].ID.String() {
		t.Errorf("candidates not ordered by id ascending: %s then %s", got[0].ID, got[1].ID)
	}
}

func prefixCaseSQLiteZero(t *testing.T) {
	ctx := context.Background()
	s := openMem(t, "")
	got, err := s.FindTaskIDsByPrefix(ctx, "deadbeef", 3)
	if err != nil {
		t.Fatalf("FindTaskIDsByPrefix: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d candidates, want 0: %+v", len(got), got)
	}
}

// prefixCaseSQLiteWorkspaceScoping uses openFileStore (gtd_filtered_test.go,
// same package): a shared physical file with two workspace-scoped stores is
// required to prove the workspace_id predicate actually filters rows — two
// independent :memory: DBs (openMem) would still appear isolated even if the
// predicate were deleted.
func prefixCaseSQLiteWorkspaceScoping(t *testing.T) {
	ctx := context.Background()
	wsA := uuid.NewString()
	wsB := uuid.NewString()
	sharedPath := filepath.Join(t.TempDir(), "prefix-workspace-scoping.db")
	storeA := openFileStore(t, sharedPath, wsA)
	storeB := openFileStore(t, sharedPath, wsB)

	taskB, err := storeB.CreateTask(ctx, gtd.CreateTaskParams{Title: "prefix-sqlite-other-workspace"})
	if err != nil {
		t.Fatalf("CreateTask (workspace B): %v", err)
	}
	prefix := taskB.ID.String()[:8]

	gotA, err := storeA.FindTaskIDsByPrefix(ctx, prefix, 3)
	if err != nil {
		t.Fatalf("FindTaskIDsByPrefix (workspace A): %v", err)
	}
	if len(gotA) != 0 {
		t.Errorf("workspace A saw %d candidates for a workspace-B-only prefix, want 0: %+v", len(gotA), gotA)
	}

	// Own-workspace visibility regression guard, same reasoning as the
	// PG twin.
	gotB, err := storeB.FindTaskIDsByPrefix(ctx, prefix, 3)
	if err != nil {
		t.Fatalf("FindTaskIDsByPrefix (workspace B): %v", err)
	}
	if len(gotB) != 1 || gotB[0].ID != taskB.ID {
		t.Errorf("workspace B's own store did not find its own task, got %+v", gotB)
	}
}

// forceSharedPrefixSQLite is forceSharedPrefix's SQLite-package twin
// (internal/gtd/find_task_ids_by_prefix_pg_test.go) — identical construction,
// duplicated rather than shared because the two are in different Go packages
// and this is the only place either package needs it.
func forceSharedPrefixSQLite(t *testing.T, base uuid.UUID, prefix string) uuid.UUID {
	t.Helper()
	s := base.String()
	flipped := byte('0')
	if s[9] == '0' {
		flipped = '1'
	}
	crafted := prefix + "-" + string(flipped) + s[10:13] + "-" + s[14:18] + "-" + s[19:23] + "-" + s[24:]
	id, err := uuid.Parse(crafted)
	if err != nil {
		t.Fatalf("forceSharedPrefixSQLite: constructed an invalid UUID %q: %v", crafted, err)
	}
	return id
}
