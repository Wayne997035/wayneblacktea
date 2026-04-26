package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/waynechen/wayneblacktea/internal/db"
	"github.com/waynechen/wayneblacktea/internal/decision"
	"github.com/waynechen/wayneblacktea/internal/gtd"
	"github.com/waynechen/wayneblacktea/internal/handler"
	apimw "github.com/waynechen/wayneblacktea/internal/middleware"
	"github.com/waynechen/wayneblacktea/internal/session"
	"github.com/waynechen/wayneblacktea/internal/workspace"
)

// ---- fake store implementations ----

type fakeGTDStore struct {
	goals       []db.Goal
	projects    []db.Project
	tasks       []db.Task
	createdGoal *db.Goal
	createdProj *db.Project
	createdTask *db.Task
	updatedTask *db.Task
	updatedProj *db.Project
	completed   *db.Task
	completed_  int64
	total_      int64
	err         error
}

func (f *fakeGTDStore) ActiveGoals(_ context.Context) ([]db.Goal, error) {
	return f.goals, f.err
}
func (f *fakeGTDStore) ListActiveProjects(_ context.Context) ([]db.Project, error) {
	return f.projects, f.err
}
func (f *fakeGTDStore) CreateGoal(_ context.Context, _ gtd.CreateGoalParams) (*db.Goal, error) {
	return f.createdGoal, f.err
}
func (f *fakeGTDStore) CreateProject(_ context.Context, _ gtd.CreateProjectParams) (*db.Project, error) {
	return f.createdProj, f.err
}
func (f *fakeGTDStore) Tasks(_ context.Context, _ *uuid.UUID) ([]db.Task, error) {
	return f.tasks, f.err
}
func (f *fakeGTDStore) CreateTask(_ context.Context, _ gtd.CreateTaskParams) (*db.Task, error) {
	return f.createdTask, f.err
}
func (f *fakeGTDStore) CompleteTask(_ context.Context, _ uuid.UUID, _ *string) (*db.Task, error) {
	return f.completed, f.err
}
func (f *fakeGTDStore) UpdateTaskStatus(_ context.Context, _ uuid.UUID, _ gtd.TaskStatus) (*db.Task, error) {
	return f.updatedTask, f.err
}
func (f *fakeGTDStore) UpdateProjectStatus(_ context.Context, _ uuid.UUID, _ gtd.ProjectStatus) (*db.Project, error) {
	return f.updatedProj, f.err
}
func (f *fakeGTDStore) WeeklyProgress(_ context.Context) (int64, int64, error) {
	return f.completed_, f.total_, f.err
}

type fakeSessionStore struct {
	handoff   *db.SessionHandoff
	setResult *db.SessionHandoff
	err       error
}

func (f *fakeSessionStore) LatestHandoff(_ context.Context) (*db.SessionHandoff, error) {
	return f.handoff, f.err
}
func (f *fakeSessionStore) SetHandoff(_ context.Context, _ session.HandoffParams) (*db.SessionHandoff, error) {
	return f.setResult, f.err
}

type fakeWorkspaceStore struct {
	repos []db.Repo
	repo  *db.Repo
	err   error
}

func (f *fakeWorkspaceStore) ActiveRepos(_ context.Context) ([]db.Repo, error) {
	return f.repos, f.err
}
func (f *fakeWorkspaceStore) UpsertRepo(_ context.Context, _ workspace.UpsertRepoParams) (*db.Repo, error) {
	return f.repo, f.err
}

type fakeDecisionStore struct {
	list []db.Decision
	item *db.Decision
	err  error
}

func (f *fakeDecisionStore) ByRepo(_ context.Context, _ string, _ int32) ([]db.Decision, error) {
	return f.list, f.err
}
func (f *fakeDecisionStore) ByProject(_ context.Context, _ uuid.UUID, _ int32) ([]db.Decision, error) {
	return f.list, f.err
}
func (f *fakeDecisionStore) Log(_ context.Context, _ decision.LogParams) (*db.Decision, error) {
	return f.item, f.err
}

// ---- helpers ----

func newEcho() *echo.Echo {
	return echo.New()
}

func performRequest(e *echo.Echo, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// ---- GTD handler tests ----

func TestGTDHandler_ListGoals(t *testing.T) {
	goalID := uuid.New()
	cases := []struct {
		name     string
		store    *fakeGTDStore
		wantCode int
	}{
		{
			name:     "returns active goals",
			store:    &fakeGTDStore{goals: []db.Goal{{ID: goalID, Title: "Finish MVP"}}},
			wantCode: http.StatusOK,
		},
		{
			name:     "store error → 500",
			store:    &fakeGTDStore{err: errors.New("db down")},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:     "empty list → 200 with empty array",
			store:    &fakeGTDStore{goals: []db.Goal{}},
			wantCode: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewGTDHandler(tc.store)
			e.GET("/api/goals", h.ListGoals)
			rec := performRequest(e, http.MethodGet, "/api/goals", "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

func TestGTDHandler_CreateGoal(t *testing.T) {
	newGoal := &db.Goal{ID: uuid.New(), Title: "Launch v1"}
	cases := []struct {
		name     string
		body     string
		store    *fakeGTDStore
		wantCode int
	}{
		{
			name:     "creates goal",
			body:     `{"title":"Launch v1"}`,
			store:    &fakeGTDStore{createdGoal: newGoal},
			wantCode: http.StatusCreated,
		},
		{
			name:     "missing title → 400",
			body:     `{"area":"work"}`,
			store:    &fakeGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid JSON → 400",
			body:     `{invalid`,
			store:    &fakeGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "store error → 500",
			body:     `{"title":"Launch v1"}`,
			store:    &fakeGTDStore{err: errors.New("db write fail")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewGTDHandler(tc.store)
			e.POST("/api/goals", h.CreateGoal)
			rec := performRequest(e, http.MethodPost, "/api/goals", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

func TestGTDHandler_CreateProject(t *testing.T) {
	newProj := &db.Project{ID: uuid.New(), Name: "wayneblacktea", Title: "Personal OS"}
	cases := []struct {
		name     string
		body     string
		store    *fakeGTDStore
		wantCode int
	}{
		{
			name:     "creates project",
			body:     `{"name":"wayneblacktea","title":"Personal OS"}`,
			store:    &fakeGTDStore{createdProj: newProj},
			wantCode: http.StatusCreated,
		},
		{
			name:     "missing name → 400",
			body:     `{"title":"Personal OS"}`,
			store:    &fakeGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "missing title → 400",
			body:     `{"name":"wayneblacktea"}`,
			store:    &fakeGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "conflict → 409",
			body:     `{"name":"dup","title":"Dup"}`,
			store:    &fakeGTDStore{err: gtd.ErrConflict},
			wantCode: http.StatusConflict,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewGTDHandler(tc.store)
			e.POST("/api/projects", h.CreateProject)
			rec := performRequest(e, http.MethodPost, "/api/projects", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

func TestGTDHandler_GetProject(t *testing.T) {
	id := uuid.New()
	proj := db.Project{ID: id, Name: "wayneblacktea", Title: "Personal OS"}
	cases := []struct {
		name     string
		paramID  string
		store    *fakeGTDStore
		wantCode int
	}{
		{
			name:     "found",
			paramID:  id.String(),
			store:    &fakeGTDStore{projects: []db.Project{proj}},
			wantCode: http.StatusOK,
		},
		{
			name:     "invalid UUID → 400",
			paramID:  "not-a-uuid",
			store:    &fakeGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "not in list → 404",
			paramID:  uuid.New().String(),
			store:    &fakeGTDStore{projects: []db.Project{proj}},
			wantCode: http.StatusNotFound,
		},
		{
			name:     "store error → 500",
			paramID:  id.String(),
			store:    &fakeGTDStore{err: errors.New("db")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewGTDHandler(tc.store)
			e.GET("/api/projects/:id", h.GetProject)
			rec := performRequest(e, http.MethodGet, "/api/projects/"+tc.paramID, "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

func TestGTDHandler_UpdateProjectStatus(t *testing.T) {
	id := uuid.New()
	updated := &db.Project{ID: id, Status: "completed"}
	cases := []struct {
		name     string
		paramID  string
		body     string
		store    *fakeGTDStore
		wantCode int
	}{
		{
			name:     "updates status",
			paramID:  id.String(),
			body:     `{"status":"completed"}`,
			store:    &fakeGTDStore{updatedProj: updated},
			wantCode: http.StatusOK,
		},
		{
			name:     "invalid UUID → 400",
			paramID:  "bad",
			body:     `{"status":"completed"}`,
			store:    &fakeGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "missing status → 400",
			paramID:  id.String(),
			body:     `{}`,
			store:    &fakeGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "not found → 404",
			paramID:  id.String(),
			body:     `{"status":"completed"}`,
			store:    &fakeGTDStore{err: gtd.ErrNotFound},
			wantCode: http.StatusNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewGTDHandler(tc.store)
			e.PATCH("/api/projects/:id/status", h.UpdateProjectStatus)
			rec := performRequest(e, http.MethodPatch, "/api/projects/"+tc.paramID+"/status", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

func TestGTDHandler_CompleteTask(t *testing.T) {
	id := uuid.New()
	done := &db.Task{ID: id, Status: "completed"}
	cases := []struct {
		name     string
		paramID  string
		body     string
		store    *fakeGTDStore
		wantCode int
	}{
		{
			name:     "completes task",
			paramID:  id.String(),
			body:     `{}`,
			store:    &fakeGTDStore{completed: done},
			wantCode: http.StatusOK,
		},
		{
			name:     "with artifact",
			paramID:  id.String(),
			body:     `{"artifact":"https://example.com/pr/1"}`,
			store:    &fakeGTDStore{completed: done},
			wantCode: http.StatusOK,
		},
		{
			name:     "invalid UUID → 400",
			paramID:  "not-uuid",
			body:     `{}`,
			store:    &fakeGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "not found → 404",
			paramID:  id.String(),
			body:     `{}`,
			store:    &fakeGTDStore{err: gtd.ErrNotFound},
			wantCode: http.StatusNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewGTDHandler(tc.store)
			e.PATCH("/api/tasks/:id/complete", h.CompleteTask)
			rec := performRequest(e, http.MethodPatch, "/api/tasks/"+tc.paramID+"/complete", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// ---- Workspace handler tests ----

func TestWorkspaceHandler_ListRepos(t *testing.T) {
	cases := []struct {
		name     string
		store    *fakeWorkspaceStore
		wantCode int
	}{
		{
			name:     "returns repos",
			store:    &fakeWorkspaceStore{repos: []db.Repo{{ID: uuid.New(), Name: "wayneblacktea"}}},
			wantCode: http.StatusOK,
		},
		{
			name:     "store error → 500",
			store:    &fakeWorkspaceStore{err: errors.New("db error")},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:     "empty list → 200",
			store:    &fakeWorkspaceStore{repos: []db.Repo{}},
			wantCode: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewWorkspaceHandler(tc.store)
			e.GET("/api/workspace/repos", h.ListRepos)
			rec := performRequest(e, http.MethodGet, "/api/workspace/repos", "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}

func TestWorkspaceHandler_UpsertRepo(t *testing.T) {
	repo := &db.Repo{ID: uuid.New(), Name: "wayneblacktea"}
	cases := []struct {
		name     string
		body     string
		store    *fakeWorkspaceStore
		wantCode int
	}{
		{
			name:     "upserts repo",
			body:     `{"name":"wayneblacktea","language":"Go"}`,
			store:    &fakeWorkspaceStore{repo: repo},
			wantCode: http.StatusOK,
		},
		{
			name:     "missing name → 400",
			body:     `{"language":"Go"}`,
			store:    &fakeWorkspaceStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid JSON → 400",
			body:     `{bad`,
			store:    &fakeWorkspaceStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "store error → 500",
			body:     `{"name":"wayneblacktea"}`,
			store:    &fakeWorkspaceStore{err: errors.New("write fail")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewWorkspaceHandler(tc.store)
			e.POST("/api/workspace/repos", h.UpsertRepo)
			rec := performRequest(e, http.MethodPost, "/api/workspace/repos", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// ---- Decision handler tests ----

func TestDecisionHandler_ListDecisions(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		store    *fakeDecisionStore
		wantCode int
	}{
		{
			name:     "list all (no filter)",
			query:    "",
			store:    &fakeDecisionStore{list: []db.Decision{{ID: uuid.New(), Title: "Use Echo"}}},
			wantCode: http.StatusOK,
		},
		{
			name:     "filter by repo",
			query:    "?repo_name=wayneblacktea",
			store:    &fakeDecisionStore{list: []db.Decision{}},
			wantCode: http.StatusOK,
		},
		{
			name:     "filter by project_id",
			query:    "?project_id=" + uuid.New().String(),
			store:    &fakeDecisionStore{list: []db.Decision{}},
			wantCode: http.StatusOK,
		},
		{
			name:     "invalid project_id → 400",
			query:    "?project_id=not-uuid",
			store:    &fakeDecisionStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "store error → 500",
			query:    "",
			store:    &fakeDecisionStore{err: errors.New("db error")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewDecisionHandler(tc.store)
			e.GET("/api/decisions", h.ListDecisions)
			rec := performRequest(e, http.MethodGet, "/api/decisions"+tc.query, "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

func TestDecisionHandler_LogDecision(t *testing.T) {
	d := &db.Decision{ID: uuid.New(), Title: "Use Echo"}
	validBody := `{"title":"Use Echo","context":"Need HTTP","decision":"Echo","rationale":"Fast"}`
	cases := []struct {
		name     string
		body     string
		store    *fakeDecisionStore
		wantCode int
	}{
		{
			name:     "logs decision",
			body:     validBody,
			store:    &fakeDecisionStore{item: d},
			wantCode: http.StatusCreated,
		},
		{
			name:     "missing required fields → 400",
			body:     `{"title":"Use Echo"}`,
			store:    &fakeDecisionStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid JSON → 400",
			body:     `{bad`,
			store:    &fakeDecisionStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "store error → 500",
			body:     validBody,
			store:    &fakeDecisionStore{err: errors.New("db write fail")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewDecisionHandler(tc.store)
			e.POST("/api/decisions", h.LogDecision)
			rec := performRequest(e, http.MethodPost, "/api/decisions", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// ---- Session handler tests ----

func TestSessionHandler_GetHandoff(t *testing.T) {
	h1 := &db.SessionHandoff{ID: uuid.New(), Intent: "continue feature"}
	cases := []struct {
		name     string
		store    *fakeSessionStore
		wantCode int
		wantNull bool
	}{
		{
			name:     "returns handoff",
			store:    &fakeSessionStore{handoff: h1},
			wantCode: http.StatusOK,
		},
		{
			name:     "no handoff → 200 null",
			store:    &fakeSessionStore{err: session.ErrNotFound},
			wantCode: http.StatusOK,
			wantNull: true,
		},
		{
			name:     "store error → 500",
			store:    &fakeSessionStore{err: errors.New("db error")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewSessionHandler(tc.store)
			e.GET("/api/session/handoff", h.GetHandoff)
			rec := performRequest(e, http.MethodGet, "/api/session/handoff", "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantNull {
				body := strings.TrimSpace(rec.Body.String())
				if body != "null" {
					t.Errorf("expected null body, got: %s", body)
				}
			}
		})
	}
}

func TestSessionHandler_SetHandoff(t *testing.T) {
	result := &db.SessionHandoff{ID: uuid.New(), Intent: "continue feature"}
	cases := []struct {
		name     string
		body     string
		store    *fakeSessionStore
		wantCode int
	}{
		{
			name:     "sets handoff",
			body:     `{"intent":"continue feature","repo_name":"wayneblacktea"}`,
			store:    &fakeSessionStore{setResult: result},
			wantCode: http.StatusCreated,
		},
		{
			name:     "missing intent → 400",
			body:     `{"repo_name":"wayneblacktea"}`,
			store:    &fakeSessionStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid JSON → 400",
			body:     `{bad`,
			store:    &fakeSessionStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "store error → 500",
			body:     `{"intent":"continue feature"}`,
			store:    &fakeSessionStore{err: errors.New("db write fail")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewSessionHandler(tc.store)
			e.POST("/api/session/handoff", h.SetHandoff)
			rec := performRequest(e, http.MethodPost, "/api/session/handoff", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// ---- Context handler tests ----

func TestContextHandler_GetTodayContext(t *testing.T) {
	now := time.Now()
	_ = now // used in setup
	cases := []struct {
		name      string
		gtdStore  *fakeGTDStore
		sessStore *fakeSessionStore
		wantCode  int
	}{
		{
			name: "returns full context",
			gtdStore: &fakeGTDStore{
				goals:      []db.Goal{{ID: uuid.New(), Title: "Goal 1"}},
				projects:   []db.Project{{ID: uuid.New(), Name: "proj1", Title: "Project 1"}},
				completed_: 3,
				total_:     10,
			},
			sessStore: &fakeSessionStore{err: session.ErrNotFound},
			wantCode:  http.StatusOK,
		},
		{
			name: "includes pending handoff",
			gtdStore: &fakeGTDStore{
				goals:    []db.Goal{},
				projects: []db.Project{},
			},
			sessStore: &fakeSessionStore{handoff: &db.SessionHandoff{ID: uuid.New(), Intent: "continue"}},
			wantCode:  http.StatusOK,
		},
		{
			name:      "gtd error → 500",
			gtdStore:  &fakeGTDStore{err: errors.New("db fail")},
			sessStore: &fakeSessionStore{},
			wantCode:  http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewContextHandler(tc.gtdStore, tc.sessStore)
			e.GET("/api/context/today", h.GetTodayContext)
			rec := performRequest(e, http.MethodGet, "/api/context/today", "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode == http.StatusOK {
				var resp map[string]json.RawMessage
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Errorf("response not valid JSON: %v", err)
				}
				for _, key := range []string{"goals", "projects", "weekly_progress"} {
					if _, ok := resp[key]; !ok {
						t.Errorf("missing key %q in response", key)
					}
				}
			}
		})
	}
}

// ---- API key middleware tests ----

func TestAPIKeyMiddleware(t *testing.T) {
	const testKey = "test-secret-key"
	cases := []struct {
		name     string
		key      string
		wantCode int
	}{
		{
			name:     "valid key → passes through",
			key:      testKey,
			wantCode: http.StatusOK,
		},
		{
			name:     "missing key → 401",
			key:      "",
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "wrong key → 401",
			key:      "wrong-key",
			wantCode: http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			e.Use(apimw.APIKeyMiddleware(testKey))
			e.GET("/test", func(c echo.Context) error {
				return c.String(http.StatusOK, "ok")
			})

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tc.key != "" {
				req.Header.Set("X-API-Key", tc.key)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}
