package gtd_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestResolveTaskIDPrefix_PG is F0930-17's PG store-level test:
// FindTaskIDsByPrefix's 0/1/>=2-row semantics, plus workspace scoping. Each
// case is its own top-level function (prefixCasePG*) rather than a closure
// literal here, so gocyclo scores it separately from the test function that
// just dispatches — a closure nested inside the test function would instead
// add its branches to the test function's own complexity count (same
// pattern as TestTaskArea_ReturnedByEveryReadPathPG in this package).
func TestResolveTaskIDPrefix_PG(t *testing.T) {
	pool := openTestPgPool(t)
	cases := []struct {
		name string
		run  func(t *testing.T, pool *pgxpool.Pool)
	}{
		{"unique 8-char prefix returns exactly one candidate", prefixCasePGUnique},
		{"two tasks sharing a prefix return both candidates, ordered by id", prefixCasePGCollide},
		{"prefix matching zero tasks returns an empty (not nil-error) slice", prefixCasePGZero},
		{"workspace scoping: a prefix unique within another workspace is invisible here", prefixCasePGWorkspaceScoping},
		{"long dashed prefixes return the task and a flipped last char returns none", prefixCasePGLongPrefix},
	}
	for _, tc := range cases {
		run := tc.run
		t.Run(tc.name, func(t *testing.T) { run(t, pool) })
	}
}

func prefixCasePGUnique(t *testing.T, pool *pgxpool.Pool) {
	ctx := context.Background()
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "prefix-pg-unique"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	prefix := task.ID.String()[:8]

	got, err := store.FindTaskIDsByPrefix(ctx, prefix, 3)
	if err != nil {
		t.Fatalf("FindTaskIDsByPrefix: %v", err)
	}
	if len(got) != 1 || got[0].ID != task.ID {
		t.Fatalf("got %+v, want exactly [%s]", got, task.ID)
	}
	if got[0].Title != "prefix-pg-unique" {
		t.Errorf("Title = %q, want %q", got[0].Title, "prefix-pg-unique")
	}
}

func prefixCasePGCollide(t *testing.T, pool *pgxpool.Pool) {
	ctx := context.Background()
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	taskA, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "prefix-pg-collide-a"})
	if err != nil {
		t.Fatalf("CreateTask A: %v", err)
	}
	taskB, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "prefix-pg-collide-b"})
	if err != nil {
		t.Fatalf("CreateTask B: %v", err)
	}

	sharedPrefix := taskA.ID.String()[:8]
	// Force B's id to share A's 8-char prefix but stay distinct overall
	// (flip B's 9th hex nibble so the two ids never collide exactly). A
	// real UUID collision on 8 hex chars is a ~1-in-4-billion event,
	// impractical to seed by retrying CreateTask. Raw SQL id UPDATE is
	// safe here specifically because red line #9 (no FK constraints
	// anywhere in this codebase) means no other table can reference
	// tasks.id via a FK that this UPDATE could break.
	forcedB := forceSharedPrefix(t, taskB.ID, sharedPrefix)
	if _, err := pool.Exec(ctx, `UPDATE tasks SET id = $1 WHERE id = $2`, forcedB, taskB.ID); err != nil {
		t.Fatalf("forcing task B id: %v", err)
	}

	got, err := store.FindTaskIDsByPrefix(ctx, sharedPrefix, 3)
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
	// PG vs SQLite parity + repeat-call determinism: both require ORDER BY id.
	if got[0].ID.String() > got[1].ID.String() {
		t.Errorf("candidates not ordered by id ascending: %s then %s", got[0].ID, got[1].ID)
	}
}

func prefixCasePGZero(t *testing.T, pool *pgxpool.Pool) {
	ctx := context.Background()
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	got, err := store.FindTaskIDsByPrefix(ctx, "deadbeef", 3)
	if err != nil {
		t.Fatalf("FindTaskIDsByPrefix: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d candidates, want 0: %+v", len(got), got)
	}
}

func prefixCasePGWorkspaceScoping(t *testing.T, pool *pgxpool.Pool) {
	ctx := context.Background()
	wsA := uuid.New()
	wsB := uuid.New()
	storeA := newPgGTDStore(pool, &wsA)
	storeB := newPgGTDStore(pool, &wsB)

	taskB, err := storeB.CreateTask(ctx, gtd.CreateTaskParams{Title: "prefix-pg-other-workspace"})
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

	// Own-workspace visibility regression guard: the SAME query against
	// the SAME workspace the task actually belongs to must still find it
	// — proves the predicate filters by workspace, not by hiding
	// everything (mirrors TestSQLiteStore_TasksFiltered_WorkspaceScoping's
	// own-workspace guard, gtd_filtered_test.go).
	gotB, err := storeB.FindTaskIDsByPrefix(ctx, prefix, 3)
	if err != nil {
		t.Fatalf("FindTaskIDsByPrefix (workspace B): %v", err)
	}
	if len(gotB) != 1 || gotB[0].ID != taskB.ID {
		t.Errorf("workspace B's own store did not find its own task, got %+v", gotB)
	}
}

// forceSharedPrefix returns a UUID that starts with prefix but is otherwise
// derived from base (flips base's 9th hex nibble so the result is never
// literally equal to a task already using that prefix).
func forceSharedPrefix(t *testing.T, base uuid.UUID, prefix string) uuid.UUID {
	t.Helper()
	s := base.String()
	// s[9] is the first nibble after the first dash (index 8) — flip it so
	// the crafted id stays distinct from any id sharing the same prefix.
	flipped := byte('0')
	if s[9] == '0' {
		flipped = '1'
	}
	crafted := prefix + "-" + string(flipped) + s[10:13] + "-" + s[14:18] + "-" + s[19:23] + "-" + s[24:]
	id, err := uuid.Parse(crafted)
	if err != nil {
		t.Fatalf("forceSharedPrefix: constructed an invalid UUID %q: %v", crafted, err)
	}
	return id
}

// prefixCasePGLongPrefix [F1124-06] covers prefixes longer than 8 characters,
// which the resolver's canonical dashed form can send up to 35 characters
// (31 hex digits plus 4 dashes). Every length must return exactly the task,
// the same prefix with its last character changed must return nothing (so a
// query that only compares the first 8 characters fails here), a dashless
// 9-hex string must return nothing (the store takes the canonical dashed
// form; canonicalising is the resolver's job), and another workspace must not
// see the task through a long prefix either.
func prefixCasePGLongPrefix(t *testing.T, pool *pgxpool.Pool) {
	ctx := context.Background()
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "prefix-pg-long"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	p := task.ID.String()

	for _, n := range []int{9, 13, 23, 35} {
		got, err := store.FindTaskIDsByPrefix(ctx, p[:n], 3)
		if err != nil {
			t.Fatalf("FindTaskIDsByPrefix(len %d): %v", n, err)
		}
		if len(got) != 1 || got[0].ID != task.ID {
			t.Errorf("prefix len %d (%q): got %+v, want exactly [%s]", n, p[:n], got, task.ID)
		}

		flipped := p[:n-1] + flipLastPrefixChar(p[n-1])
		gotFlipped, err := store.FindTaskIDsByPrefix(ctx, flipped, 3)
		if err != nil {
			t.Fatalf("FindTaskIDsByPrefix(flipped len %d): %v", n, err)
		}
		if len(gotFlipped) != 0 {
			t.Errorf("flipped-last-char prefix len %d (%q): got %d rows, want 0: %+v", n, flipped, len(gotFlipped), gotFlipped)
		}
	}

	dashless := p[:8] + p[9:10]
	gotDashless, err := store.FindTaskIDsByPrefix(ctx, dashless, 3)
	if err != nil {
		t.Fatalf("FindTaskIDsByPrefix(dashless): %v", err)
	}
	if len(gotDashless) != 0 {
		t.Errorf("dashless 9-hex prefix %q: got %d rows, want 0 (the store takes the canonical dashed form)", dashless, len(gotDashless))
	}

	otherWS := uuid.New()
	gotOther, err := newPgGTDStore(pool, &otherWS).FindTaskIDsByPrefix(ctx, p[:13], 3)
	if err != nil {
		t.Fatalf("FindTaskIDsByPrefix(other workspace): %v", err)
	}
	if len(gotOther) != 0 {
		t.Errorf("another workspace saw %d rows through a 13-char prefix, want 0: %+v", len(gotOther), gotOther)
	}
}

// flipLastPrefixChar returns a different character for c: a dash becomes '0',
// a hex digit becomes another hex digit.
func flipLastPrefixChar(c byte) string {
	switch c {
	case '-':
		return "0"
	case '0':
		return "1"
	default:
		return "0"
	}
}
