package notion

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/google/uuid"
)

// BriefingStores is the minimum store surface BuildDailyBriefing needs.
// Defined as an interface so tests can inject fakes without spinning up
// Postgres. The concrete *gtd.Store / *learning.Store / *proposal.Store /
// *decision.Store types satisfy this surface by structural conformance — see
// cmd/server/main.go for the production wiring.
type BriefingStores interface {
	Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error)
	DueReviews(ctx context.Context, limit int) ([]learning.DueReview, error)
	ListPending(ctx context.Context) ([]db.PendingProposal, error)
	// All returns the most recent decisions across the workspace; we then
	// filter to the past 24h in BuildDailyBriefing because the existing SQL
	// query orders by created_at DESC and accepts a limit.
	All(ctx context.Context, limit int32) ([]db.Decision, error)
	// WeeklyProgress returns (completed-this-week, total-active) task counts.
	WeeklyProgress(ctx context.Context) (completed, total int64, err error)
}

// TaskBlock is a single in-progress task projected into a Notion-friendly
// shape. The fields map 1:1 to the columns the briefing renders.
type TaskBlock struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Importance int    `json:"importance"` // 1 = highest, 3 = default
	Context    string `json:"context,omitempty"`
}

// ReviewBlock represents a concept due for spaced-repetition review.
type ReviewBlock struct {
	ConceptID string    `json:"concept_id"`
	Title     string    `json:"title"`
	DueDate   time.Time `json:"due_date"`
}

// ProposalBlock represents a proposal awaiting user resolution.
type ProposalBlock struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	ProposedBy string    `json:"proposed_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// DecisionBlock represents a decision logged in the past 24 hours.
type DecisionBlock struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	RepoName  string    `json:"repo_name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// HealthBlock summarises stuck-task signals + weekly progress so the morning
// briefing surfaces "where Claude probably forgot to close out work" without
// requiring the reader to open the system_health tool.
type HealthBlock struct {
	StuckTaskCount      int      `json:"stuck_task_count"`
	StuckTaskIDs        []string `json:"stuck_task_ids,omitempty"`
	WeeklyCompletedTask int64    `json:"weekly_completed_tasks"`
	WeeklyTotalActive   int64    `json:"weekly_total_active_tasks"`
}

// DailyBriefing is the read-only daily snapshot the scheduler writes to
// Notion at 08:00 Asia/Taipei. The shape is deliberately small so the
// briefing fits on a phone screen.
type DailyBriefing struct {
	Date             time.Time       `json:"date"`
	InProgressTasks  []TaskBlock     `json:"in_progress_tasks"`
	DueReviews       []ReviewBlock   `json:"due_reviews"`
	PendingProposals []ProposalBlock `json:"pending_proposals"`
	RecentDecisions  []DecisionBlock `json:"recent_decisions"`
	SystemHealth     HealthBlock     `json:"system_health"`
}

// stuckTaskThreshold matches internal/mcp/tools_health.go:61 — anything
// in_progress longer than this counts as stuck. Kept in sync with the MCP
// system_health tool default so users see consistent numbers across
// surfaces (briefing vs. tool call).
const stuckTaskThreshold = 4 * time.Hour

// recentDecisionWindow is the lookback window for "decisions made today".
// 24 hours rather than "since 00:00" so an 8 AM briefing always includes
// the previous afternoon/evening's work.
const recentDecisionWindow = 24 * time.Hour

// recentDecisionsFetchLimit caps the SQL pull when we filter client-side by
// time. 100 is generous: even a heavy day rarely exceeds 20 decisions.
const recentDecisionsFetchLimit = 100

// dueReviewsFetchLimit matches the system_health tool's implicit budget.
// 50 is comfortably more than a daily morning queue.
const dueReviewsFetchLimit = 50

// BuildDailyBriefing aggregates personal-OS state from all relevant stores
// into a DailyBriefing snapshot. It returns an error only when EVERY store
// call fails — partial failure is logged via the returned briefing's empty
// sections, because the morning push should never be cancelled outright by
// (e.g.) a transient decisions query failure when in-progress tasks pulled
// fine.
func BuildDailyBriefing(ctx context.Context, stores BriefingStores, now time.Time) (*DailyBriefing, error) {
	if stores == nil {
		return nil, errors.New("notion briefing: stores must not be nil")
	}

	briefing := &DailyBriefing{Date: now}
	var firstErr error

	keepFirstErr(&firstErr, fillTasks(ctx, stores, briefing, now))
	keepFirstErr(&firstErr, fillReviews(ctx, stores, briefing))
	keepFirstErr(&firstErr, fillProposals(ctx, stores, briefing))
	keepFirstErr(&firstErr, fillDecisions(ctx, stores, briefing, now))
	keepFirstErr(&firstErr, fillWeeklyProgress(ctx, stores, briefing))

	// Surface the first failure so the caller can log a Warn, but keep the
	// (partially populated) briefing so we still write whatever we have.
	// Only fail outright when every section is empty AND we saw an error —
	// that means we have nothing useful to push.
	if firstErr != nil && briefingIsEmpty(briefing) {
		return nil, firstErr
	}
	return briefing, nil
}

// keepFirstErr sets *dst to err when dst is nil and err is non-nil.
// Used to accumulate the first partial-failure from independent fill functions.
func keepFirstErr(dst *error, err error) {
	if err != nil && *dst == nil {
		*dst = err
	}
}

// briefingIsEmpty reports whether every data section in b is empty.
func briefingIsEmpty(b *DailyBriefing) bool {
	return len(b.InProgressTasks) == 0 && len(b.DueReviews) == 0 &&
		len(b.PendingProposals) == 0 && len(b.RecentDecisions) == 0
}

// fillTasks loads in-progress tasks and computes stuck-task health signals.
func fillTasks(ctx context.Context, stores BriefingStores, b *DailyBriefing, now time.Time) error {
	tasks, err := stores.Tasks(ctx, nil)
	if err != nil {
		return fmt.Errorf("listing tasks: %w", err)
	}
	stuckCutoff := now.Add(-stuckTaskThreshold)
	for _, t := range tasks {
		if t.Status != "in_progress" {
			continue
		}
		block := TaskBlock{ID: t.ID.String(), Title: t.Title, Importance: 3}
		if t.Importance.Valid {
			block.Importance = int(t.Importance.Int16)
		}
		if t.Context.Valid {
			block.Context = t.Context.String
		}
		b.InProgressTasks = append(b.InProgressTasks, block)
		if t.UpdatedAt.Valid && t.UpdatedAt.Time.Before(stuckCutoff) {
			b.SystemHealth.StuckTaskCount++
			b.SystemHealth.StuckTaskIDs = append(b.SystemHealth.StuckTaskIDs, t.ID.String())
		}
	}
	return nil
}

// fillReviews loads concepts due for spaced-repetition review.
func fillReviews(ctx context.Context, stores BriefingStores, b *DailyBriefing) error {
	reviews, err := stores.DueReviews(ctx, dueReviewsFetchLimit)
	if err != nil {
		return fmt.Errorf("listing due reviews: %w", err)
	}
	for _, r := range reviews {
		b.DueReviews = append(b.DueReviews, ReviewBlock{
			ConceptID: r.ConceptID.String(),
			Title:     r.Title,
			DueDate:   r.DueDate,
		})
	}
	return nil
}

// fillProposals loads proposals awaiting user resolution.
func fillProposals(ctx context.Context, stores BriefingStores, b *DailyBriefing) error {
	proposals, err := stores.ListPending(ctx)
	if err != nil {
		return fmt.Errorf("listing pending proposals: %w", err)
	}
	for _, p := range proposals {
		block := ProposalBlock{ID: p.ID.String(), Type: p.Type}
		if p.ProposedBy.Valid {
			block.ProposedBy = p.ProposedBy.String
		}
		if p.CreatedAt.Valid {
			block.CreatedAt = p.CreatedAt.Time
		}
		b.PendingProposals = append(b.PendingProposals, block)
	}
	return nil
}

// fillDecisions loads decisions made within the recent window.
func fillDecisions(ctx context.Context, stores BriefingStores, b *DailyBriefing, now time.Time) error {
	decisions, err := stores.All(ctx, recentDecisionsFetchLimit)
	if err != nil {
		return fmt.Errorf("listing recent decisions: %w", err)
	}
	cutoff := now.Add(-recentDecisionWindow)
	for _, d := range decisions {
		if !d.CreatedAt.Valid || d.CreatedAt.Time.Before(cutoff) {
			continue
		}
		block := DecisionBlock{ID: d.ID.String(), Title: d.Title, CreatedAt: d.CreatedAt.Time}
		if d.RepoName.Valid {
			block.RepoName = d.RepoName.String
		}
		b.RecentDecisions = append(b.RecentDecisions, block)
	}
	return nil
}

// fillWeeklyProgress reads the weekly task completion counters.
func fillWeeklyProgress(ctx context.Context, stores BriefingStores, b *DailyBriefing) error {
	completed, total, err := stores.WeeklyProgress(ctx)
	if err != nil {
		return fmt.Errorf("reading weekly progress: %w", err)
	}
	b.SystemHealth.WeeklyCompletedTask = completed
	b.SystemHealth.WeeklyTotalActive = total
	return nil
}
