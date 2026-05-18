package sqlite_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/labstack/echo/v4"
)

// emptyDecisionStoreSQLite is a no-op decision store used by the dashboard
// vague-tasks SQLite parity test. The endpoint never invokes decision.All so
// returning empty is sufficient.
type emptyDecisionStoreSQLite struct{}

func (s *emptyDecisionStoreSQLite) All(_ context.Context, _ int32) ([]db.Decision, error) {
	return nil, nil
}

// emptyProposalStoreSQLite is a no-op proposal store used by the dashboard
// vague-tasks SQLite parity test.
type emptyProposalStoreSQLite struct{}

func (s *emptyProposalStoreSQLite) ListPending(_ context.Context) ([]db.PendingProposal, error) {
	return nil, nil
}

// TestDashboard_GetVagueTasks_SQLite_Integration is the SQLite parity for the
// PG-side TestDashboard_GetVagueTasks_PG_Integration test (gtd package). Same
// scenario: seed two vague pending tasks (one with TBD marker, one with the
// auto-capture placeholder literal) and one healthy descriptive task, hit the
// HTTP endpoint, and assert count=2 with both vague IDs in the sample.
//
// Dual-backend parity is mandatory per backend-security-design §6.5 — the
// underlying logic is shared (validator.CheckVagueness on the same task rows),
// but the integration test exists to confirm the SQLite-backed gtd.Store
// returns the same rows / order / Description nullability the handler expects.
func TestDashboard_GetVagueTasks_SQLite_Integration(t *testing.T) {
	store := openMem(t, "")
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

	h := handler.NewDashboardHandler(store, &emptyDecisionStoreSQLite{}, &emptyProposalStoreSQLite{})
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
		t.Errorf("count = %d, want 2", resp.Count)
	}
	if len(resp.SampleIDs) != 2 {
		t.Fatalf("sample_ids len = %d, want 2 (got %v)", len(resp.SampleIDs), resp.SampleIDs)
	}

	gotIDs := map[string]bool{}
	for _, id := range resp.SampleIDs {
		gotIDs[id] = true
	}
	if !gotIDs[vagueA.ID.String()] {
		t.Errorf("missing vagueA %s; got %v", vagueA.ID.String(), resp.SampleIDs)
	}
	if !gotIDs[vagueB.ID.String()] {
		t.Errorf("missing vagueB %s; got %v", vagueB.ID.String(), resp.SampleIDs)
	}
	if gotIDs[healthy.ID.String()] {
		t.Errorf("unexpectedly contains healthy %s", healthy.ID.String())
	}
}
