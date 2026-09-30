package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

// TestListTasksQ_MatchesTitleSubstring_SQLite mirrors
// TestListTasksQ_MatchesTitleSubstring_PG (internal/gtd) — same assertions,
// same shape, the dual-backend parity this feature requires.
func TestListTasksQ_MatchesTitleSubstring_SQLite(t *testing.T) {
	t.Parallel()
	s := openMem(t, "")
	ctx := context.Background()

	for _, title := range []string{"修復登入問題", "Add login page", "Fix LOGIN bug", "登出功能"} {
		if _, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: title}); err != nil {
			t.Fatalf("CreateTask %q: %v", title, err)
		}
	}

	got, err := s.TasksFiltered(ctx, gtd.TaskFilter{Q: "登入", Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered(Q=登入): %v", err)
	}
	if len(got) != 1 || got[0].Title != "修復登入問題" {
		t.Fatalf("Q=登入: got %+v, want exactly [修復登入問題] (登出 must not match)", got)
	}

	got, err = s.TasksFiltered(ctx, gtd.TaskFilter{Q: "login", Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered(Q=login): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Q=login (case-insensitive): got %d rows, want 2 (Add login page, Fix LOGIN bug): %+v", len(got), got)
	}

	got, err = s.TasksFiltered(ctx, gtd.TaskFilter{Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered(no Q): %v", err)
	}
	if len(got) != 4 {
		t.Errorf("no Q: got %d rows, want 4 (unfiltered, byte-identical to pre-F0930-20 behaviour)", len(got))
	}
}

// TestListTasksQ_EscapesLikeWildcards_SQLite mirrors the PG escaping guard.
func TestListTasksQ_EscapesLikeWildcards_SQLite(t *testing.T) {
	t.Parallel()
	s := openMem(t, "")
	ctx := context.Background()

	for _, title := range []string{"100% done", "1000 done", "a_b config", "aXb config"} {
		if _, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: title}); err != nil {
			t.Fatalf("CreateTask %q: %v", title, err)
		}
	}

	got, err := s.TasksFiltered(ctx, gtd.TaskFilter{Q: "100%", Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered(Q=100%%): %v", err)
	}
	if len(got) != 1 || got[0].Title != "100% done" {
		t.Fatalf("Q=100%%: got %+v, want exactly [100%% done] — literal %% must not act as a SQL wildcard", got)
	}

	got, err = s.TasksFiltered(ctx, gtd.TaskFilter{Q: "a_b", Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered(Q=a_b): %v", err)
	}
	if len(got) != 1 || got[0].Title != "a_b config" {
		t.Fatalf("Q=a_b: got %+v, want exactly [a_b config] — literal _ must not act as a SQL wildcard", got)
	}
}

// TestListTasksQ_WorkspaceIsolation_SQLite mirrors the PG workspace-scoping
// test, using openFileStore (gtd_filtered_test.go, same package) so both
// stores share one physical table — required to prove the workspace_id
// predicate on the Q branch actually filters rows.
func TestListTasksQ_WorkspaceIsolation_SQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	wsA := "11111111-1111-4111-8111-111111111111"
	wsB := "22222222-2222-4222-8222-222222222222"
	sharedPath := filepath.Join(t.TempDir(), "list-tasks-q-workspace.db")
	storeA := openFileStore(t, sharedPath, wsA)
	storeB := openFileStore(t, sharedPath, wsB)

	if _, err := storeB.CreateTask(ctx, gtd.CreateTaskParams{Title: "workspace-b-only-searchable-task"}); err != nil {
		t.Fatalf("CreateTask (workspace B): %v", err)
	}

	gotA, err := storeA.TasksFiltered(ctx, gtd.TaskFilter{Q: "searchable", Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered (workspace A): %v", err)
	}
	if len(gotA) != 0 {
		t.Errorf("workspace A saw %d rows for a workspace-B-only q, want 0: %+v", len(gotA), gotA)
	}

	gotB, err := storeB.TasksFiltered(ctx, gtd.TaskFilter{Q: "searchable", Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered (workspace B): %v", err)
	}
	if len(gotB) != 1 {
		t.Errorf("workspace B's own store did not find its own task via q, got %+v", gotB)
	}
}
