package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// taskCleanDesc raises no CheckTaskInput warning for kind "general" (104 bytes,
// carries a file:line reference); taskVagueDesc raises exactly the two warnings
// in taskVagueWarnings. None of these tests may use t.Parallel: each sets
// WBT_STRICT_VAGUENESS explicitly and the variable is process-global.
const (
	taskCleanDesc = "Add slog.Info audit row to confirm_proposal at internal/handler/proposal_handler.go:240 with proposal_id"
	taskVagueDesc = "TBD"
)

var taskVagueWarnings = []string{
	"description: contains vague marker TBD",
	"description: too short (< 30 chars) and no file:line reference",
}

// taskAcceptTimeout bounds every Materialize call: the SQLite pool holds ONE
// connection, so a read-back that bypasses the open tx (s.db.conn) blocks until
// this deadline instead of failing fast. The deadline turns that hang into a
// "context deadline exceeded" error the test can report.
const taskAcceptTimeout = 5 * time.Second

// taskAcceptFixture is one real :memory: SQLite database holding a pending
// TypeTask proposal and a real sqliteAcceptAdapter whose tx is already open.
type taskAcceptFixture struct {
	gtd     *sqlite.GTDStore
	sdb     *sqlite.DB
	prop    *db.PendingProposal
	adapter proposal.AcceptAdapter
}

func newTaskAcceptFixture(ctx context.Context, t *testing.T, workspaceID string, payload map[string]any) taskAcceptFixture {
	t.Helper()
	d, err := sqlite.OpenTemplated(t, ctx, ":memory:", workspaceID)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ps := sqlite.NewProposalStore(d)
	gs := sqlite.NewGTDStore(d)

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	prop, err := ps.Create(ctx, proposal.CreateParams{Type: proposal.TypeTask, Payload: raw, ProposedBy: "test"})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	adapter := sqlite.NewAcceptAdapter(prop.ID, sqlite.AcceptDeps{Proposal: ps, GTD: gs})
	if err := adapter.BeginTx(ctx); err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	return taskAcceptFixture{gtd: gs, sdb: d, prop: prop, adapter: adapter}
}

func (f taskAcceptFixture) taskRows(ctx context.Context, t *testing.T, title string) int {
	t.Helper()
	var n int
	if err := f.sdb.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE title = ?1`, title).Scan(&n); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	return n
}

// TestSqliteAcceptAdapter_TypeTask_Materialises drives a clean TypeTask
// proposal through the real adapter inside a real tx: the returned value is a
// *db.Task read back through that tx, committed rows are visible afterwards and
// rolled-back rows are not. The tx read-back is the part a non-tx taskByID
// would hang on (the Materialize ctx carries a deadline for that reason).
func TestSqliteAcceptAdapter_TypeTask_Materialises(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", "")
	cases := []struct {
		name   string
		commit bool
	}{
		{name: "commit_makes_row_visible", commit: true},
		{name: "rollback_leaves_no_row", commit: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), taskAcceptTimeout)
			defer cancel()
			title := "T-" + uuid.NewString()
			fx := newTaskAcceptFixture(ctx, t, "", map[string]any{
				"title": title, "description": taskCleanDesc, "suggested_kind": "general",
			})

			created, err := fx.adapter.Materialize(ctx, fx.prop, nil)
			if err != nil {
				fx.adapter.Rollback(ctx)
				t.Fatalf("Materialize: %v", err)
			}
			task, ok := created.(*db.Task)
			if !ok {
				fx.adapter.Rollback(ctx)
				t.Fatalf("created = %T, want *db.Task", created)
			}
			requireCleanTaskReadBack(t, task, title)

			if tc.commit {
				if err := fx.adapter.Commit(ctx); err != nil {
					t.Fatalf("Commit: %v", err)
				}
			} else {
				fx.adapter.Rollback(ctx)
			}
			fx.requireRowState(ctx, t, task, tc.commit)
		})
	}
}

// requireCleanTaskReadBack fails unless task is the clean "general" task the
// fixture inserted, as the tx read-back returns it.
func requireCleanTaskReadBack(t *testing.T, task *db.Task, title string) {
	t.Helper()
	if task.Title != title || task.Description.String != taskCleanDesc || task.Kind != "general" ||
		task.Status != "pending" || task.Priority != 3 || task.Area != "unsorted" {
		t.Errorf("unexpected task read-back: %+v", task)
	}
}

// requireRowState checks what the finished tx left behind, through the pooled
// connection (so it can only run once the tx is committed or rolled back): a
// committed task is visible by title and id, a rolled-back one is gone.
func (f taskAcceptFixture) requireRowState(ctx context.Context, t *testing.T, task *db.Task, committed bool) {
	t.Helper()
	wantRows := 0
	if committed {
		wantRows = 1
	}
	if got := f.taskRows(ctx, t, task.Title); got != wantRows {
		t.Errorf("tasks rows for %q = %d, want %d", task.Title, got, wantRows)
	}
	_, getErr := f.gtd.GetTaskByID(ctx, task.ID)
	if committed && getErr != nil {
		t.Errorf("GetTaskByID after commit: %v", getErr)
	}
	if !committed && !errors.Is(getErr, gtd.ErrNotFound) {
		t.Errorf("GetTaskByID after rollback = %v, want gtd.ErrNotFound", getErr)
	}
}

// TestSqliteAcceptAdapter_TypeTask_WarnMapShape pins the warn-mode return
// shape: a vague description in warn mode still creates the row, and the
// adapter returns map[string]any{"task", "warnings"} exactly like the PG
// adapter, so the HTTP response builder can read both backends the same way.
func TestSqliteAcceptAdapter_TypeTask_WarnMapShape(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", "")
	ctx, cancel := context.WithTimeout(context.Background(), taskAcceptTimeout)
	defer cancel()
	title := "T-" + uuid.NewString()
	fx := newTaskAcceptFixture(ctx, t, "", map[string]any{
		"title": title, "description": taskVagueDesc, "suggested_kind": "general",
	})
	defer fx.adapter.Rollback(ctx)

	created, err := fx.adapter.Materialize(ctx, fx.prop, nil)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	m, ok := created.(map[string]any)
	if !ok {
		t.Fatalf("created = %T, want map[string]any", created)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	if len(keys) != 2 || m["task"] == nil || m["warnings"] == nil {
		t.Fatalf("map keys = %v, want exactly task and warnings", keys)
	}
	task, ok := m["task"].(*db.Task)
	if !ok || task == nil {
		t.Fatalf(`m["task"] = %T, want non-nil *db.Task`, m["task"])
	}
	if task.Title != title {
		t.Errorf("task.Title = %q, want %q", task.Title, title)
	}
	warnings, ok := m["warnings"].([]string)
	if !ok {
		t.Fatalf(`m["warnings"] = %T, want []string`, m["warnings"])
	}
	if !reflect.DeepEqual(warnings, taskVagueWarnings) {
		t.Errorf("warnings = %q, want %q", warnings, taskVagueWarnings)
	}
	// The count goes through the pooled connection the open tx holds, so it
	// can only run once the tx is committed.
	if err := fx.adapter.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := fx.taskRows(ctx, t, title); got != 1 {
		t.Errorf("tasks rows after commit = %d, want 1 (warn mode still creates the task)", got)
	}
}

// TestSqliteAcceptAdapter_TypeTask_StrictRejected: strict mode turns the same
// vague payload into a Materialize error and writes no row (the decode runs
// before the insert).
func TestSqliteAcceptAdapter_TypeTask_StrictRejected(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", "true")
	ctx, cancel := context.WithTimeout(context.Background(), taskAcceptTimeout)
	defer cancel()
	title := "T-" + uuid.NewString()
	fx := newTaskAcceptFixture(ctx, t, "", map[string]any{
		"title": title, "description": taskVagueDesc, "suggested_kind": "general",
	})
	defer fx.adapter.Rollback(ctx)

	created, err := fx.adapter.Materialize(ctx, fx.prop, nil)
	if err == nil {
		t.Fatalf("Materialize = %v, want a vagueness error", created)
	}
	if !strings.Contains(err.Error(), "vagueness check failed") {
		t.Errorf("error = %q, want substring %q", err.Error(), "vagueness check failed")
	}
	fx.adapter.Rollback(ctx) // release the pooled connection before counting
	if got := fx.taskRows(ctx, t, title); got != 0 {
		t.Errorf("tasks rows after strict rejection = %d, want 0", got)
	}
}

// TestSqliteAcceptAdapter_TypeTask_WorkspaceScopedReadBack opens the database
// with a workspace id: the insert stamps it and the tx read-back (which filters
// on workspace_id) must still find the row, with workspace_id set on the result.
func TestSqliteAcceptAdapter_TypeTask_WorkspaceScopedReadBack(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", "")
	ctx, cancel := context.WithTimeout(context.Background(), taskAcceptTimeout)
	defer cancel()
	ws := uuid.New()
	title := "T-" + uuid.NewString()
	fx := newTaskAcceptFixture(ctx, t, ws.String(), map[string]any{
		"title": title, "description": taskCleanDesc, "suggested_kind": "general",
	})
	defer fx.adapter.Rollback(ctx)

	created, err := fx.adapter.Materialize(ctx, fx.prop, nil)
	if err != nil {
		t.Fatalf("Materialize with a workspace: %v", err)
	}
	task, ok := created.(*db.Task)
	if !ok {
		t.Fatalf("created = %T, want *db.Task", created)
	}
	if !task.WorkspaceID.Valid || uuid.UUID(task.WorkspaceID.Bytes) != ws {
		t.Errorf("task.WorkspaceID = %+v, want valid %s", task.WorkspaceID, ws)
	}
}
