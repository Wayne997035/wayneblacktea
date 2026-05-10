package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/timeline"
	"github.com/labstack/echo/v4"
)

// fakeTimelineAggregator implements timelineAggregator for testing.
type fakeTimelineAggregator struct {
	events []timeline.Event
	err    error
}

func (f *fakeTimelineAggregator) Aggregate(_ context.Context, _, _ time.Time) ([]timeline.Event, error) {
	return f.events, f.err
}

// timelineRespEnvelope mirrors the JSON envelope returned by GetTimeline so
// subtests can decode it without each defining its own anonymous struct.
type timelineRespEnvelope struct {
	Events []timeline.Event `json:"events"`
	From   string           `json:"from"`
	To     string           `json:"to"`
}

// decodeTimelineResp decodes the handler's JSON envelope or fails the test.
// Centralising this here keeps each subtest's checkBody small and keeps the
// outer TestTimelineHandler_GetTimeline below the gocyclo budget.
func decodeTimelineResp(t *testing.T, body []byte) timelineRespEnvelope {
	t.Helper()
	var resp timelineRespEnvelope
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	return resp
}

// timelineCase keeps each test case's data + body assertion together so the
// outer table-driven runner stays small (gocyclo budget).
type timelineCase struct {
	name      string
	query     string
	agg       *fakeTimelineAggregator
	wantCode  int
	checkBody func(t *testing.T, body []byte)
}

func checkDefaultParams(t *testing.T, body []byte) {
	t.Helper()
	resp := decodeTimelineResp(t, body)
	if len(resp.Events) != 1 {
		t.Errorf("want 1 event, got %d", len(resp.Events))
	}
	if resp.From == "" || resp.To == "" {
		t.Error("from and to must be non-empty")
	}
}

func checkEventsNotNil(t *testing.T, body []byte) {
	t.Helper()
	resp := decodeTimelineResp(t, body)
	if resp.Events == nil {
		t.Error("events must be [] not null")
	}
}

func checkTaskDueEvent(t *testing.T, body []byte) {
	t.Helper()
	resp := decodeTimelineResp(t, body)
	if len(resp.Events) != 1 {
		t.Fatalf("want 1 event, got %d", len(resp.Events))
	}
	if resp.Events[0].Kind != timeline.KindTaskDue {
		t.Errorf("want kind=task_due, got %q", resp.Events[0].Kind)
	}
	if resp.Events[0].Title != "review PR #88" {
		t.Errorf("want title='review PR #88', got %q", resp.Events[0].Title)
	}
}

func checkFutureTaskDue(t *testing.T, body []byte) {
	t.Helper()
	resp := decodeTimelineResp(t, body)
	if len(resp.Events) != 1 {
		t.Fatalf("want 1 event, got %d", len(resp.Events))
	}
	if resp.Events[0].Kind != timeline.KindTaskDue {
		t.Errorf("want kind=task_due, got %q", resp.Events[0].Kind)
	}
	// Handler MUST NOT drop future-dated events — they are the
	// whole point of the planning view.
	if !resp.Events[0].OccurredAt.After(time.Now().UTC()) {
		t.Errorf("want occurred_at in future, got %s", resp.Events[0].OccurredAt)
	}
}

func timelineCases() []timelineCase {
	sampleEvent := timeline.Event{
		Kind:       timeline.KindDecision,
		OccurredAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		RefID:      "some-id",
		Title:      "Use Echo framework",
	}
	return []timelineCase{
		{
			name:      "default params → 200 with events slice",
			query:     "",
			agg:       &fakeTimelineAggregator{events: []timeline.Event{sampleEvent}},
			wantCode:  http.StatusOK,
			checkBody: checkDefaultParams,
		},
		{
			name:      "valid from/to → 200",
			query:     "?from=2026-04-01T00:00:00Z&to=2026-05-01T00:00:00Z",
			agg:       &fakeTimelineAggregator{events: []timeline.Event{}},
			wantCode:  http.StatusOK,
			checkBody: checkEventsNotNil,
		},
		{
			name:     "range > 366 days → 400",
			query:    "?from=2024-01-01T00:00:00Z&to=2026-01-01T00:00:00Z",
			agg:      &fakeTimelineAggregator{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "malformed from param → 400",
			query:    "?from=not-a-date&to=2026-05-01T00:00:00Z",
			agg:      &fakeTimelineAggregator{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "malformed to param → 400",
			query:    "?from=2026-04-01T00:00:00Z&to=bad-date",
			agg:      &fakeTimelineAggregator{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "aggregator error → 500",
			query:    "",
			agg:      &fakeTimelineAggregator{err: errors.New("db down")},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:      "empty events → [] not null",
			query:     "",
			agg:       &fakeTimelineAggregator{events: []timeline.Event{}},
			wantCode:  http.StatusOK,
			checkBody: checkEventsNotNil,
		},
		{
			name:      "nil events from aggregator → [] not null",
			query:     "",
			agg:       &fakeTimelineAggregator{events: nil},
			wantCode:  http.StatusOK,
			checkBody: checkEventsNotNil,
		},
		{
			name:     "from=to (zero range) → 200",
			query:    "?from=2026-05-01T00:00:00Z&to=2026-05-01T00:00:00Z",
			agg:      &fakeTimelineAggregator{events: []timeline.Event{}},
			wantCode: http.StatusOK,
		},
		{
			name:     "from after to (reversed range) → 400",
			query:    "?from=2026-05-02T00:00:00Z&to=2026-05-01T00:00:00Z",
			agg:      &fakeTimelineAggregator{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:  "task_due event surfaces in JSON envelope with kind=task_due",
			query: "?from=2026-05-01T00:00:00Z&to=2026-05-30T00:00:00Z",
			agg: &fakeTimelineAggregator{events: []timeline.Event{{
				Kind:       timeline.KindTaskDue,
				OccurredAt: time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC),
				RefID:      "task-uuid",
				Title:      "review PR #88",
			}}},
			wantCode:  http.StatusOK,
			checkBody: checkTaskDueEvent,
		},
		{
			name:  "future-dated task_due event preserved unchanged (planning view)",
			query: "",
			agg: &fakeTimelineAggregator{events: []timeline.Event{{
				Kind:       timeline.KindTaskDue,
				OccurredAt: time.Now().UTC().Add(48 * time.Hour),
				RefID:      "future-task",
				Title:      "scheduled work",
			}}},
			wantCode:  http.StatusOK,
			checkBody: checkFutureTaskDue,
		},
	}
}

func TestTimelineHandler_GetTimeline(t *testing.T) {
	for _, tc := range timelineCases() {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			h := handler.NewTimelineHandler(tc.agg)
			e.GET("/api/timeline", h.GetTimeline)
			rec := performRequest(e, http.MethodGet, "/api/timeline"+tc.query, "")
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
				return
			}
			if tc.checkBody != nil && rec.Code == http.StatusOK {
				tc.checkBody(t, rec.Body.Bytes())
			}
		})
	}
}
