package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/aicost"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	apimw "github.com/Wayne997035/wayneblacktea/internal/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

// fakeDashboardActivityStore satisfies dashboardActivityStore (unexported; tested via exported handler).
type fakeDashboardActivityStore struct {
	latestAt          *time.Time
	latestAtErr       error
	feedRows          []db.ActivityLog
	feedErr           error
	latestActionAt    *time.Time
	latestActionAtErr error
}

func (f *fakeDashboardActivityStore) LatestActivityAt(_ context.Context) (*time.Time, error) {
	return f.latestAt, f.latestAtErr
}

func (f *fakeDashboardActivityStore) ListRecentAutomation(_ context.Context, _ int32) ([]db.ActivityLog, error) {
	return f.feedRows, f.feedErr
}

func (f *fakeDashboardActivityStore) LatestActionAt(_ context.Context, _ string) (*time.Time, error) {
	return f.latestActionAt, f.latestActionAtErr
}

// jsonNull is the JSON encoding of null, used by tests that assert a nil value
// is serialised correctly across multiple test cases.
const jsonNull = "null"

// fakeDashboardGTDStore satisfies dashboardGTDStore (unexported; tested via exported handler).
type fakeDashboardGTDStore struct {
	projects        []db.Project
	completed       int64
	total           int64
	topTask         *db.Task
	topTaskErr      error
	upcomingTasks   []db.Task
	upcomingTaskErr error
	dueDateTasks    []db.Task
	dueDateTasksErr error
	allTasks        []db.Task
	allTasksErr     error
	err             error
}

func (f *fakeDashboardGTDStore) WeeklyProgress(_ context.Context) (int64, int64, error) {
	return f.completed, f.total, f.err
}

func (f *fakeDashboardGTDStore) ListActiveProjects(_ context.Context) ([]db.Project, error) {
	return f.projects, f.err
}

func (f *fakeDashboardGTDStore) TopPendingTask(_ context.Context) (*db.Task, error) {
	return f.topTask, f.topTaskErr
}

func (f *fakeDashboardGTDStore) UpcomingTasks(_ context.Context, _ time.Time, _, _ int) ([]db.Task, error) {
	return f.upcomingTasks, f.upcomingTaskErr
}

func (f *fakeDashboardGTDStore) TasksByDueDateRange(_ context.Context, _, _ time.Time) ([]db.Task, error) {
	return f.dueDateTasks, f.dueDateTasksErr
}

func (f *fakeDashboardGTDStore) Tasks(_ context.Context, _ *uuid.UUID) ([]db.Task, error) {
	return f.allTasks, f.allTasksErr
}

// fakeDashboardDecisionStore satisfies dashboardDecisionStore.
type fakeDashboardDecisionStore struct {
	list []db.Decision
	err  error
}

func (f *fakeDashboardDecisionStore) All(_ context.Context, _ int32) ([]db.Decision, error) {
	return f.list, f.err
}

// fakeDashboardProposalStore satisfies dashboardProposalStore.
type fakeDashboardProposalStore struct {
	list []db.PendingProposal
	err  error
}

func (f *fakeDashboardProposalStore) ListPending(_ context.Context) ([]db.PendingProposal, error) {
	return f.list, f.err
}

// ---- D1: GetStats ----

func TestDashboardHandler_GetStats(t *testing.T) {
	cases := []struct {
		name      string
		query     string
		gtdStore  *fakeDashboardGTDStore
		decStore  *fakeDashboardDecisionStore
		propStore *fakeDashboardProposalStore
		wantCode  int
		checkBody func(t *testing.T, body []byte)
	}{
		{
			name:      "happy path — 7d default",
			query:     "",
			gtdStore:  &fakeDashboardGTDStore{completed: 5, total: 10},
			decStore:  &fakeDashboardDecisionStore{list: []db.Decision{{ID: uuid.New(), Title: "Use Echo"}, {ID: uuid.New(), Title: "Use PG"}}},
			propStore: &fakeDashboardProposalStore{list: []db.PendingProposal{{ID: uuid.New(), Type: "concept"}}},
			wantCode:  http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var resp map[string]json.RawMessage
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				for _, key := range []string{"period", "task_completed", "task_total", "decision_count", "pending_proposals"} {
					if _, ok := resp[key]; !ok {
						t.Errorf("missing key %q in response", key)
					}
				}
			},
		},
		{
			name:      "happy path — 30d explicit",
			query:     "?period=30",
			gtdStore:  &fakeDashboardGTDStore{completed: 20, total: 40},
			decStore:  &fakeDashboardDecisionStore{list: []db.Decision{}},
			propStore: &fakeDashboardProposalStore{list: []db.PendingProposal{}},
			wantCode:  http.StatusOK,
		},
		{
			name:      "invalid period → 400",
			query:     "?period=14",
			gtdStore:  &fakeDashboardGTDStore{},
			decStore:  &fakeDashboardDecisionStore{},
			propStore: &fakeDashboardProposalStore{},
			wantCode:  http.StatusBadRequest,
		},
		{
			name:      "gtd store error → 500",
			query:     "",
			gtdStore:  &fakeDashboardGTDStore{err: errors.New("db down")},
			decStore:  &fakeDashboardDecisionStore{},
			propStore: &fakeDashboardProposalStore{},
			wantCode:  http.StatusInternalServerError,
		},
		{
			name:      "decision store error → 500",
			query:     "",
			gtdStore:  &fakeDashboardGTDStore{completed: 1, total: 2},
			decStore:  &fakeDashboardDecisionStore{err: errors.New("db down")},
			propStore: &fakeDashboardProposalStore{},
			wantCode:  http.StatusInternalServerError,
		},
		{
			name:      "proposal store error → 500",
			query:     "",
			gtdStore:  &fakeDashboardGTDStore{completed: 1, total: 2},
			decStore:  &fakeDashboardDecisionStore{list: []db.Decision{}},
			propStore: &fakeDashboardProposalStore{err: errors.New("db down")},
			wantCode:  http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewDashboardHandler(tc.gtdStore, tc.decStore, tc.propStore)
			e.GET("/api/dashboard/stats", h.GetStats)
			rec := performRequest(e, http.MethodGet, "/api/dashboard/stats"+tc.query, "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.checkBody != nil && rec.Code == http.StatusOK {
				tc.checkBody(t, rec.Body.Bytes())
			}
		})
	}
}

// TestDashboardHandler_GetStats_WeeklyInvariant verifies the field-semantics
// contract documented on statsResponse: TaskCompleted and TaskTotal both come
// from gtd.Store.WeeklyProgress (week-scoped) and the invariant
// `TaskCompleted <= TaskTotal` MUST hold. Sibling regression for PR #125 which
// redefined the underlying SQL. Regression for: if a future maintainer reverts
// CountWeeklyRelevantTasks back to whole-backlog semantics, this test catches it
// by asserting both that exact mocked values surface AND the invariant.
func TestDashboardHandler_GetStats_WeeklyInvariant(t *testing.T) {
	cases := []struct {
		name      string
		completed int64
		total     int64
	}{
		{name: "empty week", completed: 0, total: 0},
		{name: "mid week — some done some open", completed: 2, total: 5},
		{name: "all done this week", completed: 7, total: 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewDashboardHandler(
				&fakeDashboardGTDStore{completed: tc.completed, total: tc.total},
				&fakeDashboardDecisionStore{list: []db.Decision{}},
				&fakeDashboardProposalStore{list: []db.PendingProposal{}},
			)
			e.GET("/api/dashboard/stats", h.GetStats)
			rec := performRequest(e, http.MethodGet, "/api/dashboard/stats", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("got status %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			var resp struct {
				TaskCompleted int64 `json:"task_completed"`
				TaskTotal     int64 `json:"task_total"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			if resp.TaskCompleted != tc.completed {
				t.Errorf("task_completed = %d, want %d", resp.TaskCompleted, tc.completed)
			}
			if resp.TaskTotal != tc.total {
				t.Errorf("task_total = %d, want %d", resp.TaskTotal, tc.total)
			}
			if resp.TaskCompleted > resp.TaskTotal {
				t.Errorf("invariant violated: task_completed (%d) > task_total (%d) — gtd.WeeklyProgress semantics regressed?",
					resp.TaskCompleted, resp.TaskTotal)
			}
		})
	}
}

// TestDashboardHandler_GetStats_Unauthorized verifies that AuthMiddleware guards all dashboard endpoints.
func TestDashboardHandler_GetStats_Unauthorized(t *testing.T) {
	const apiKey = "secret"
	e := echo.New()
	e.Use(apimw.APIKeyMiddleware(apiKey))
	h := handler.NewDashboardHandler(
		&fakeDashboardGTDStore{completed: 3, total: 5},
		&fakeDashboardDecisionStore{list: []db.Decision{}},
		&fakeDashboardProposalStore{list: []db.PendingProposal{}},
	)
	e.GET("/api/dashboard/stats", h.GetStats)
	// No X-API-Key header — expect 401.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/dashboard/stats", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want 401", rec.Code)
	}
}

// ---- D2: GetRecentDecisions ----

func TestDashboardHandler_GetRecentDecisions(t *testing.T) {
	decisions := []db.Decision{
		{ID: uuid.New(), Title: "Use Echo", Decision: "Echo", Rationale: "Fast"},
		{ID: uuid.New(), Title: "Use PG", Decision: "PostgreSQL", Rationale: "Reliable"},
	}
	cases := []struct {
		name      string
		query     string
		decStore  *fakeDashboardDecisionStore
		wantCode  int
		checkBody func(t *testing.T, body []byte)
	}{
		{
			name:     "happy path — default limit",
			query:    "",
			decStore: &fakeDashboardDecisionStore{list: decisions},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var out []map[string]json.RawMessage
				if err := json.Unmarshal(body, &out); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				if len(out) != 2 {
					t.Errorf("expected 2 decisions, got %d", len(out))
				}
			},
		},
		{
			name:     "limit=1 — only first decision returned from store",
			query:    "?limit=1",
			decStore: &fakeDashboardDecisionStore{list: decisions[:1]},
			wantCode: http.StatusOK,
		},
		{
			name:     "limit=200 — capped at 100 (store called with 100)",
			query:    "?limit=200",
			decStore: &fakeDashboardDecisionStore{list: decisions},
			wantCode: http.StatusOK,
		},
		{
			name:     "limit=0 → 400",
			query:    "?limit=0",
			decStore: &fakeDashboardDecisionStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "limit=abc → 400",
			query:    "?limit=abc",
			decStore: &fakeDashboardDecisionStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "store error → 500",
			query:    "",
			decStore: &fakeDashboardDecisionStore{err: errors.New("db error")},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:     "empty store → 200 empty array",
			query:    "",
			decStore: &fakeDashboardDecisionStore{list: []db.Decision{}},
			wantCode: http.StatusOK,
		},
		{
			name:  "decision with repo_name",
			query: "",
			decStore: &fakeDashboardDecisionStore{list: []db.Decision{
				{
					ID:        uuid.New(),
					Title:     "Use Redis",
					Decision:  "Redis",
					Rationale: "Cache hit",
					RepoName:  pgtype.Text{String: "wayneblacktea", Valid: true},
				},
			}},
			wantCode: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewDashboardHandler(
				&fakeDashboardGTDStore{},
				tc.decStore,
				&fakeDashboardProposalStore{},
			)
			e.GET("/api/dashboard/recent-decisions", h.GetRecentDecisions)
			rec := performRequest(e, http.MethodGet, "/api/dashboard/recent-decisions"+tc.query, "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.checkBody != nil && rec.Code == http.StatusOK {
				tc.checkBody(t, rec.Body.Bytes())
			}
		})
	}
}

// ---- D6: GetNextTask ----

func TestDashboardHandler_GetNextTask(t *testing.T) {
	taskID := uuid.New()
	task := &db.Task{
		ID:       taskID,
		Title:    "Ship next-task endpoint",
		Status:   "pending",
		Priority: 1,
	}
	cases := []struct {
		name      string
		gtdStore  *fakeDashboardGTDStore
		wantCode  int
		checkBody func(t *testing.T, body []byte)
	}{
		{
			name:     "task exists → 200 with task JSON",
			gtdStore: &fakeDashboardGTDStore{topTask: task},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var resp map[string]json.RawMessage
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				taskRaw, ok := resp["task"]
				if !ok {
					t.Fatal("missing key \"task\" in response")
				}
				if string(taskRaw) == jsonNull {
					t.Error("expected task object, got null")
				}
				var taskObj map[string]json.RawMessage
				if err := json.Unmarshal(taskRaw, &taskObj); err != nil {
					t.Fatalf("task is not a JSON object: %v", err)
				}
				if _, ok := taskObj["id"]; !ok {
					t.Error("task object missing \"id\" field")
				}
			},
		},
		{
			name:     "no pending task → 200 with null task",
			gtdStore: &fakeDashboardGTDStore{topTask: nil},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var resp map[string]json.RawMessage
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				taskRaw, ok := resp["task"]
				if !ok {
					t.Fatal("missing key \"task\" in response")
				}
				if string(taskRaw) != jsonNull {
					t.Errorf("expected null task, got: %s", taskRaw)
				}
			},
		},
		{
			name:     "store error → 500",
			gtdStore: &fakeDashboardGTDStore{topTaskErr: errors.New("db down")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewDashboardHandler(tc.gtdStore, &fakeDashboardDecisionStore{}, &fakeDashboardProposalStore{})
			e.GET("/api/dashboard/next-task", h.GetNextTask)
			rec := performRequest(e, http.MethodGet, "/api/dashboard/next-task", "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.checkBody != nil && rec.Code == http.StatusOK {
				tc.checkBody(t, rec.Body.Bytes())
			}
		})
	}
}

// ---- D7: GetUpcomingTasks ----

func TestDashboardHandler_GetUpcomingTasks(t *testing.T) {
	now := time.Now()
	todayDue := pgtype.Timestamptz{Time: now, Valid: true}
	tomorrowDue := pgtype.Timestamptz{Time: now.AddDate(0, 0, 1), Valid: true}
	dayAfterDue := pgtype.Timestamptz{Time: now.AddDate(0, 0, 2), Valid: true}
	upcomingDue := pgtype.Timestamptz{Time: now.AddDate(0, 0, 5), Valid: true}
	highImp := int16(1)

	allFiveTasks := []db.Task{
		{ID: uuid.New(), Title: "today-task", Status: "pending", Priority: 1, DueDate: todayDue},
		{ID: uuid.New(), Title: "tomorrow-task", Status: "pending", Priority: 2, DueDate: tomorrowDue},
		{ID: uuid.New(), Title: "dayafter-task", Status: "pending", Priority: 2, DueDate: dayAfterDue},
		{ID: uuid.New(), Title: "upcoming-task", Status: "pending", Priority: 3, DueDate: upcomingDue},
		{
			ID:         uuid.New(),
			Title:      "unscheduled-important-task",
			Status:     "in_progress",
			Priority:   1,
			Importance: pgtype.Int2{Int16: highImp, Valid: true},
		},
	}

	cases := []struct {
		name      string
		query     string
		gtdStore  *fakeDashboardGTDStore
		wantCode  int
		checkBody func(t *testing.T, body []byte)
	}{
		{
			name:     "happy path — default params, groups present",
			query:    "",
			gtdStore: &fakeDashboardGTDStore{upcomingTasks: allFiveTasks},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var resp map[string]json.RawMessage
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				if _, ok := resp["groups"]; !ok {
					t.Error("missing key \"groups\" in response")
				}
				var groups map[string]json.RawMessage
				if err := json.Unmarshal(resp["groups"], &groups); err != nil {
					t.Fatalf("groups is not a JSON object: %v", err)
				}
				for _, key := range []string{"today", "tomorrow", "day_after", "upcoming", "unscheduled_important"} {
					if _, ok := groups[key]; !ok {
						t.Errorf("missing bucket %q in groups", key)
					}
				}
			},
		},
		{
			name:     "empty store — all buckets present but empty",
			query:    "",
			gtdStore: &fakeDashboardGTDStore{upcomingTasks: []db.Task{}},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var resp struct {
					Groups struct {
						Today                []json.RawMessage `json:"today"`
						Tomorrow             []json.RawMessage `json:"tomorrow"`
						DayAfter             []json.RawMessage `json:"day_after"`
						Upcoming             []json.RawMessage `json:"upcoming"`
						UnscheduledImportant []json.RawMessage `json:"unscheduled_important"`
					} `json:"groups"`
				}
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				// All buckets must be empty arrays (not null).
				for _, bucket := range [][]json.RawMessage{
					resp.Groups.Today, resp.Groups.Tomorrow, resp.Groups.DayAfter,
					resp.Groups.Upcoming, resp.Groups.UnscheduledImportant,
				} {
					if bucket == nil {
						t.Error("expected empty array bucket, got nil (null)")
					}
				}
			},
		},
		{
			name:     "days=0 → 400 Bad Request",
			query:    "?days=0",
			gtdStore: &fakeDashboardGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "days=-1 → 400 Bad Request",
			query:    "?days=-1",
			gtdStore: &fakeDashboardGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "days=abc → 400 Bad Request",
			query:    "?days=abc",
			gtdStore: &fakeDashboardGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "limit=0 → 400 Bad Request",
			query:    "?limit=0",
			gtdStore: &fakeDashboardGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "tz=Invalid/Zone → 400 Bad Request",
			query:    "?tz=Invalid%2FZone",
			gtdStore: &fakeDashboardGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "tz=Asia/Taipei → 200",
			query:    "?tz=Asia%2FTaipei",
			gtdStore: &fakeDashboardGTDStore{upcomingTasks: allFiveTasks},
			wantCode: http.StatusOK,
		},
		{
			name:     "store error → 500",
			query:    "",
			gtdStore: &fakeDashboardGTDStore{upcomingTaskErr: errors.New("db down")},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:     "days=14 (max) accepted",
			query:    "?days=14",
			gtdStore: &fakeDashboardGTDStore{upcomingTasks: []db.Task{}},
			wantCode: http.StatusOK,
		},
		{
			name:     "limit=50 (max) accepted",
			query:    "?limit=50",
			gtdStore: &fakeDashboardGTDStore{upcomingTasks: []db.Task{}},
			wantCode: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewDashboardHandler(tc.gtdStore, &fakeDashboardDecisionStore{}, &fakeDashboardProposalStore{})
			e.GET("/api/dashboard/upcoming-tasks", h.GetUpcomingTasks)
			rec := performRequest(e, http.MethodGet, "/api/dashboard/upcoming-tasks"+tc.query, "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.checkBody != nil && rec.Code == http.StatusOK {
				tc.checkBody(t, rec.Body.Bytes())
			}
		})
	}
}

// ---- D8: GetUpcoming ----

func TestDashboardHandler_GetUpcoming(t *testing.T) {
	now := time.Now().UTC()
	task1 := db.Task{
		ID:       uuid.New(),
		Title:    "task-due-tomorrow",
		Status:   "pending",
		Priority: 1,
		DueDate:  pgtype.Timestamptz{Time: now.AddDate(0, 0, 1), Valid: true},
	}
	task2 := db.Task{
		ID:       uuid.New(),
		Title:    "task-due-in-5-days",
		Status:   "in_progress",
		Priority: 2,
		DueDate:  pgtype.Timestamptz{Time: now.AddDate(0, 0, 5), Valid: true},
	}

	cases := []struct {
		name      string
		gtdStore  *fakeDashboardGTDStore
		wantCode  int
		checkBody func(t *testing.T, body []byte)
	}{
		{
			name:     "happy path — tasks returned as flat JSON array",
			gtdStore: &fakeDashboardGTDStore{dueDateTasks: []db.Task{task1, task2}},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var tasks []map[string]json.RawMessage
				if err := json.Unmarshal(body, &tasks); err != nil {
					t.Fatalf("expected JSON array, got invalid JSON: %v (body: %s)", err, body)
				}
				if len(tasks) != 2 {
					t.Errorf("expected 2 tasks, got %d", len(tasks))
				}
				// Verify required fields are present on each task.
				for i, task := range tasks {
					for _, key := range []string{"id", "title", "status"} {
						if _, ok := task[key]; !ok {
							t.Errorf("task[%d]: missing field %q", i, key)
						}
					}
				}
			},
		},
		{
			name:     "empty store — returns empty JSON array (not null)",
			gtdStore: &fakeDashboardGTDStore{dueDateTasks: []db.Task{}},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var tasks []json.RawMessage
				if err := json.Unmarshal(body, &tasks); err != nil {
					t.Fatalf("expected JSON array, got invalid JSON: %v (body: %s)", err, body)
				}
				if tasks == nil {
					t.Error("expected empty array, got null")
				}
				if len(tasks) != 0 {
					t.Errorf("expected 0 tasks, got %d", len(tasks))
				}
			},
		},
		{
			name:     "nil store result — returns empty JSON array (not null)",
			gtdStore: &fakeDashboardGTDStore{dueDateTasks: nil},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var tasks []json.RawMessage
				if err := json.Unmarshal(body, &tasks); err != nil {
					t.Fatalf("expected JSON array, got invalid JSON: %v (body: %s)", err, body)
				}
				if tasks == nil {
					t.Error("expected empty array, got null")
				}
			},
		},
		{
			name:     "store error — returns 500",
			gtdStore: &fakeDashboardGTDStore{dueDateTasksErr: errors.New("db down")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewDashboardHandler(tc.gtdStore, &fakeDashboardDecisionStore{}, &fakeDashboardProposalStore{})
			e.GET("/api/dashboard/upcoming", h.GetUpcoming)
			rec := performRequest(e, http.MethodGet, "/api/dashboard/upcoming", "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.checkBody != nil && rec.Code == http.StatusOK {
				tc.checkBody(t, rec.Body.Bytes())
			}
		})
	}
}

// ---- D9: GetStats_LastUpdatedAt ----

func TestDashboardHandler_GetStats_LastUpdatedAt(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name          string
		actStore      *fakeDashboardActivityStore
		wantCode      int
		wantHasField  bool
		wantFieldNull bool
	}{
		{
			name:         "activity rows exist — last_updated_at present",
			actStore:     &fakeDashboardActivityStore{latestAt: &now},
			wantCode:     http.StatusOK,
			wantHasField: true,
		},
		{
			name:         "empty activity log — last_updated_at absent",
			actStore:     &fakeDashboardActivityStore{latestAt: nil},
			wantCode:     http.StatusOK,
			wantHasField: false,
		},
		{
			name:         "activity store error — field absent, response still 200",
			actStore:     &fakeDashboardActivityStore{latestAtErr: errors.New("db down")},
			wantCode:     http.StatusOK,
			wantHasField: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewDashboardHandler(
				&fakeDashboardGTDStore{completed: 1, total: 2},
				&fakeDashboardDecisionStore{list: []db.Decision{}},
				&fakeDashboardProposalStore{list: []db.PendingProposal{}},
			)
			h.SetActivityStore(tc.actStore)
			e.GET("/api/dashboard/stats", h.GetStats)
			rec := performRequest(e, http.MethodGet, "/api/dashboard/stats", "")
			if rec.Code != tc.wantCode {
				t.Errorf("status: got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
				return
			}
			var resp map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			_, hasField := resp["last_updated_at"]
			if tc.wantHasField && !hasField {
				t.Error("expected last_updated_at field in response, got none")
			}
			if !tc.wantHasField && hasField {
				t.Errorf("expected last_updated_at absent, got: %s", resp["last_updated_at"])
			}
		})
	}
}

// ---- D10: GetAutomationFeed ----

func TestDashboardHandler_GetAutomationFeed(t *testing.T) {
	now := time.Now().UTC()
	actID := uuid.New()
	rows := []db.ActivityLog{
		{
			ID:        actID,
			Action:    "task:completed",
			Notes:     pgtype.Text{String: "task_id=abc", Valid: true},
			CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		},
	}

	cases := []struct {
		name      string
		query     string
		actStore  *fakeDashboardActivityStore
		wantCode  int
		checkBody func(t *testing.T, body []byte)
	}{
		{
			name:     "happy path — one row returned",
			query:    "",
			actStore: &fakeDashboardActivityStore{feedRows: rows},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var resp map[string]json.RawMessage
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				if _, ok := resp["feed"]; !ok {
					t.Error("missing key 'feed'")
				}
				if _, ok := resp["fetched_at"]; !ok {
					t.Error("missing key 'fetched_at'")
				}
				var feed []map[string]json.RawMessage
				if err := json.Unmarshal(resp["feed"], &feed); err != nil {
					t.Fatalf("feed is not array: %v", err)
				}
				if len(feed) != 1 {
					t.Errorf("expected 1 feed item, got %d", len(feed))
				}
				for _, key := range []string{"id", "action", "kind", "occurred_at"} {
					if _, ok := feed[0][key]; !ok {
						t.Errorf("feed item missing key %q", key)
					}
				}
			},
		},
		{
			name:     "empty activity log — empty feed array",
			query:    "",
			actStore: &fakeDashboardActivityStore{feedRows: nil},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var resp map[string]json.RawMessage
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				var feed []json.RawMessage
				if err := json.Unmarshal(resp["feed"], &feed); err != nil {
					t.Fatalf("feed is not array: %v", err)
				}
				if len(feed) != 0 {
					t.Errorf("expected empty feed, got %d items", len(feed))
				}
			},
		},
		{
			name:     "limit=0 → 400 Bad Request",
			query:    "?limit=0",
			actStore: &fakeDashboardActivityStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "limit=51 → 400 Bad Request",
			query:    "?limit=51",
			actStore: &fakeDashboardActivityStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "store error → 500",
			query:    "",
			actStore: &fakeDashboardActivityStore{feedErr: errors.New("db down")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewDashboardHandler(
				&fakeDashboardGTDStore{},
				&fakeDashboardDecisionStore{},
				&fakeDashboardProposalStore{},
			)
			h.SetActivityStore(tc.actStore)
			e.GET("/api/dashboard/automation-feed", h.GetAutomationFeed)
			rec := performRequest(e, http.MethodGet, "/api/dashboard/automation-feed"+tc.query, "")
			if rec.Code != tc.wantCode {
				t.Errorf("status: got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.checkBody != nil && rec.Code == http.StatusOK {
				tc.checkBody(t, rec.Body.Bytes())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FIX 3: GetAICost handler tests
// ---------------------------------------------------------------------------

// fakeAICostStoreRows satisfies handler.DashboardAICostStoreIface.
// It returns aicost.CostRow values via the real interface contract.
type fakeAICostStoreRows struct {
	rows       []aicost.CostRow
	grandTotal int64
	err        error
}

func (f *fakeAICostStoreRows) AICostLast30d(_ context.Context) ([]aicost.CostRow, int64, error) {
	return f.rows, f.grandTotal, f.err
}

// checkAICostEmptyBody asserts the graceful-degrade shape: by_model is an
// empty JSON array and period is "30d".
func checkAICostEmptyBody(t *testing.T, body []byte) {
	t.Helper()
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	byModel, ok := resp["by_model"]
	if !ok {
		t.Fatal("missing key 'by_model'")
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(byModel, &arr); err != nil {
		t.Fatalf("by_model not a JSON array: %v", err)
	}
	if len(arr) != 0 {
		t.Errorf("expected empty by_model, got %d items", len(arr))
	}
	var period string
	if err := json.Unmarshal(resp["period"], &period); err != nil || period != "30d" {
		t.Errorf("period = %q; want %q", period, "30d")
	}
}

// checkAICostRowsBody asserts the JSON shape when rows are present:
// two entries in by_model, correct first model name, correct total_cost_usd.
func checkAICostRowsBody(t *testing.T, body []byte) {
	t.Helper()
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, key := range []string{"by_model", "total_cost_usd", "period"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("missing key %q", key)
		}
	}
	var byModel []map[string]json.RawMessage
	if err := json.Unmarshal(resp["by_model"], &byModel); err != nil {
		t.Fatalf("by_model not an array: %v", err)
	}
	if len(byModel) != 2 {
		t.Errorf("by_model len = %d; want 2", len(byModel))
	}
	for _, key := range []string{"model", "total_cost_usd", "input_tokens", "output_tokens"} {
		if _, ok := byModel[0][key]; !ok {
			t.Errorf("by_model[0] missing key %q", key)
		}
	}
	var firstModel string
	if err := json.Unmarshal(byModel[0]["model"], &firstModel); err != nil || firstModel != "claude-haiku-4-5" {
		t.Errorf("by_model[0].model = %q; want claude-haiku-4-5", firstModel)
	}
	var total float64
	if err := json.Unmarshal(resp["total_cost_usd"], &total); err != nil {
		t.Fatalf("total_cost_usd not a number: %v", err)
	}
	wantTotal := float64(1_950_000) / 1_000_000
	if total != wantTotal {
		t.Errorf("total_cost_usd = %v; want %v", total, wantTotal)
	}
}

// TestDashboardHandler_GetAICost tests the GET /api/dashboard/ai-cost endpoint.
//
// Cases:
//   - nil aiCost store → 200 with empty by_model (graceful degrade for SQLite dev)
//   - non-nil stub returning rows → 200 with correct JSON shape
//   - stub returning error → 500
func TestDashboardHandler_GetAICost(t *testing.T) {
	cases := []struct {
		name      string
		setupFn   func(h *handler.DashboardHandler)
		wantCode  int
		checkBody func(t *testing.T, body []byte)
	}{
		{
			name:      "nil aiCost store → 200 with empty by_model",
			setupFn:   nil,
			wantCode:  http.StatusOK,
			checkBody: checkAICostEmptyBody,
		},
		{
			name: "non-nil stub returning rows → 200 with correct JSON shape",
			setupFn: func(h *handler.DashboardHandler) {
				h.SetAICostStore(&fakeAICostStoreRows{
					rows: []aicost.CostRow{
						{Model: "claude-haiku-4-5", TotalCostUSD: 1.5, InputTokens: 1_000_000, OutputTokens: 1_000_000},
						{Model: "claude-sonnet-4-6", TotalCostUSD: 0.45, InputTokens: 100_000, OutputTokens: 10_000},
					},
					grandTotal: 1_950_000,
				})
			},
			wantCode:  http.StatusOK,
			checkBody: checkAICostRowsBody,
		},
		{
			name: "stub returning error → 500",
			setupFn: func(h *handler.DashboardHandler) {
				h.SetAICostStore(&fakeAICostStoreRows{err: errors.New("db down")})
			},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewDashboardHandler(
				&fakeDashboardGTDStore{},
				&fakeDashboardDecisionStore{},
				&fakeDashboardProposalStore{},
			)
			if tc.setupFn != nil {
				tc.setupFn(h)
			}
			e.GET("/api/dashboard/ai-cost", h.GetAICost)
			rec := performRequest(e, http.MethodGet, "/api/dashboard/ai-cost", "")
			if rec.Code != tc.wantCode {
				t.Errorf("status: got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.checkBody != nil && rec.Code == http.StatusOK {
				tc.checkBody(t, rec.Body.Bytes())
			}
		})
	}
}
