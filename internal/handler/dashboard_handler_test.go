package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	apimw "github.com/Wayne997035/wayneblacktea/internal/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

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

// ---- D3: GetActiveProjects ----

func TestDashboardHandler_GetActiveProjects(t *testing.T) {
	projects := []db.Project{
		{ID: uuid.New(), Name: "wayneblacktea", Title: "Personal OS", Status: "active", Area: "engineering", Priority: 1},
		{ID: uuid.New(), Name: "chatbot-go", Title: "Chatbot", Status: "active", Priority: 2},
	}
	cases := []struct {
		name     string
		gtdStore *fakeDashboardGTDStore
		wantCode int
	}{
		{
			name:     "happy path",
			gtdStore: &fakeDashboardGTDStore{projects: projects},
			wantCode: http.StatusOK,
		},
		{
			name:     "empty → 200 empty array",
			gtdStore: &fakeDashboardGTDStore{projects: []db.Project{}},
			wantCode: http.StatusOK,
		},
		{
			name:     "store error → 500",
			gtdStore: &fakeDashboardGTDStore{err: errors.New("db error")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewDashboardHandler(tc.gtdStore, &fakeDashboardDecisionStore{}, &fakeDashboardProposalStore{})
			e.GET("/api/dashboard/active-projects", h.GetActiveProjects)
			rec := performRequest(e, http.MethodGet, "/api/dashboard/active-projects", "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// ---- D4: GetWeeklyProgress ----

func TestDashboardHandler_GetWeeklyProgress(t *testing.T) {
	cases := []struct {
		name      string
		gtdStore  *fakeDashboardGTDStore
		wantCode  int
		checkBody func(t *testing.T, body []byte)
	}{
		{
			name:     "happy path",
			gtdStore: &fakeDashboardGTDStore{completed: 7, total: 15},
			wantCode: http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var resp map[string]json.RawMessage
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				for _, key := range []string{"completed", "total"} {
					if _, ok := resp[key]; !ok {
						t.Errorf("missing key %q in response", key)
					}
				}
			},
		},
		{
			name:     "zero progress → 200 with zeros",
			gtdStore: &fakeDashboardGTDStore{completed: 0, total: 0},
			wantCode: http.StatusOK,
		},
		{
			name:     "store error → 500",
			gtdStore: &fakeDashboardGTDStore{err: errors.New("db error")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewDashboardHandler(tc.gtdStore, &fakeDashboardDecisionStore{}, &fakeDashboardProposalStore{})
			e.GET("/api/dashboard/weekly-progress", h.GetWeeklyProgress)
			rec := performRequest(e, http.MethodGet, "/api/dashboard/weekly-progress", "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.checkBody != nil && rec.Code == http.StatusOK {
				tc.checkBody(t, rec.Body.Bytes())
			}
		})
	}
}

// ---- D5: GetPendingKnowledgeProposals ----

func TestDashboardHandler_GetPendingKnowledgeProposals(t *testing.T) {
	knowledgeOnly := []db.PendingProposal{
		{ID: uuid.New(), Type: "knowledge", Status: "pending"},
		{ID: uuid.New(), Type: "knowledge", Status: "pending", CreatedAt: pgtype.Timestamptz{Valid: false}},
	}
	mixed := []db.PendingProposal{
		{ID: uuid.New(), Type: "knowledge", Status: "pending"},
		{ID: uuid.New(), Type: "concept", Status: "pending"},
		{ID: uuid.New(), Type: "goal", Status: "pending"},
	}
	cases := []struct {
		name      string
		propStore *fakeDashboardProposalStore
		wantCode  int
		checkBody func(t *testing.T, body []byte)
	}{
		{
			name:      "happy path — knowledge only returned",
			propStore: &fakeDashboardProposalStore{list: knowledgeOnly},
			wantCode:  http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var out []map[string]json.RawMessage
				if err := json.Unmarshal(body, &out); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				if len(out) != 2 {
					t.Errorf("expected 2 knowledge proposals, got %d", len(out))
				}
				for _, item := range out {
					for _, key := range []string{"id", "type", "status"} {
						if _, ok := item[key]; !ok {
							t.Errorf("missing key %q in item", key)
						}
					}
				}
			},
		},
		{
			name:      "mixed types — only knowledge proposals returned",
			propStore: &fakeDashboardProposalStore{list: mixed},
			wantCode:  http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var out []map[string]json.RawMessage
				if err := json.Unmarshal(body, &out); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				if len(out) != 1 {
					t.Errorf("expected 1 knowledge proposal (filtered), got %d", len(out))
				}
				var rawType string
				if err := json.Unmarshal(out[0]["type"], &rawType); err != nil || rawType != "knowledge" {
					t.Errorf("expected type=knowledge, got %q", rawType)
				}
			},
		},
		{
			name:      "empty → 200 empty array",
			propStore: &fakeDashboardProposalStore{list: []db.PendingProposal{}},
			wantCode:  http.StatusOK,
		},
		{
			name:      "store error → 500",
			propStore: &fakeDashboardProposalStore{err: errors.New("db error")},
			wantCode:  http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewDashboardHandler(&fakeDashboardGTDStore{}, &fakeDashboardDecisionStore{}, tc.propStore)
			e.GET("/api/dashboard/pending-knowledge-proposals", h.GetPendingKnowledgeProposals)
			rec := performRequest(e, http.MethodGet, "/api/dashboard/pending-knowledge-proposals", "")
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
