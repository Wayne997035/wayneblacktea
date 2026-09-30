package gtd_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// TestListTasksQ_MatchesTitleSubstring_PG is F0930-20's PG store-level test:
// CJK substring and case-insensitive English substring both match; a
// non-matching title (different word) does not; empty Q is unfiltered.
func TestListTasksQ_MatchesTitleSubstring_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	for _, title := range []string{"修復登入問題", "Add login page", "Fix LOGIN bug", "登出功能"} {
		if _, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: title}); err != nil {
			t.Fatalf("CreateTask %q: %v", title, err)
		}
	}

	got, err := store.TasksFiltered(ctx, gtd.TaskFilter{Q: "登入", Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered(Q=登入): %v", err)
	}
	if len(got) != 1 || got[0].Title != "修復登入問題" {
		t.Fatalf("Q=登入: got %+v, want exactly [修復登入問題] (登出 must not match)", got)
	}

	got, err = store.TasksFiltered(ctx, gtd.TaskFilter{Q: "login", Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered(Q=login): %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Q=login (case-insensitive): got %d rows, want 2 (Add login page, Fix LOGIN bug): %+v", len(got), got)
	}

	got, err = store.TasksFiltered(ctx, gtd.TaskFilter{Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered(no Q): %v", err)
	}
	if len(got) != 4 {
		t.Errorf("no Q: got %d rows, want 4 (unfiltered, byte-identical to pre-F0930-20 behaviour)", len(got))
	}
}

// TestListTasksQ_EscapesLikeWildcards_PG is F0930-20's PG escaping
// regression guard — the first test in this repo to exercise underscore
// escaping (list-tasks-q.md's own self-verification checklist note).
func TestListTasksQ_EscapesLikeWildcards_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	for _, title := range []string{"100% done", "1000 done", "a_b config", "aXb config"} {
		if _, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: title}); err != nil {
			t.Fatalf("CreateTask %q: %v", title, err)
		}
	}

	got, err := store.TasksFiltered(ctx, gtd.TaskFilter{Q: "100%", Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered(Q=100%%): %v", err)
	}
	if len(got) != 1 || got[0].Title != "100% done" {
		t.Fatalf("Q=100%%: got %+v, want exactly [100%% done] — literal %% must not act as a SQL wildcard "+
			"(a title like \"1000 done\" matching would mean the escape is missing)", got)
	}

	got, err = store.TasksFiltered(ctx, gtd.TaskFilter{Q: "a_b", Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered(Q=a_b): %v", err)
	}
	if len(got) != 1 || got[0].Title != "a_b config" {
		t.Fatalf("Q=a_b: got %+v, want exactly [a_b config] — literal _ must not act as a SQL wildcard "+
			"(a title like \"aXb config\" matching would mean the escape is missing)", got)
	}
}

// TestListTasksQ_WorkspaceIsolation_PG is F0930-20's PG workspace-scoping
// test: two stores on the SAME pool (shared physical table), different
// workspace_id. Removing/breaking the workspace predicate on the Q branch
// specifically would leak workspace B's row into workspace A's result.
func TestListTasksQ_WorkspaceIsolation_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsA := uuid.New()
	wsB := uuid.New()
	storeA := newPgGTDStore(pool, &wsA)
	storeB := newPgGTDStore(pool, &wsB)
	ctx := context.Background()

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

	// Own-workspace visibility regression guard: proves the filter itself
	// isn't broken, only cross-workspace leakage is blocked.
	gotB, err := storeB.TasksFiltered(ctx, gtd.TaskFilter{Q: "searchable", Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered (workspace B): %v", err)
	}
	if len(gotB) != 1 {
		t.Errorf("workspace B's own store did not find its own task via q, got %+v", gotB)
	}
}
