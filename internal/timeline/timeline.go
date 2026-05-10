// Package timeline aggregates events from multiple domain stores into a
// unified chronological feed for the Personal OS calendar feature.
package timeline

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Kind identifies the type of event in the timeline feed.
type Kind string

const (
	KindTaskCreated     Kind = "task_created"
	KindTaskCompleted   Kind = "task_completed"
	KindTaskDue         Kind = "task_due"
	KindDecision        Kind = "decision"
	KindActivity        Kind = "activity"
	KindKnowledge       Kind = "knowledge"
	KindConcept         Kind = "concept"
	KindReviewSubmitted Kind = "review_submitted"
	KindHandoffCreated  Kind = "handoff_created"
	KindHandoffResolved Kind = "handoff_resolved"
)

// Event is a single unit in the aggregated timeline feed.
type Event struct {
	Kind       Kind      `json:"kind"`
	OccurredAt time.Time `json:"occurred_at"`
	RefID      string    `json:"ref_id"`
	Title      string    `json:"title"`
	RepoName   string    `json:"repo_name,omitempty"`
	ProjectID  string    `json:"project_id,omitempty"`
}

// TaskSource returns tasks optionally filtered by project, plus tasks
// scheduled (due_date) inside an arbitrary range. The first method powers
// historical task_created / task_completed events; the second powers
// forward-looking task_due "planning" events used by the calendar to show
// what is scheduled for tomorrow / next week.
type TaskSource interface {
	Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error)
	// TasksByDueDateRange returns tasks whose status is pending or
	// in_progress AND whose due_date falls in [from, to] (inclusive on both
	// ends). Workspace scoping is applied by the implementation.
	TasksByDueDateRange(ctx context.Context, from, to time.Time) ([]db.Task, error)
}

// DecisionSource returns recent decisions.
type DecisionSource interface {
	All(ctx context.Context, limit int32) ([]db.Decision, error)
}

// ActivitySource returns activity log rows created since a given time.
type ActivitySource interface {
	ListActivityLogsSince(ctx context.Context, since time.Time, maxRows int32) ([]db.ActivityLog, error)
}

// KnowledgeSource lists knowledge items.
type KnowledgeSource interface {
	List(ctx context.Context, limit, offset int) ([]db.KnowledgeItem, error)
}

// ConceptSource lists concepts.
type ConceptSource interface {
	ListConcepts(ctx context.Context, limit int) ([]db.Concept, error)
}

// ReviewSource returns reviews completed since a given time.
type ReviewSource interface {
	ReviewedSince(ctx context.Context, since time.Time, limit int) ([]learning.DueReview, error)
}

// HandoffSource returns handoffs created or resolved since a given time.
type HandoffSource interface {
	HandoffsSince(ctx context.Context, since time.Time, limit int) ([]db.SessionHandoff, error)
}

// Aggregator pulls events from all domain stores and merges them into a
// single sorted timeline.
type Aggregator struct {
	Tasks     TaskSource
	Decisions DecisionSource
	Activity  ActivitySource
	Knowledge KnowledgeSource
	Concepts  ConceptSource
	Reviews   ReviewSource
	Handoffs  HandoffSource
}

// maxRowsPerSource caps the rows fetched from each source at personal OS
// scale; generous enough to avoid missing events within a 366-day window.
const maxRowsPerSource = 10000

// Aggregate returns all events in [from, to], sorted by occurred_at
// descending. Each source is queried independently; the first non-nil error
// encountered is returned (remaining sources still contribute). Pass a
// non-cancellable context if you want best-effort partial results.
func (a *Aggregator) Aggregate(ctx context.Context, from, to time.Time) ([]Event, error) {
	var events []Event
	var firstErr error

	record := func(es []Event, err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
		events = append(events, es...)
	}

	record(a.collectTasks(ctx, from, to))
	record(a.collectDecisions(ctx, from, to))
	record(a.collectActivity(ctx, from, to))
	record(a.collectKnowledge(ctx, from, to))
	record(a.collectConcepts(ctx, from, to))
	record(a.collectReviews(ctx, from, to))
	record(a.collectHandoffs(ctx, from, to))

	sort.Slice(events, func(i, j int) bool {
		return events[i].OccurredAt.After(events[j].OccurredAt)
	})

	return events, firstErr
}

func (a *Aggregator) collectTasks(ctx context.Context, from, to time.Time) ([]Event, error) {
	if a.Tasks == nil {
		return nil, nil
	}
	tasks, err := a.Tasks.Tasks(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("timeline tasks: %w", err)
	}
	var out []Event
	for _, t := range tasks {
		if t.CreatedAt.Valid {
			if e := newEvent(KindTaskCreated, t.CreatedAt.Time, t.ID.String(), t.Title, from, to); e != nil {
				e.ProjectID = uuidFromPgtype(t.ProjectID)
				out = append(out, *e)
			}
		}
		if t.Status == "completed" && t.UpdatedAt.Valid {
			if e := newEvent(KindTaskCompleted, t.UpdatedAt.Time, t.ID.String(), t.Title, from, to); e != nil {
				e.ProjectID = uuidFromPgtype(t.ProjectID)
				out = append(out, *e)
			}
		}
	}

	// Forward-looking planning events: pending / in_progress tasks whose
	// due_date is inside the requested range. Querying via a separate store
	// method lets implementations push the WHERE filter to the DB rather
	// than scanning every task in the workspace.
	dueTasks, err := a.Tasks.TasksByDueDateRange(ctx, from, to)
	if err != nil {
		return out, fmt.Errorf("timeline task_due: %w", err)
	}
	for _, t := range dueTasks {
		if !t.DueDate.Valid {
			continue
		}
		if e := newEvent(KindTaskDue, t.DueDate.Time, t.ID.String(), t.Title, from, to); e != nil {
			e.ProjectID = uuidFromPgtype(t.ProjectID)
			out = append(out, *e)
		}
	}
	return out, nil
}

func (a *Aggregator) collectDecisions(ctx context.Context, from, to time.Time) ([]Event, error) {
	if a.Decisions == nil {
		return nil, nil
	}
	decisions, err := a.Decisions.All(ctx, maxRowsPerSource)
	if err != nil {
		return nil, fmt.Errorf("timeline decisions: %w", err)
	}
	var out []Event
	for _, d := range decisions {
		if d.CreatedAt.Valid {
			if e := newEvent(KindDecision, d.CreatedAt.Time, d.ID.String(), d.Title, from, to); e != nil {
				e.RepoName = d.RepoName.String
				out = append(out, *e)
			}
		}
	}
	return out, nil
}

func (a *Aggregator) collectActivity(ctx context.Context, from, to time.Time) ([]Event, error) {
	if a.Activity == nil {
		return nil, nil
	}
	logs, err := a.Activity.ListActivityLogsSince(ctx, from, maxRowsPerSource)
	if err != nil {
		return nil, fmt.Errorf("timeline activity: %w", err)
	}
	var out []Event
	for _, l := range logs {
		if l.CreatedAt.Valid {
			if e := newEvent(KindActivity, l.CreatedAt.Time, l.ID.String(), l.Action, from, to); e != nil {
				out = append(out, *e)
			}
		}
	}
	return out, nil
}

func (a *Aggregator) collectKnowledge(ctx context.Context, from, to time.Time) ([]Event, error) {
	if a.Knowledge == nil {
		return nil, nil
	}
	items, err := a.Knowledge.List(ctx, maxRowsPerSource, 0)
	if err != nil {
		return nil, fmt.Errorf("timeline knowledge: %w", err)
	}
	var out []Event
	for _, k := range items {
		if k.CreatedAt.Valid {
			if e := newEvent(KindKnowledge, k.CreatedAt.Time, k.ID.String(), k.Title, from, to); e != nil {
				out = append(out, *e)
			}
		}
	}
	return out, nil
}

func (a *Aggregator) collectConcepts(ctx context.Context, from, to time.Time) ([]Event, error) {
	if a.Concepts == nil {
		return nil, nil
	}
	concepts, err := a.Concepts.ListConcepts(ctx, maxRowsPerSource)
	if err != nil {
		return nil, fmt.Errorf("timeline concepts: %w", err)
	}
	var out []Event
	for _, c := range concepts {
		if c.CreatedAt.Valid {
			if e := newEvent(KindConcept, c.CreatedAt.Time, c.ID.String(), c.Title, from, to); e != nil {
				out = append(out, *e)
			}
		}
	}
	return out, nil
}

func (a *Aggregator) collectReviews(ctx context.Context, from, to time.Time) ([]Event, error) {
	if a.Reviews == nil {
		return nil, nil
	}
	reviews, err := a.Reviews.ReviewedSince(ctx, from, maxRowsPerSource)
	if err != nil {
		return nil, fmt.Errorf("timeline reviews: %w", err)
	}
	var out []Event
	for _, r := range reviews {
		// Use DueDate as a proxy for last-review-at ordering (the sqlc
		// query returns rows ordered by last_review_at DESC; DueDate is the
		// post-review scheduled date). We don't have last_review_at exposed
		// in DueReview, but the row was returned because last_review_at >=
		// from, so we use time.Now() as a best-effort fallback when DueDate
		// is zero. In practice DueDate is always set after first review.
		occurredAt := r.DueDate
		if occurredAt.IsZero() {
			occurredAt = time.Now().UTC()
		}
		if e := newEvent(KindReviewSubmitted, occurredAt, r.ScheduleID.String(), r.Title, from, to); e != nil {
			out = append(out, *e)
		}
	}
	return out, nil
}

func (a *Aggregator) collectHandoffs(ctx context.Context, from, to time.Time) ([]Event, error) {
	if a.Handoffs == nil {
		return nil, nil
	}
	handoffs, err := a.Handoffs.HandoffsSince(ctx, from, maxRowsPerSource)
	if err != nil {
		return nil, fmt.Errorf("timeline handoffs: %w", err)
	}
	var out []Event
	for _, h := range handoffs {
		if h.CreatedAt.Valid {
			if e := newEvent(KindHandoffCreated, h.CreatedAt.Time, h.ID.String(), h.Intent, from, to); e != nil {
				e.RepoName = h.RepoName.String
				out = append(out, *e)
			}
		}
		if h.ResolvedAt.Valid {
			if e := newEvent(KindHandoffResolved, h.ResolvedAt.Time, h.ID.String(), h.Intent, from, to); e != nil {
				e.RepoName = h.RepoName.String
				out = append(out, *e)
			}
		}
	}
	return out, nil
}

// newEvent creates an Event only if ts falls within [from, to]; returns nil otherwise.
func newEvent(kind Kind, ts time.Time, refID, title string, from, to time.Time) *Event {
	if ts.IsZero() {
		return nil
	}
	if ts.Before(from) || ts.After(to) {
		return nil
	}
	return &Event{
		Kind:       kind,
		OccurredAt: ts.UTC(),
		RefID:      refID,
		Title:      title,
	}
}

// uuidFromPgtype converts a pgtype.UUID to its string representation.
// Returns "" for invalid (NULL) UUIDs.
func uuidFromPgtype(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
