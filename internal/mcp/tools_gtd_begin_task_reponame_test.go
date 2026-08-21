package mcp

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// TestResolveBeginTaskRepoName_FallbackChain pins resolveBeginTaskRepoName's
// three branches (U16, 2026-08-20-mcp-surface-spec.md). The function feeds
// worksession.CreateParams.RepoName, which rejects "" — so the load-bearing
// property is not "picks the right name" but "never returns empty", on every
// path including the ones where the project lookup gives it nothing to work
// with. A regression here does not surface as a wrong repo name; it surfaces
// as begin_task silently failing to attach a work session, which
// attachBeginTaskWorkSession swallows by design (best-effort). That is why
// this is asserted directly against the resolver rather than through the tool.
func TestResolveBeginTaskRepoName_FallbackChain(t *testing.T) {
	ctx := context.Background()

	// seedTaskInProject creates a project with the given repo_name ("" → NULL)
	// and a task linked to it, returning the task.
	seedTaskInProject := func(t *testing.T, s *Server, repoName string) *db.Task {
		t.Helper()
		p, err := s.gtd.CreateProject(ctx, gtd.CreateProjectParams{
			Name:     "proj-" + uuid.NewString(),
			Title:    "fallback-chain fixture",
			Area:     "engineering",
			RepoName: repoName,
		})
		if err != nil {
			t.Fatalf("CreateProject(repo_name=%q): %v", repoName, err)
		}
		task, err := s.gtd.CreateTask(ctx, gtd.CreateTaskParams{
			ProjectID: &p.ID,
			Title:     "fallback-chain task " + uuid.NewString(),
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		return task
	}

	t.Run("project has repo_name -> that repo_name wins", func(t *testing.T) {
		s := newTestWorkSessionServer(t)
		task := seedTaskInProject(t, s, "chat-gateway")

		got := s.resolveBeginTaskRepoName(ctx, task)
		if got != "chat-gateway" {
			t.Errorf("resolveBeginTaskRepoName = %q, want %q", got, "chat-gateway")
		}
		// bad case: the fallback must NOT pre-empt a project that actually
		// carries a repo_name — that would scope every work session to the
		// single-tenant default and make per-repo session queries useless.
		if got == primaryProjectSlug {
			t.Errorf("fallback %q pre-empted the project's own repo_name", primaryProjectSlug)
		}
	})

	t.Run("project exists but repo_name is NULL -> falls back", func(t *testing.T) {
		s := newTestWorkSessionServer(t)
		task := seedTaskInProject(t, s, "") // empty → stored as NULL

		if got := s.resolveBeginTaskRepoName(ctx, task); got != primaryProjectSlug {
			t.Errorf("resolveBeginTaskRepoName = %q, want fallback %q", got, primaryProjectSlug)
		}
	})

	t.Run("task has no project -> falls back", func(t *testing.T) {
		s := newTestWorkSessionServer(t)
		id := seedTask(t, s) // seedTask creates a task with ProjectID nil
		task, err := s.gtd.GetTaskByID(ctx, id)
		if err != nil {
			t.Fatalf("GetTaskByID: %v", err)
		}
		if task.ProjectID.Valid {
			t.Fatalf("fixture precondition failed: seedTask produced a project-linked task")
		}

		if got := s.resolveBeginTaskRepoName(ctx, task); got != primaryProjectSlug {
			t.Errorf("resolveBeginTaskRepoName = %q, want fallback %q", got, primaryProjectSlug)
		}
	})

	// bad case, stated once for the whole chain: worksession.CreateParams
	// rejects an empty RepoName, so an empty return on ANY branch breaks
	// begin_task's work-session attachment rather than producing a merely
	// mislabelled session.
	t.Run("no branch ever returns the empty string", func(t *testing.T) {
		s := newTestWorkSessionServer(t)
		withRepo := seedTaskInProject(t, s, "neomart-api")
		withoutRepo := seedTaskInProject(t, s, "")
		orphanID := seedTask(t, s)
		orphan, err := s.gtd.GetTaskByID(ctx, orphanID)
		if err != nil {
			t.Fatalf("GetTaskByID: %v", err)
		}

		for _, task := range []*db.Task{withRepo, withoutRepo, orphan} {
			if got := s.resolveBeginTaskRepoName(ctx, task); got == "" {
				t.Errorf("resolveBeginTaskRepoName returned \"\" for task %s", task.ID)
			}
		}
	})
}
