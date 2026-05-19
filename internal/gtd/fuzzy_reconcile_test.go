package gtd_test

import (
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// pendingTask returns a db.Task in the shape MatchPendingTasksFuzzy expects:
// pending status, branch_name and pr_url both NULL.
func pendingTask(id uuid.UUID, title string) db.Task {
	return db.Task{
		ID:     id,
		Title:  title,
		Status: string(gtd.TaskStatusPending),
		// BranchName / PRUrl intentionally left invalid (Valid=false)
	}
}

// taskWithBranch returns a db.Task that has a branch_name set — should be
// ignored by the fuzzy matcher.
func taskWithBranch(id uuid.UUID, title, branch string) db.Task {
	t := pendingTask(id, title)
	t.BranchName = pgtype.Text{String: branch, Valid: true}
	return t
}

func TestMatchPendingTasksFuzzy_TaskIDInBody(t *testing.T) {
	t.Parallel()
	taskID := uuid.New()
	tasks := []db.Task{
		pendingTask(taskID, "wholly unrelated title"),
	}
	prs := []gtd.MergedPR{{
		URL:   "https://github.com/o/r/pull/1",
		Title: "completely different",
		Body:  "Closes task " + taskID.String(),
	}}
	got := gtd.MatchPendingTasksFuzzy(prs, tasks)
	if len(got) != 1 {
		t.Fatalf("got %d matches, want 1", len(got))
	}
	if got[0].TaskID != taskID {
		t.Errorf("task_id = %s, want %s", got[0].TaskID, taskID)
	}
	if got[0].Score != 1.0 {
		t.Errorf("score = %f, want 1.0", got[0].Score)
	}
	if got[0].Reason != gtd.FuzzyReasonTaskIDInBody {
		t.Errorf("reason = %q, want %q", got[0].Reason, gtd.FuzzyReasonTaskIDInBody)
	}
}

func TestMatchPendingTasksFuzzy_JaccardAboveThreshold(t *testing.T) {
	t.Parallel()
	taskID := uuid.New()
	tasks := []db.Task{
		pendingTask(taskID, "fix useCompleteTask onError invalidation"),
	}
	prs := []gtd.MergedPR{{
		URL:   "https://github.com/o/r/pull/2",
		Title: "fix: useCompleteTask onError invalidation",
		Body:  "",
	}}
	got := gtd.MatchPendingTasksFuzzy(prs, tasks)
	if len(got) != 1 {
		t.Fatalf("got %d matches, want 1 (high token overlap)", len(got))
	}
	if got[0].TaskID != taskID {
		t.Errorf("task_id = %s, want %s", got[0].TaskID, taskID)
	}
	if got[0].Score < 0.6 || got[0].Score > 1.0 {
		t.Errorf("score = %f, want [0.6, 1.0]", got[0].Score)
	}
	if got[0].Reason != gtd.FuzzyReasonJaccardTitle {
		t.Errorf("reason = %q, want %q", got[0].Reason, gtd.FuzzyReasonJaccardTitle)
	}
}

func TestMatchPendingTasksFuzzy_BelowThresholdRejected(t *testing.T) {
	t.Parallel()
	tasks := []db.Task{
		pendingTask(uuid.New(), "implement OAuth flow"),
	}
	prs := []gtd.MergedPR{{
		URL:   "https://github.com/o/r/pull/3",
		Title: "bump golangci-lint",
		Body:  "",
	}}
	got := gtd.MatchPendingTasksFuzzy(prs, tasks)
	if len(got) != 0 {
		t.Fatalf("got %d matches, want 0 (no overlap below threshold)", len(got))
	}
}

func TestMatchPendingTasksFuzzy_MultiPRPicksBestScore(t *testing.T) {
	t.Parallel()
	taskID := uuid.New()
	tasks := []db.Task{
		pendingTask(taskID, "refactor frontend dashboard pagination logic"),
	}
	prs := []gtd.MergedPR{
		{URL: "https://github.com/o/r/pull/100", Title: "frontend dashboard tweak"},
		{URL: "https://github.com/o/r/pull/200", Title: "refactor frontend dashboard pagination logic update"},
		{URL: "https://github.com/o/r/pull/300", Title: "unrelated CI infra change"},
	}
	got := gtd.MatchPendingTasksFuzzy(prs, tasks)
	if len(got) != 1 {
		t.Fatalf("got %d matches, want 1 (best-score pick)", len(got))
	}
	// pull/200 has the highest token overlap (5/6 vs 2/6).
	if got[0].PRURL != "https://github.com/o/r/pull/200" {
		t.Errorf("picked %s, want pull/200 (highest jaccard)", got[0].PRURL)
	}
}

func TestMatchPendingTasksFuzzy_SkipsTaskWithLinkage(t *testing.T) {
	t.Parallel()
	taskID := uuid.New()
	tasks := []db.Task{
		// Has branch_name set → exact-match path owns this, fuzzy MUST skip.
		taskWithBranch(taskID, "refactor frontend dashboard", "feature/x"),
	}
	prs := []gtd.MergedPR{{
		URL:   "https://github.com/o/r/pull/7",
		Title: "refactor frontend dashboard",
	}}
	got := gtd.MatchPendingTasksFuzzy(prs, tasks)
	if len(got) != 0 {
		t.Errorf("got %d matches, want 0 (task already linked → skip)", len(got))
	}
}

func TestMatchPendingTasksFuzzy_SkipsCompletedTask(t *testing.T) {
	t.Parallel()
	taskID := uuid.New()
	tasks := []db.Task{{
		ID:     taskID,
		Title:  "do the thing",
		Status: string(gtd.TaskStatusCompleted),
	}}
	prs := []gtd.MergedPR{{
		URL:   "https://github.com/o/r/pull/8",
		Title: "do the thing",
	}}
	got := gtd.MatchPendingTasksFuzzy(prs, tasks)
	if len(got) != 0 {
		t.Errorf("got %d matches, want 0 (task already completed)", len(got))
	}
}

func TestMatchPendingTasksFuzzy_EmptyInputs(t *testing.T) {
	t.Parallel()
	if got := gtd.MatchPendingTasksFuzzy(nil, nil); len(got) != 0 {
		t.Errorf("nil/nil: got %d, want 0", len(got))
	}
	if got := gtd.MatchPendingTasksFuzzy([]gtd.MergedPR{}, []db.Task{pendingTask(uuid.New(), "x")}); len(got) != 0 {
		t.Errorf("empty PRs: got %d, want 0", len(got))
	}
	if got := gtd.MatchPendingTasksFuzzy([]gtd.MergedPR{{URL: "https://github.com/o/r/pull/1", Title: "x"}}, []db.Task{}); len(got) != 0 {
		t.Errorf("empty tasks: got %d, want 0", len(got))
	}
}

// TestMatchPendingTasksFuzzy_StopwordsDoNotDominate verifies the Jaccard
// score doesn't inflate purely from stopword overlap (the, and, for…).
func TestMatchPendingTasksFuzzy_StopwordsDoNotDominate(t *testing.T) {
	t.Parallel()
	tasks := []db.Task{
		pendingTask(uuid.New(), "the and for with from this"),
	}
	prs := []gtd.MergedPR{{
		URL:   "https://github.com/o/r/pull/9",
		Title: "the and for with from this",
	}}
	got := gtd.MatchPendingTasksFuzzy(prs, tasks)
	// Both sets reduce to empty after stopwords + len<3 → jaccard(∅,∅)=0
	// → below threshold → no match.
	if len(got) != 0 {
		t.Errorf("got %d, want 0 (pure stopwords should not match)", len(got))
	}
}

// TestMatchPendingTasksFuzzy_UUIDInBodyBeatsLowJaccard verifies rule 1 wins
// even when the title overlap would be below threshold.
func TestMatchPendingTasksFuzzy_UUIDInBodyBeatsLowJaccard(t *testing.T) {
	t.Parallel()
	taskID := uuid.New()
	tasks := []db.Task{
		pendingTask(taskID, "tightly scoped backend specific work"),
	}
	prs := []gtd.MergedPR{{
		URL:   "https://github.com/o/r/pull/11",
		Title: "completely different bump dependency",
		Body:  "Random body referencing task " + taskID.String() + " inline.",
	}}
	got := gtd.MatchPendingTasksFuzzy(prs, tasks)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].Reason != gtd.FuzzyReasonTaskIDInBody {
		t.Errorf("reason = %q, want %q", got[0].Reason, gtd.FuzzyReasonTaskIDInBody)
	}
	if got[0].Score != 1.0 {
		t.Errorf("score = %f, want 1.0", got[0].Score)
	}
}
