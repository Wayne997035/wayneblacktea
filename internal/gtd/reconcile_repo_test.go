package gtd_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// tasksOnlyStore serves Tasks from memory; any other StoreIface method
// panics (nil embedded interface), which the matcher never calls.
type tasksOnlyStore struct {
	gtd.StoreIface
	tasks []db.Task
}

func (s tasksOnlyStore) Tasks(context.Context, *uuid.UUID) ([]db.Task, error) { return s.tasks, nil }

func branchTask(branch string, projectID *uuid.UUID, updated time.Time) db.Task {
	t := db.Task{
		ID:         uuid.New(),
		Status:     string(gtd.TaskStatusPending),
		BranchName: pgtype.Text{String: branch, Valid: true},
		UpdatedAt:  pgtype.Timestamptz{Time: updated, Valid: true},
	}
	if projectID != nil {
		t.ProjectID = pgtype.UUID{Bytes: *projectID, Valid: true}
	}
	return t
}

// repoFixture links project → repo name → github_slug the way production
// data does, and returns the resolver built from it.
func repoFixture(slug string) (uuid.UUID, gtd.RepoResolver) {
	pid := uuid.New()
	projects := []db.Project{{ID: pid, RepoName: pgtype.Text{String: "wbt-dir", Valid: true}}}
	repos := []db.Repo{{Name: "wbt-dir", GithubSlug: pgtype.Text{String: slug, Valid: true}}}
	return pid, gtd.NewRepoResolver(projects, repos)
}

// TestMatchMergedPRs_RepoAware pins [F0925-31]: a branch_name hit only
// auto-closes a task whose repo (project → repo → github_slug) is the PR's
// repo; another repo's same-named branch is skipped and counted; a task whose
// repo cannot be derived becomes an unverified candidate, never a Match.
func TestMatchMergedPRs_RepoAware(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Now().UTC()
	pid, resolve := repoFixture("Wayne997035/wayneblacktea")
	pr := gtd.MergedPR{URL: "https://github.com/x/y/pull/1", HeadRef: "feat/x", MergedAt: now}

	t.Run("same repo matches, case-insensitively", func(t *testing.T) {
		t.Parallel()
		task := branchTask("feat/x", &pid, now)
		p := pr
		p.Repo = "wayne997035/WAYNEBLACKTEA"
		res, err := gtd.MatchMergedPRs(ctx, tasksOnlyStore{tasks: []db.Task{task}}, []gtd.MergedPR{p}, resolve)
		if err != nil || len(res.Matches) != 1 || res.Matches[0].TaskID != task.ID {
			t.Fatalf("want 1 match on %s, got %+v (err %v)", task.ID, res, err)
		}
	})
	t.Run("other repo's same branch is skipped", func(t *testing.T) {
		t.Parallel()
		task := branchTask("feat/x", &pid, now)
		p := pr
		p.Repo = "someone/else"
		res, err := gtd.MatchMergedPRs(ctx, tasksOnlyStore{tasks: []db.Task{task}}, []gtd.MergedPR{p}, resolve)
		if err != nil || len(res.Matches) != 0 || res.SkippedRepoMismatch != 1 || len(res.UnverifiedRepo) != 0 {
			t.Fatalf("want 0 matches, 1 skipped, got %+v (err %v)", res, err)
		}
	})
	t.Run("unknown repo becomes an unverified candidate", func(t *testing.T) {
		t.Parallel()
		task := branchTask("feat/x", nil, now)
		p := pr
		p.Repo = "Wayne997035/wayneblacktea"
		res, err := gtd.MatchMergedPRs(ctx, tasksOnlyStore{tasks: []db.Task{task}}, []gtd.MergedPR{p}, resolve)
		if err != nil || len(res.Matches) != 0 || len(res.UnverifiedRepo) != 1 || res.UnverifiedRepo[0].TaskID != task.ID {
			t.Fatalf("want 1 unverified, 0 matches, got %+v (err %v)", res, err)
		}
	})
	t.Run("winner comes from verified, unknown sibling is unverified", func(t *testing.T) {
		t.Parallel()
		verified := branchTask("feat/x", &pid, now.Add(-time.Hour))
		unknown := branchTask("feat/x", nil, now) // more recent, but repo unknown
		p := pr
		p.Repo = "Wayne997035/wayneblacktea"
		res, err := gtd.MatchMergedPRs(ctx, tasksOnlyStore{tasks: []db.Task{verified, unknown}}, []gtd.MergedPR{p}, resolve)
		if err != nil || len(res.Matches) != 1 || res.Matches[0].TaskID != verified.ID ||
			len(res.UnverifiedRepo) != 1 || res.UnverifiedRepo[0].TaskID != unknown.ID {
			t.Fatalf("want verified winner + unknown unverified, got %+v (err %v)", res, err)
		}
	})
	t.Run("pr_url_exact ignores repo", func(t *testing.T) {
		t.Parallel()
		url := "https://github.com/someone/else/pull/9"
		task := db.Task{ID: uuid.New(), Status: string(gtd.TaskStatusPending), PRUrl: pgtype.Text{String: url, Valid: true}}
		p := gtd.MergedPR{URL: url, HeadRef: "whatever", Repo: "someone/else", MergedAt: now}
		res, err := gtd.MatchMergedPRs(ctx, tasksOnlyStore{tasks: []db.Task{task}}, []gtd.MergedPR{p}, resolve)
		if err != nil || len(res.Matches) != 1 || res.Matches[0].Reason != gtd.MatchReasonPRURLExact {
			t.Fatalf("want pr_url_exact match, got %+v (err %v)", res, err)
		}
	})
	t.Run("nil resolver is an error", func(t *testing.T) {
		t.Parallel()
		res, err := gtd.MatchMergedPRs(ctx, tasksOnlyStore{tasks: []db.Task{branchTask("feat/x", &pid, now)}}, []gtd.MergedPR{pr}, nil)
		if err == nil || len(res.Matches) != 0 {
			t.Fatalf("nil resolver must error with no matches, got %+v (err %v)", res, err)
		}
	})
}

// TestMatchPendingTasksFuzzy_RepoAware pins the fuzzy path's exclusion at
// the (task, PR) pair level: a task whose repo is known skips only PRs from
// other repos, still matching a same-repo PR in the same batch; comparison is
// case-insensitive; a nil resolver keeps the previous behaviour.
func TestMatchPendingTasksFuzzy_RepoAware(t *testing.T) {
	t.Parallel()
	pid, resolve := repoFixture("Wayne997035/Foo")
	task := db.Task{
		ID: uuid.New(), Status: string(gtd.TaskStatusPending), Title: "add reconcile repo matcher",
		ProjectID: pgtype.UUID{Bytes: pid, Valid: true},
	}
	other := gtd.MergedPR{URL: "https://github.com/b/b/pull/1", Title: "add reconcile repo matcher", Repo: "b/b"}
	same := gtd.MergedPR{URL: "https://github.com/w/f/pull/2", Title: "add reconcile repo matcher", Repo: "wayne997035/foo"}

	got := gtd.MatchPendingTasksFuzzy([]gtd.MergedPR{other, same}, []db.Task{task}, resolve)
	if len(got) != 1 || got[0].PRURL != same.URL {
		t.Fatalf("want exactly the same-repo PR, got %+v", got)
	}
	if got := gtd.MatchPendingTasksFuzzy([]gtd.MergedPR{other}, []db.Task{task}, resolve); len(got) != 0 {
		t.Fatalf("other repo's PR must be excluded, got %+v", got)
	}
	if got := gtd.MatchPendingTasksFuzzy([]gtd.MergedPR{other}, []db.Task{task}, nil); len(got) != 1 {
		t.Fatalf("nil resolver must keep the previous behaviour, got %+v", got)
	}
}
