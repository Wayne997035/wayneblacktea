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

func TestTimelineHandler_GetTimeline(t *testing.T) {
	sampleEvent := timeline.Event{
		Kind:       timeline.KindDecision,
		OccurredAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		RefID:      "some-id",
		Title:      "Use Echo framework",
	}

	cases := []struct {
		name      string
		query     string
		agg       *fakeTimelineAggregator
		wantCode  int
		checkBody func(t *testing.T, body []byte)
	}{
		{
			name:     "default params → 200 with events slice",
			query:    "",
			agg:      &fakeTimelineAggregator{events: []timeline.Event{sampleEvent}},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var resp struct {
					Events []timeline.Event `json:"events"`
					From   string           `json:"from"`
					To     string           `json:"to"`
				}
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				if len(resp.Events) != 1 {
					t.Errorf("want 1 event, got %d", len(resp.Events))
				}
				if resp.From == "" || resp.To == "" {
					t.Error("from and to must be non-empty")
				}
			},
		},
		{
			name:     "valid from/to → 200",
			query:    "?from=2026-04-01T00:00:00Z&to=2026-05-01T00:00:00Z",
			agg:      &fakeTimelineAggregator{events: []timeline.Event{}},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var resp struct {
					Events []timeline.Event `json:"events"`
				}
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				if resp.Events == nil {
					t.Error("events must be [] not null")
				}
			},
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
			name:     "empty events → [] not null",
			query:    "",
			agg:      &fakeTimelineAggregator{events: []timeline.Event{}},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var resp struct {
					Events []timeline.Event `json:"events"`
				}
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				if resp.Events == nil {
					t.Error("events must be empty array [], not null")
				}
			},
		},
		{
			name:     "nil events from aggregator → [] not null",
			query:    "",
			agg:      &fakeTimelineAggregator{events: nil},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var resp struct {
					Events []timeline.Event `json:"events"`
				}
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				if resp.Events == nil {
					t.Error("events must be empty array [], not null")
				}
			},
		},
		{
			name:     "from=to (zero range) → 200",
			query:    "?from=2026-05-01T00:00:00Z&to=2026-05-01T00:00:00Z",
			agg:      &fakeTimelineAggregator{events: []timeline.Event{}},
			wantCode: http.StatusOK,
		},
	}

	for _, tc := range cases {
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
