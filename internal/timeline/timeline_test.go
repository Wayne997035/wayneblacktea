package timeline_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/timeline"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ----- fake implementations -----

type fakeTaskSource struct {
	tasks []db.Task
	err   error
}

func (f *fakeTaskSource) Tasks(_ context.Context, _ *uuid.UUID) ([]db.Task, error) {
	return f.tasks, f.err
}

type fakeDecisionSource struct {
	decisions []db.Decision
	err       error
}

func (f *fakeDecisionSource) All(_ context.Context, _ int32) ([]db.Decision, error) {
	return f.decisions, f.err
}

type fakeActivitySource struct {
	logs []db.ActivityLog
	err  error
}

func (f *fakeActivitySource) ListActivityLogsSince(_ context.Context, _ time.Time, _ int32) ([]db.ActivityLog, error) {
	return f.logs, f.err
}

type fakeKnowledgeSource struct {
	items []db.KnowledgeItem
	err   error
}

func (f *fakeKnowledgeSource) List(_ context.Context, _, _ int) ([]db.KnowledgeItem, error) {
	return f.items, f.err
}

type fakeConceptSource struct {
	concepts []db.Concept
	err      error
}

func (f *fakeConceptSource) ListConcepts(_ context.Context, _ int) ([]db.Concept, error) {
	return f.concepts, f.err
}

type fakeReviewSource struct {
	reviews []learning.DueReview
	err     error
}

func (f *fakeReviewSource) ReviewedSince(_ context.Context, _ time.Time, _ int) ([]learning.DueReview, error) {
	return f.reviews, f.err
}

type fakeHandoffSource struct {
	handoffs []db.SessionHandoff
	err      error
}

func (f *fakeHandoffSource) HandoffsSince(_ context.Context, _ time.Time, _ int) ([]db.SessionHandoff, error) {
	return f.handoffs, f.err
}

// ----- helpers -----

func ts(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// ----- tests -----

// TestAggregate_FilterByRange verifies that events outside [from, to] are excluded.
func TestAggregate_FilterByRange(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	from := now.Add(-7 * 24 * time.Hour)
	to := now

	inside := now.Add(-2 * 24 * time.Hour)
	outside := now.Add(-10 * 24 * time.Hour) // before from

	taskID := uuid.New()
	agg := &timeline.Aggregator{
		Tasks: &fakeTaskSource{tasks: []db.Task{
			{ID: taskID, Title: "inside", Status: "pending", CreatedAt: ts(inside)},
			{ID: uuid.New(), Title: "outside", Status: "pending", CreatedAt: ts(outside)},
		}},
	}

	events, err := agg.Aggregate(context.Background(), from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("want 1 event (inside range), got %d: %+v", len(events), events)
	}
	if events[0].RefID != taskID.String() {
		t.Errorf("want task %s, got %s", taskID, events[0].RefID)
	}
}

// TestAggregate_SortedDescending verifies events are sorted by occurred_at DESC.
func TestAggregate_SortedDescending(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	from := now.Add(-30 * 24 * time.Hour)
	to := now

	t1 := now.Add(-3 * 24 * time.Hour)
	t2 := now.Add(-1 * 24 * time.Hour)
	t3 := now.Add(-5 * 24 * time.Hour)

	agg := &timeline.Aggregator{
		Tasks: &fakeTaskSource{tasks: []db.Task{
			{ID: uuid.New(), Title: "three", CreatedAt: ts(t3)},
			{ID: uuid.New(), Title: "one", CreatedAt: ts(t1)},
			{ID: uuid.New(), Title: "two", CreatedAt: ts(t2)},
		}},
	}

	events, err := agg.Aggregate(context.Background(), from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("want 3 events, got %d", len(events))
	}
	// Should be ordered: two (t2) > one (t1) > three (t3)
	wantOrder := []string{"two", "one", "three"}
	for i, want := range wantOrder {
		if events[i].Title != want {
			t.Errorf("events[%d].Title = %q, want %q", i, events[i].Title, want)
		}
	}
}

// TestAggregate_AllKinds verifies all 9 event kinds appear when all sources return data.
func TestAggregate_AllKinds(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	from := now.Add(-30 * 24 * time.Hour)
	to := now
	mid := now.Add(-15 * 24 * time.Hour)

	handoffID := uuid.New()
	taskID := uuid.New()
	agg := &timeline.Aggregator{
		Tasks: &fakeTaskSource{tasks: []db.Task{
			{ID: taskID, Title: "task", CreatedAt: ts(mid), Status: "completed", UpdatedAt: ts(mid)},
		}},
		Decisions: &fakeDecisionSource{decisions: []db.Decision{
			{ID: uuid.New(), Title: "decision", CreatedAt: ts(mid)},
		}},
		Activity: &fakeActivitySource{logs: []db.ActivityLog{
			{ID: uuid.New(), Action: "activity", CreatedAt: ts(mid)},
		}},
		Knowledge: &fakeKnowledgeSource{items: []db.KnowledgeItem{
			{ID: uuid.New(), Title: "knowledge", CreatedAt: ts(mid)},
		}},
		Concepts: &fakeConceptSource{concepts: []db.Concept{
			{ID: uuid.New(), Title: "concept", CreatedAt: ts(mid)},
		}},
		Reviews: &fakeReviewSource{reviews: []learning.DueReview{
			{ScheduleID: uuid.New(), Title: "review", DueDate: mid},
		}},
		Handoffs: &fakeHandoffSource{handoffs: []db.SessionHandoff{
			{ID: handoffID, Intent: "handoff", CreatedAt: ts(mid), ResolvedAt: ts(mid.Add(time.Hour))},
		}},
	}

	events, err := agg.Aggregate(context.Background(), from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// task_created, task_completed, decision, activity, knowledge, concept,
	// review_submitted, handoff_created, handoff_resolved = 9 events
	if len(events) != 9 {
		t.Errorf("want 9 events, got %d", len(events))
		for i, e := range events {
			t.Logf("  events[%d]: kind=%s title=%s", i, e.Kind, e.Title)
		}
	}

	kindSet := make(map[timeline.Kind]bool)
	for _, e := range events {
		kindSet[e.Kind] = true
	}
	expectedKinds := []timeline.Kind{
		timeline.KindTaskCreated, timeline.KindTaskCompleted, timeline.KindDecision,
		timeline.KindActivity, timeline.KindKnowledge, timeline.KindConcept,
		timeline.KindReviewSubmitted, timeline.KindHandoffCreated, timeline.KindHandoffResolved,
	}
	for _, k := range expectedKinds {
		if !kindSet[k] {
			t.Errorf("missing event kind %q", k)
		}
	}
}

// TestAggregate_EmptySourcesReturnEmpty verifies empty sources produce empty slice, no error.
func TestAggregate_EmptySourcesReturnEmpty(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	from := now.Add(-30 * 24 * time.Hour)
	to := now

	agg := &timeline.Aggregator{
		Tasks:     &fakeTaskSource{tasks: []db.Task{}},
		Decisions: &fakeDecisionSource{decisions: []db.Decision{}},
		Activity:  &fakeActivitySource{logs: []db.ActivityLog{}},
		Knowledge: &fakeKnowledgeSource{items: []db.KnowledgeItem{}},
		Concepts:  &fakeConceptSource{concepts: []db.Concept{}},
		Reviews:   &fakeReviewSource{reviews: []learning.DueReview{}},
		Handoffs:  &fakeHandoffSource{handoffs: []db.SessionHandoff{}},
	}

	events, err := agg.Aggregate(context.Background(), from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("want 0 events, got %d", len(events))
	}
}

// TestAggregate_NilSourcesReturnEmpty verifies nil sources are skipped gracefully.
func TestAggregate_NilSourcesReturnEmpty(t *testing.T) {
	now := time.Now().UTC()
	from := now.Add(-30 * 24 * time.Hour)
	to := now

	agg := &timeline.Aggregator{} // all sources nil

	events, err := agg.Aggregate(context.Background(), from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("want 0 events, got %d", len(events))
	}
}

// TestAggregate_SourceErrorPropagated verifies the first error is returned.
func TestAggregate_SourceErrorPropagated(t *testing.T) {
	now := time.Now().UTC()
	from := now.Add(-7 * 24 * time.Hour)
	to := now

	sentinelErr := &testError{"db down"}
	agg := &timeline.Aggregator{
		Decisions: &fakeDecisionSource{err: sentinelErr},
	}

	_, err := agg.Aggregate(context.Background(), from, to)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// TestAggregate_ToExclusive verifies event exactly at "to" is included (inclusive boundary).
func TestAggregate_ToExclusive(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	from := now.Add(-1 * time.Hour)
	to := now

	agg := &timeline.Aggregator{
		Decisions: &fakeDecisionSource{decisions: []db.Decision{
			{ID: uuid.New(), Title: "exactly at to", CreatedAt: ts(now)},
			{ID: uuid.New(), Title: "after to", CreatedAt: ts(now.Add(time.Second))},
		}},
	}

	events, err := agg.Aggregate(context.Background(), from, to)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("want 1 event (at boundary included), got %d", len(events))
	}
	if events[0].Title != "exactly at to" {
		t.Errorf("want 'exactly at to', got %q", events[0].Title)
	}
}
