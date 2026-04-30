package notion

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// fakeBriefingStores satisfies BriefingStores with deterministic in-memory
// fixtures so we can exercise BuildDailyBriefing without hitting Postgres.
type fakeBriefingStores struct {
	tasks         []db.Task
	tasksErr      error
	dueReviews    []learning.DueReview
	dueReviewsErr error
	pending       []db.PendingProposal
	pendingErr    error
	decisions     []db.Decision
	decisionsErr  error
	weeklyDone    int64
	weeklyTotal   int64
	weeklyErr     error
}

func (f *fakeBriefingStores) Tasks(_ context.Context, _ *uuid.UUID) ([]db.Task, error) {
	return f.tasks, f.tasksErr
}

func (f *fakeBriefingStores) DueReviews(_ context.Context, _ int) ([]learning.DueReview, error) {
	return f.dueReviews, f.dueReviewsErr
}

func (f *fakeBriefingStores) ListPending(_ context.Context) ([]db.PendingProposal, error) {
	return f.pending, f.pendingErr
}

func (f *fakeBriefingStores) All(_ context.Context, _ int32) ([]db.Decision, error) {
	return f.decisions, f.decisionsErr
}

func (f *fakeBriefingStores) WeeklyProgress(_ context.Context) (int64, int64, error) {
	return f.weeklyDone, f.weeklyTotal, f.weeklyErr
}

// validTime is a helper for building pgtype.Timestamptz fixtures.
func validTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func validInt2(v int16) pgtype.Int2 {
	return pgtype.Int2{Int16: v, Valid: true}
}

func validText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: true}
}

func TestBuildDailyBriefing(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 28, 8, 0, 0, 0, time.UTC)
	freshTaskUpdate := now.Add(-1 * time.Hour) // not stuck
	stuckTaskUpdate := now.Add(-6 * time.Hour) // stuck (> 4 h)

	taskFreshID := uuid.New()
	taskStuckID := uuid.New()
	taskCompletedID := uuid.New()
	conceptID := uuid.New()
	scheduleID := uuid.New()
	proposalID := uuid.New()
	decisionFreshID := uuid.New()
	decisionStaleID := uuid.New()

	tests := []struct {
		name    string
		stores  *fakeBriefingStores
		wantErr bool
		briefingWant
	}{
		{
			name: "happy path with all sections populated",
			stores: &fakeBriefingStores{
				tasks: []db.Task{
					{
						ID: taskFreshID, Title: "fresh task", Status: "in_progress",
						UpdatedAt: validTime(freshTaskUpdate), Importance: validInt2(1),
						Context: validText("urgent context"),
					},
					{
						ID: taskStuckID, Title: "stuck task", Status: "in_progress",
						UpdatedAt: validTime(stuckTaskUpdate), Importance: validInt2(2),
					},
					{
						ID: taskCompletedID, Title: "done task", Status: "completed",
						UpdatedAt: validTime(freshTaskUpdate),
					},
				},
				dueReviews: []learning.DueReview{
					{
						ConceptID: conceptID, ScheduleID: scheduleID, Title: "due concept",
						DueDate: now.Add(-1 * time.Hour),
					},
				},
				pending: []db.PendingProposal{
					{
						ID: proposalID, Type: "concept", ProposedBy: validText("mcp:add_knowledge"),
						CreatedAt: validTime(now.Add(-2 * time.Hour)),
					},
				},
				decisions: []db.Decision{
					{
						ID: decisionFreshID, Title: "fresh decision", RepoName: validText("wayneblacktea"),
						CreatedAt: validTime(now.Add(-5 * time.Hour)),
					},
					{
						ID: decisionStaleID, Title: "old decision",
						CreatedAt: validTime(now.Add(-30 * time.Hour)),
					}, // outside 24 h window
				},
				weeklyDone:  3,
				weeklyTotal: 7,
			},
			briefingWant: briefingWant{
				now:                  now,
				wantInProgress:       2,
				wantStuckCount:       1,
				wantStuckIDs:         []string{taskStuckID.String()},
				wantDecisionCount:    1,
				wantPendingCount:     1,
				wantReviewCount:      1,
				wantCompletedTasks:   3,
				wantTotalActiveTasks: 7,
				wantImportance:       1,
			},
		},
		{
			name: "task without importance defaults to 3",
			stores: &fakeBriefingStores{
				tasks: []db.Task{
					{
						ID: taskFreshID, Title: "no importance", Status: "in_progress",
						UpdatedAt: validTime(freshTaskUpdate),
					},
				},
			},
			briefingWant: briefingWant{now: now, wantInProgress: 1, wantImportance: 3},
		},
		{
			name: "all section queries failing returns error",
			stores: &fakeBriefingStores{
				tasksErr:      errors.New("boom tasks"),
				dueReviewsErr: errors.New("boom reviews"),
				pendingErr:    errors.New("boom proposals"),
				decisionsErr:  errors.New("boom decisions"),
				weeklyErr:     errors.New("boom weekly"),
			},
			wantErr:      true,
			briefingWant: briefingWant{now: now},
		},
		{
			name: "tasks fail but decisions succeed returns partial briefing",
			stores: &fakeBriefingStores{
				tasksErr: errors.New("transient"),
				decisions: []db.Decision{
					{
						ID: decisionFreshID, Title: "still here",
						CreatedAt: validTime(now.Add(-1 * time.Hour)),
					},
				},
			},
			briefingWant: briefingWant{now: now, wantInProgress: 0, wantDecisionCount: 1},
		},
		{
			name: "decision exactly at 24h cutoff is excluded",
			stores: &fakeBriefingStores{
				decisions: []db.Decision{
					{
						ID: decisionFreshID, Title: "boundary",
						CreatedAt: validTime(now.Add(-recentDecisionWindow - time.Second)),
					},
				},
			},
			briefingWant: briefingWant{now: now, wantDecisionCount: 0},
		},
		{
			name: "decision with invalid created_at is skipped",
			stores: &fakeBriefingStores{
				decisions: []db.Decision{
					{ID: decisionFreshID, Title: "no timestamp"},
				},
			},
			briefingWant: briefingWant{now: now, wantDecisionCount: 0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := BuildDailyBriefing(context.Background(), tc.stores, tc.now)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil briefing %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == nil {
				t.Fatalf("expected briefing, got nil")
			}
			assertBriefing(t, got, tc.briefingWant)
		})
	}
}

// briefingWant captures per-test assertions for assertBriefing so we can
// keep TestBuildDailyBriefing's complexity within the gocyclo limit.
type briefingWant struct {
	now                  time.Time
	wantInProgress       int
	wantStuckCount       int
	wantStuckIDs         []string
	wantDecisionCount    int
	wantPendingCount     int
	wantReviewCount      int
	wantCompletedTasks   int64
	wantTotalActiveTasks int64
	wantImportance       int
}

func assertBriefing(t *testing.T, got *DailyBriefing, want briefingWant) {
	t.Helper()
	if !got.Date.Equal(want.now) {
		t.Errorf("Date = %v, want %v", got.Date, want.now)
	}
	if len(got.InProgressTasks) != want.wantInProgress {
		t.Errorf("InProgressTasks count = %d, want %d", len(got.InProgressTasks), want.wantInProgress)
	}
	if got.SystemHealth.StuckTaskCount != want.wantStuckCount {
		t.Errorf("StuckTaskCount = %d, want %d", got.SystemHealth.StuckTaskCount, want.wantStuckCount)
	}
	if len(want.wantStuckIDs) > 0 {
		if len(got.SystemHealth.StuckTaskIDs) != len(want.wantStuckIDs) ||
			got.SystemHealth.StuckTaskIDs[0] != want.wantStuckIDs[0] {
			t.Errorf("StuckTaskIDs = %v, want %v", got.SystemHealth.StuckTaskIDs, want.wantStuckIDs)
		}
	}
	if len(got.RecentDecisions) != want.wantDecisionCount {
		t.Errorf("RecentDecisions count = %d, want %d", len(got.RecentDecisions), want.wantDecisionCount)
	}
	if len(got.PendingProposals) != want.wantPendingCount {
		t.Errorf("PendingProposals count = %d, want %d", len(got.PendingProposals), want.wantPendingCount)
	}
	if len(got.DueReviews) != want.wantReviewCount {
		t.Errorf("DueReviews count = %d, want %d", len(got.DueReviews), want.wantReviewCount)
	}
	if got.SystemHealth.WeeklyCompletedTask != want.wantCompletedTasks {
		t.Errorf("WeeklyCompletedTask = %d, want %d", got.SystemHealth.WeeklyCompletedTask, want.wantCompletedTasks)
	}
	if got.SystemHealth.WeeklyTotalActive != want.wantTotalActiveTasks {
		t.Errorf("WeeklyTotalActive = %d, want %d", got.SystemHealth.WeeklyTotalActive, want.wantTotalActiveTasks)
	}
	if want.wantImportance != 0 && len(got.InProgressTasks) > 0 &&
		got.InProgressTasks[0].Importance != want.wantImportance {
		t.Errorf("first InProgressTasks importance = %d, want %d",
			got.InProgressTasks[0].Importance, want.wantImportance)
	}
}

func TestBuildDailyBriefing_NilStores(t *testing.T) {
	t.Parallel()

	got, err := BuildDailyBriefing(context.Background(), nil, time.Now())
	if err == nil {
		t.Fatalf("expected error for nil stores, got briefing %+v", got)
	}
	if got != nil {
		t.Fatalf("expected nil briefing, got %+v", got)
	}
}
