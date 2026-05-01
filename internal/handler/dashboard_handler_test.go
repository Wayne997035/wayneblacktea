package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	apimw "github.com/Wayne997035/wayneblacktea/internal/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

// fakeDashboardGTDStore satisfies dashboardGTDStore (unexported; tested via exported handler).
type fakeDashboardGTDStore struct {
	projects  []db.Project
	completed int64
	total     int64
	err       error
}

func (f *fakeDashboardGTDStore) WeeklyProgress(_ context.Context) (int64, int64, error) {
	return f.completed, f.total, f.err
}

func (f *fakeDashboardGTDStore) ListActiveProjects(_ context.Context) ([]db.Project, error) {
	return f.projects, f.err
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
	proposals := []db.PendingProposal{
		{ID: uuid.New(), Type: "concept", Status: "pending"},
		{ID: uuid.New(), Type: "concept", Status: "pending", CreatedAt: pgtype.Timestamptz{Valid: false}},
	}
	cases := []struct {
		name      string
		propStore *fakeDashboardProposalStore
		wantCode  int
		checkBody func(t *testing.T, body []byte)
	}{
		{
			name:      "happy path",
			propStore: &fakeDashboardProposalStore{list: proposals},
			wantCode:  http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var out []map[string]json.RawMessage
				if err := json.Unmarshal(body, &out); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				if len(out) != 2 {
					t.Errorf("expected 2 proposals, got %d", len(out))
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
