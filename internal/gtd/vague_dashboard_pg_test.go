//go:build integration

package gtd_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// emptyDecisionStore is a no-op decision store used by the dashboard
// vague-tasks integration test. The endpoint never calls decision.All so
// returning empty is sufficient.
type emptyDecisionStore struct{}

func (s *emptyDecisionStore) All(_ context.Context, _ int32) ([]db.Decision, error) {
	return nil, nil
}

// emptyProposalStore is a no-op proposal store used by the dashboard
// vague-tasks integration test. The endpoint never calls ListPending so
// returning empty is sufficient.
type emptyProposalStore struct{}

func (s *emptyProposalStore) ListPending(_ context.Context) ([]db.PendingProposal, error) {
	return nil, nil
}

// TestDashboard_GetVagueTasks_PG_Integration exercises the full
// dashboard → gtd.Store → real Postgres path for /api/dashboard/vague-tasks.
//
// Seeds three pending tasks:
//   - one with description "TBD" (vague — TBD marker)
//   - one with description "auto-captured from MCP: complete_task" (vague —
//     newly added auto-capture placeholder marker; this is the literal the
//     production regression produced)
//   - one with a descriptive, file:line-referencing description (healthy)
//
// Asserts the endpoint returns count=2 and sample_ids contains both vague
// IDs but not the healthy one. testcontainers PG is mandatory per
// backend-security-design §6.5.
//
// fakeDecisionStore / fakeProposalStore are local empty stubs because the
// vague-tasks endpoint only touches the GTD store; the other dependencies
// are wired into NewDashboardHandler but never invoked for this route.
func TestDashboard_GetVagueTasks_PG_Integration(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	vagueA, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title:       "vague A",
		Description: "TBD",
		Kind:        "feature",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("CreateTask vagueA: %v", err)
	}
	vagueB, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title:       "vague B (auto-capture placeholder literal)",
		Description: "auto-captured from MCP: complete_task",
		Kind:        "fix-pr",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("CreateTask vagueB: %v", err)
	}
	healthy, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title:       "healthy",
		Description: "Refactor internal/storage/factory.go:42 to inject *pgxpool.Pool via constructor",
		Kind:        "refactor",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("CreateTask healthy: %v", err)
	}

	h := handler.NewDashboardHandler(store, &emptyDecisionStore{}, &emptyProposalStore{})
	e := echo.New()
	e.GET("/api/dashboard/vague-tasks", h.GetVagueTasks)

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/dashboard/vague-tasks", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Count     int      `json:"count"`
		SampleIDs []string `json:"sample_ids"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Count != 2 {
		t.Errorf("count = %d, want 2 (body: %s)", resp.Count, rec.Body.String())
	}
	if len(resp.SampleIDs) != 2 {
		t.Fatalf("sample_ids len = %d, want 2 (got %v)", len(resp.SampleIDs), resp.SampleIDs)
	}

	gotIDs := map[string]bool{}
	for _, id := range resp.SampleIDs {
		gotIDs[id] = true
	}
	if !gotIDs[vagueA.ID.String()] {
		t.Errorf("sample_ids missing vagueA %s; got %v", vagueA.ID.String(), resp.SampleIDs)
	}
	if !gotIDs[vagueB.ID.String()] {
		t.Errorf("sample_ids missing vagueB %s; got %v", vagueB.ID.String(), resp.SampleIDs)
	}
	if gotIDs[healthy.ID.String()] {
		t.Errorf("sample_ids unexpectedly contains healthy %s", healthy.ID.String())
	}

	// Defensive: confirm JSON serialises sample_ids as an array (never null) —
	// the handler explicitly normalises nil → []string{} to keep the contract
	// stable for frontend clients.
	if !strings.Contains(rec.Body.String(), `"sample_ids":[`) {
		t.Errorf("body missing sample_ids array marker: %s", rec.Body.String())
	}
}
