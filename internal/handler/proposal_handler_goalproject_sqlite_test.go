package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// openGoalProjectSQLiteHandler opens a fresh :memory: SQLite DB (migrations
// applied by wbtsqlite.Open itself — see internal/storage/sqlite/db.go),
// wires the 4 concrete stores wbtsqlite.AcceptDeps needs, and returns a
// ProposalHandler with WithGoalProjectAccept wired to a real
// sqlite.NewAcceptAdapter factory (no mocks — backend-security-design.md
// §6.5's SQLite exception: real file/:memory: DB, not testcontainers).
func openGoalProjectSQLiteHandler(t *testing.T) (*handler.ProposalHandler, *wbtsqlite.ProposalStore, *wbtsqlite.DB) {
	t.Helper()
	ctx := context.Background()
	sdb, err := wbtsqlite.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("wbtsqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })

	propStore := wbtsqlite.NewProposalStore(sdb)
	gtdStore := wbtsqlite.NewGTDStore(sdb)
	learningStore := wbtsqlite.NewLearningStore(sdb)
	decisionStore := wbtsqlite.NewDecisionStore(sdb)

	deps := wbtsqlite.AcceptDeps{
		Proposal: propStore,
		GTD:      gtdStore,
		Learning: learningStore,
		Decision: decisionStore,
	}
	h := handler.NewProposalHandler(propStore, learningStore).
		WithGoalProjectAccept(func(id uuid.UUID) proposal.AcceptAdapter {
			return wbtsqlite.NewAcceptAdapter(id, deps)
		})
	return h, propStore, sdb
}

func TestConfirmProposal_TypeGoal_SQLite_MaterialisesGoalsRow(t *testing.T) {
	h, propStore, sdb := openGoalProjectSQLiteHandler(t)
	ctx := context.Background()

	title := "sqlite-goal-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"title": title, "area": "career"})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeGoal, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+p.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var count int
	if err := sdb.QueryRowContext(ctx, "SELECT count(*) FROM goals WHERE title = ?", title).Scan(&count); err != nil {
		t.Fatalf("query goals: %v", err)
	}
	if count != 1 {
		t.Errorf("goals row count = %d, want 1", count)
	}

	got, err := propStore.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != string(proposal.StatusAccepted) {
		t.Errorf("proposal status = %q, want accepted", got.Status)
	}
}

func TestConfirmProposal_TypeProject_SQLite_MaterialisesProjectsRow(t *testing.T) {
	h, propStore, sdb := openGoalProjectSQLiteHandler(t)
	ctx := context.Background()

	name := "sqlite-project-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"name": name, "title": "Test Project", "area": "projects"})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeProject, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+p.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var count int
	if err := sdb.QueryRowContext(ctx, "SELECT count(*) FROM projects WHERE name = ?", name).Scan(&count); err != nil {
		t.Fatalf("query projects: %v", err)
	}
	if count != 1 {
		t.Errorf("projects row count = %d, want 1", count)
	}
}

func TestConfirmProposal_TypeGoal_SQLite_MalformedPayload(t *testing.T) {
	h, propStore, sdb := openGoalProjectSQLiteHandler(t)
	ctx := context.Background()

	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeGoal, Payload: []byte(`{bad json`)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+p.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var count int
	if err := sdb.QueryRowContext(ctx, "SELECT count(*) FROM goals").Scan(&count); err != nil {
		t.Fatalf("query goals: %v", err)
	}
	if count != 0 {
		t.Errorf("goals row count = %d, want 0 on malformed payload", count)
	}
	got, err := propStore.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != string(proposal.StatusPending) {
		t.Errorf("proposal status = %q, want still pending after 400", got.Status)
	}
}

// TestConfirmProposal_TypeProject_SQLite_PriorityOutOfRange_BadRequest mirrors
// TestConfirmProposal_TypeProject_PG_PriorityOutOfRange_BadRequest (Minor 1,
// round-2 security review) for the SQLite backend.
func TestConfirmProposal_TypeProject_SQLite_PriorityOutOfRange_BadRequest(t *testing.T) {
	h, propStore, _ := openGoalProjectSQLiteHandler(t)
	ctx := context.Background()

	payload, _ := json.Marshal(map[string]any{"name": "sqlite-priority-" + uuid.NewString(), "title": "ok", "priority": 99})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeProject, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+p.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}

	got, err := propStore.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != string(proposal.StatusPending) {
		t.Errorf("proposal status = %q, want still pending after 400", got.Status)
	}
}

// TestConfirmProposal_TypeGoal_SQLite_EmptyTitle_BadRequest mirrors
// TestConfirmProposal_TypeGoal_PG_EmptyTitle_BadRequest (Minor 2, round-2
// security review) for the SQLite backend.
func TestConfirmProposal_TypeGoal_SQLite_EmptyTitle_BadRequest(t *testing.T) {
	h, propStore, _ := openGoalProjectSQLiteHandler(t)
	ctx := context.Background()

	payload, _ := json.Marshal(map[string]any{"title": "", "area": "career"})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeGoal, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+p.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}

	got, err := propStore.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != string(proposal.StatusPending) {
		t.Errorf("proposal status = %q, want still pending after 400", got.Status)
	}
}

// TestConfirmProposal_TypeProject_SQLite_DuplicateName_Conflict mirrors
// TestConfirmProposal_TypeProject_PG_DuplicateName_Conflict (Minor 3, round-2
// security review) for the SQLite backend — internal/storage/sqlite/gtd.go's
// CreateProjectTx also maps its unique-constraint violation to gtd.ErrConflict.
func TestConfirmProposal_TypeProject_SQLite_DuplicateName_Conflict(t *testing.T) {
	h, propStore, _ := openGoalProjectSQLiteHandler(t)
	ctx := context.Background()

	name := "sqlite-dup-project-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"name": name, "title": "First", "area": "projects"})
	first, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeProject, Payload: payload})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+first.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept first: want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	dupPayload, _ := json.Marshal(map[string]any{"name": name, "title": "Second", "area": "projects"})
	second, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeProject, Payload: dupPayload})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}

	rec2 := performRequest(e, http.MethodPost, "/api/proposals/"+second.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("accept duplicate: want 409, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), "already exists") {
		t.Errorf("body = %q, want substring %q", rec2.Body.String(), "already exists")
	}

	got, err := propStore.Get(ctx, second.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != string(proposal.StatusPending) {
		t.Errorf("second proposal status = %q, want still pending after 409", got.Status)
	}
}

// TestConfirmBatch_TypeGoal_SQLite_MaterialisesGoalsRow proves the batch
// accept path (POST /api/proposals/confirm-batch) routes TypeGoal through the
// same h.goalProjectAdapter → proposal.AcceptOrchestration seam the singular
// path uses on SQLite too — mirrors
// TestConfirmBatch_TypeGoal_PG_MaterialisesGoalsRow (proposal_handler_goalproject_pg_test.go,
// same package, shared batchResultEntry helpers).
func TestConfirmBatch_TypeGoal_SQLite_MaterialisesGoalsRow(t *testing.T) {
	h, propStore, sdb := openGoalProjectSQLiteHandler(t)
	ctx := context.Background()

	title := "sqlite-batch-goal-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"title": title, "area": "career"})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeGoal, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := newEcho()
	e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
	body := `{"ids":["` + p.ID.String() + `"],"action":"accept"}`
	rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	assertBatchResultOK(t, rec.Body.Bytes(), p.ID.String())

	var count int
	if err := sdb.QueryRowContext(ctx, "SELECT count(*) FROM goals WHERE title = ?", title).Scan(&count); err != nil {
		t.Fatalf("query goals: %v", err)
	}
	if count != 1 {
		t.Errorf("goals row count = %d, want 1", count)
	}
}

// TestConfirmBatch_TypeProject_SQLite_MaterialisesProjectsRow mirrors
// TestConfirmBatch_TypeGoal_SQLite_MaterialisesGoalsRow for TypeProject.
func TestConfirmBatch_TypeProject_SQLite_MaterialisesProjectsRow(t *testing.T) {
	h, propStore, sdb := openGoalProjectSQLiteHandler(t)
	ctx := context.Background()

	name := "sqlite-batch-project-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"name": name, "title": "Test Project", "area": "projects"})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeProject, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := newEcho()
	e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
	body := `{"ids":["` + p.ID.String() + `"],"action":"accept"}`
	rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	assertBatchResultOK(t, rec.Body.Bytes(), p.ID.String())

	var count int
	if err := sdb.QueryRowContext(ctx, "SELECT count(*) FROM projects WHERE name = ?", name).Scan(&count); err != nil {
		t.Fatalf("query projects: %v", err)
	}
	if count != 1 {
		t.Errorf("projects row count = %d, want 1", count)
	}
}

// TestConfirmBatch_TypeGoal_SQLite_MalformedPayload proves a malformed goal
// payload surfaces as a per-row batch error and leaves the proposal pending
// on SQLite — mirrors TestConfirmBatch_TypeGoal_PG_MalformedPayload.
func TestConfirmBatch_TypeGoal_SQLite_MalformedPayload(t *testing.T) {
	h, propStore, sdb := openGoalProjectSQLiteHandler(t)
	ctx := context.Background()

	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeGoal, Payload: []byte(`{bad json`)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := newEcho()
	e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
	body := `{"ids":["` + p.ID.String() + `"],"action":"accept"}`
	rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 (batch endpoint always 200; per-row error), got %d: %s", rec.Code, rec.Body.String())
	}
	entry := decodeBatchResultEntry(t, rec.Body.Bytes(), p.ID.String())
	if entry.OK {
		t.Errorf("entry.OK = true, want false on malformed payload")
	}
	if !strings.Contains(entry.Error, "malformed") {
		t.Errorf("entry.Error = %q, want substring %q", entry.Error, "malformed")
	}

	var count int
	if err := sdb.QueryRowContext(ctx, "SELECT count(*) FROM goals").Scan(&count); err != nil {
		t.Fatalf("query goals: %v", err)
	}
	if count != 0 {
		t.Errorf("goals row count = %d, want 0 on malformed payload", count)
	}
	got, err := propStore.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != string(proposal.StatusPending) {
		t.Errorf("proposal status = %q, want still pending after malformed batch accept", got.Status)
	}
}

// TestConfirmProposal_TypePlaybook_SQLite_ResolveOnly proves playbook accept
// never routes through h.goalProjectAdapter: the SQLite AcceptAdapter's
// Materialize returns an "A1-seam TODO" error for TypePlaybook (see
// internal/storage/sqlite/accept_proposal.go), so any regression that widens
// the handleAccept switch to include playbook would surface as a 500 here.
func TestConfirmProposal_TypePlaybook_SQLite_ResolveOnly(t *testing.T) {
	h, propStore, _ := openGoalProjectSQLiteHandler(t)
	ctx := context.Background()

	p, err := propStore.Create(ctx, proposal.CreateParams{
		Type:    proposal.TypePlaybook,
		Payload: []byte(`{"title":"pb"}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+p.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 (resolve-only), got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "A1-seam") {
		t.Errorf("playbook accept must not hit the goal/project adapter; body=%s", rec.Body.String())
	}
	got, err := propStore.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != string(proposal.StatusAccepted) {
		t.Errorf("proposal status = %q, want accepted", got.Status)
	}
}
