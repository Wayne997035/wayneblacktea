package handler_test

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// goalProjectSkipMigrations mirrors internal/gtd/pg_test_main_test.go's
// skipMigrations / internal/proposal/batch_confirm_postgres_test.go's
// batchSkipMigrations: .up.sql files with psql metacommands pgx cannot parse.
var goalProjectSkipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true, // psql `\set` metacommand
}

var goalProjectPgPool *pgxpool.Pool

// TestMain spins up a single pgvector/pgvector:pg16 testcontainer shared by
// every PG integration test in this package (internal/handler currently has
// no other TestMain). Skipped entirely under `go test -short` (no Docker) —
// mirrors internal/proposal/batch_confirm_postgres_test.go's run().
func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(runGoalProjectAcceptPGSuite(m))
}

func runGoalProjectAcceptPGSuite(m *testing.M) int {
	if testing.Short() {
		return m.Run()
	}
	ctx := context.Background()
	c, err := tcpostgres.Run(
		ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_handler_goalproject_test"),
		tcpostgres.WithUsername("wbt"),
		tcpostgres.WithPassword("wbt"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Fatalf("start postgres container: %v", err)
	}
	defer func() { _ = c.Terminate(ctx) }()

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Printf("get connection string: %v", err)
		return 1
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Printf("pgxpool.New: %v", err)
		return 1
	}
	defer pool.Close()

	applyGoalProjectUpMigrationsOnce(ctx, pool)

	goalProjectPgPool = pool
	return m.Run()
}

func applyGoalProjectUpMigrationsOnce(ctx context.Context, pool *pgxpool.Pool) {
	entries, err := migrationfs.FS.ReadDir(".")
	if err != nil {
		log.Fatalf("read embedded migrations dir: %v", err)
	}
	var ups []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		ups = append(ups, name)
	}
	sort.Strings(ups)

	for _, name := range ups {
		if goalProjectSkipMigrations[name] {
			log.Printf("applyGoalProjectUpMigrations: skipping %s (psql-metacommand-only file)", name)
			continue
		}
		body, err := migrationfs.FS.ReadFile(name)
		if err != nil {
			log.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			log.Fatalf("apply %s: %v", name, err)
		}
	}
}

// openGoalProjectTestPgPool returns the package-level singleton pool
// initialised in TestMain. Skip with -short: requires Docker and adds ~5-10s.
func openGoalProjectTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return goalProjectPgPool
}

// newGoalProjectPGHandler wires a ProposalHandler with WithGoalProjectAccept
// pointed at a real proposal.NewPgAcceptAdapter factory over the shared
// testcontainers pool (no mocks — backend-security-design.md §6.5).
func newGoalProjectPGHandler(pool *pgxpool.Pool) (*handler.ProposalHandler, *proposal.Store) {
	propStore := proposal.NewStore(pool, nil)
	gtdStore := gtd.NewStore(pool, nil)
	learningStore := learning.NewStore(pool, nil)
	decisionStore := decision.NewStore(pool, nil)

	deps := proposal.PgAcceptDeps{
		Pool:     pool,
		Proposal: propStore,
		GTD:      gtdStore,
		Learning: learningStore,
		Decision: decisionStore,
	}
	h := handler.NewProposalHandler(propStore, learningStore).
		WithGoalProjectAccept(func(id uuid.UUID) proposal.AcceptAdapter {
			return proposal.NewPgAcceptAdapter(id, deps)
		})
	return h, propStore
}

func TestConfirmProposal_TypeGoal_PG_MaterialisesGoalsRow(t *testing.T) {
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()
	h, propStore := newGoalProjectPGHandler(pool)

	title := "pg-goal-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"title": title, "area": "career"})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeGoal, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID) })

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+p.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM goals WHERE title = $1", title).Scan(&count); err != nil {
		t.Fatalf("query goals: %v", err)
	}
	if count != 1 {
		t.Errorf("goals row count = %d, want 1", count)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM goals WHERE title = $1", title) })

	got, err := propStore.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != string(proposal.StatusAccepted) {
		t.Errorf("proposal status = %q, want accepted", got.Status)
	}
}

func TestConfirmProposal_TypeProject_PG_MaterialisesProjectsRow(t *testing.T) {
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()
	h, propStore := newGoalProjectPGHandler(pool)

	name := "pg-project-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"name": name, "title": "Test Project", "area": "projects"})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeProject, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID) })

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+p.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM projects WHERE name = $1", name).Scan(&count); err != nil {
		t.Fatalf("query projects: %v", err)
	}
	if count != 1 {
		t.Errorf("projects row count = %d, want 1", count)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM projects WHERE name = $1", name) })
}

func TestConfirmProposal_TypeProject_PG_MalformedPayload(t *testing.T) {
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()
	h, propStore := newGoalProjectPGHandler(pool)

	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeProject, Payload: []byte(`{"goal_id":"not-a-uuid"}`)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID) })

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

// TestConfirmProposal_TypeProject_PG_PriorityOutOfRange_BadRequest proves
// Minor 1 (round-2 security review): projects.priority has a CHECK (priority
// BETWEEN 1 AND 5); an out-of-range value must be rejected by
// validateGoalProjectPayload's decode-before-tx check (400), not reach
// Postgres and raise pg 23514 inside AcceptOrchestration's transaction (500).
func TestConfirmProposal_TypeProject_PG_PriorityOutOfRange_BadRequest(t *testing.T) {
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()
	h, propStore := newGoalProjectPGHandler(pool)

	payload, _ := json.Marshal(map[string]any{"name": "pg-priority-" + uuid.NewString(), "title": "ok", "priority": 99})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeProject, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID) })

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

// TestConfirmProposal_TypeGoal_PG_EmptyTitle_BadRequest proves Minor 2
// (round-2 security review): goals.title is NOT NULL but accepts ”, so an
// empty title must be rejected before the transaction (400), matching
// DecodeDecisionParams/DecodeTaskParams's existing missing-title behaviour.
func TestConfirmProposal_TypeGoal_PG_EmptyTitle_BadRequest(t *testing.T) {
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()
	h, propStore := newGoalProjectPGHandler(pool)

	payload, _ := json.Marshal(map[string]any{"title": "", "area": "career"})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeGoal, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID) })

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

// TestConfirmProposal_TypeProject_PG_DuplicateName_Conflict proves Minor 3
// (round-2 security review): projects.name is UNIQUE and CreateProject maps
// pg 23505 to gtd.ErrConflict (internal/gtd/store.go); acceptGoalOrProject
// must map that through to 409, not let it fall into the generic 500 branch.
func TestConfirmProposal_TypeProject_PG_DuplicateName_Conflict(t *testing.T) {
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()
	h, propStore := newGoalProjectPGHandler(pool)

	name := "pg-dup-project-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"name": name, "title": "First", "area": "projects"})
	first, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeProject, Payload: payload})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", first.ID) })

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+first.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("accept first: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM projects WHERE name = $1", name) })

	dupPayload, _ := json.Marshal(map[string]any{"name": name, "title": "Second", "area": "projects"})
	second, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeProject, Payload: dupPayload})
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", second.ID) })

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

// TestConfirmBatch_TypeGoal_PG_MaterialisesGoalsRow proves the batch accept
// path (POST /api/proposals/confirm-batch) routes TypeGoal through the same
// h.goalProjectAdapter → proposal.AcceptOrchestration seam the singular path
// uses — closing the gap where batch accept silently dropped goal/project
// payloads (batchProposalMeta's default case).
func TestConfirmBatch_TypeGoal_PG_MaterialisesGoalsRow(t *testing.T) {
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()
	h, propStore := newGoalProjectPGHandler(pool)

	title := "pg-batch-goal-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"title": title, "area": "career"})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeGoal, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID) })

	e := newEcho()
	e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
	body := `{"ids":["` + p.ID.String() + `"],"action":"accept"}`
	rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	assertBatchResultOK(t, rec.Body.Bytes(), p.ID.String())

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM goals WHERE title = $1", title).Scan(&count); err != nil {
		t.Fatalf("query goals: %v", err)
	}
	if count != 1 {
		t.Errorf("goals row count = %d, want 1", count)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM goals WHERE title = $1", title) })
}

// TestConfirmBatch_TypeProject_PG_MaterialisesProjectsRow mirrors
// TestConfirmBatch_TypeGoal_PG_MaterialisesGoalsRow for TypeProject.
func TestConfirmBatch_TypeProject_PG_MaterialisesProjectsRow(t *testing.T) {
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()
	h, propStore := newGoalProjectPGHandler(pool)

	name := "pg-batch-project-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"name": name, "title": "Test Project", "area": "projects"})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeProject, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID) })

	e := newEcho()
	e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
	body := `{"ids":["` + p.ID.String() + `"],"action":"accept"}`
	rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	assertBatchResultOK(t, rec.Body.Bytes(), p.ID.String())

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM projects WHERE name = $1", name).Scan(&count); err != nil {
		t.Fatalf("query projects: %v", err)
	}
	if count != 1 {
		t.Errorf("projects row count = %d, want 1", count)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM projects WHERE name = $1", name) })
}

// TestConfirmBatch_TypeGoal_PG_MalformedPayload proves a malformed goal
// payload surfaces as a per-row batch error (not a silent drop or a 500) and
// leaves the proposal pending — mirrors
// TestConfirmProposal_TypeProject_PG_MalformedPayload's singular-path
// assertion for the batch endpoint. Uses an invalid due_date (syntactically
// valid JSON, semantically invalid) rather than broken JSON syntax: the
// pending_proposals.payload column is `jsonb`, so Postgres itself rejects
// non-JSON bytes at INSERT time before the handler ever sees them (same
// constraint TestConfirmProposal_TypeProject_PG_MalformedPayload works
// around with "goal_id":"not-a-uuid").
func TestConfirmBatch_TypeGoal_PG_MalformedPayload(t *testing.T) {
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()
	h, propStore := newGoalProjectPGHandler(pool)

	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeGoal, Payload: []byte(`{"title":"ok","due_date":"not-a-date"}`)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID) })

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

	got, err := propStore.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != string(proposal.StatusPending) {
		t.Errorf("proposal status = %q, want still pending after malformed batch accept", got.Status)
	}
}

// batchResultEntry mirrors handler.batchConfirmResultEntry's JSON shape —
// duplicated here (package handler_test, unexported type) rather than
// exported from the handler package purely for test use.
type batchResultEntry struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped"`
	Error   string `json:"error"`
}

// decodeBatchResultEntry decodes a ConfirmBatch response body and returns the
// single result entry matching wantID.
func decodeBatchResultEntry(t *testing.T, body []byte, wantID string) batchResultEntry {
	t.Helper()
	var resp struct {
		Results []batchResultEntry `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response not valid JSON: %v (body: %s)", err, body)
	}
	for _, r := range resp.Results {
		if r.ID == wantID {
			return r
		}
	}
	t.Fatalf("no result entry for id %s in response: %s", wantID, body)
	return batchResultEntry{}
}

// assertBatchResultOK fails the test unless the result entry for wantID has
// OK=true and no Error.
func assertBatchResultOK(t *testing.T, body []byte, wantID string) {
	t.Helper()
	entry := decodeBatchResultEntry(t, body, wantID)
	if !entry.OK {
		t.Errorf("entry.OK = false, want true (error=%q)", entry.Error)
	}
	if entry.Error != "" {
		t.Errorf("unexpected entry.Error = %q", entry.Error)
	}
}

// TestConfirmProposal_TypePlaybook_PG_ResolveOnly proves playbook accept
// never routes through h.goalProjectAdapter on the PG backend either: the PG
// AcceptAdapter's Materialize returns an "A1-seam TODO" error for
// TypePlaybook (accept_pg.go), so a regression here would surface as 500.
func TestConfirmProposal_TypePlaybook_PG_ResolveOnly(t *testing.T) {
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()
	h, propStore := newGoalProjectPGHandler(pool)

	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypePlaybook, Payload: []byte(`{"title":"pb"}`)})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID) })

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+p.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 (resolve-only), got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "A1-seam") {
		t.Errorf("playbook accept must not hit the goal/project adapter; body=%s", rec.Body.String())
	}
}
