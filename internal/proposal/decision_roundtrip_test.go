package proposal_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// roundTripTitle is the canonical decision title each subtest uses to
// assert the materialised row matches the proposer payload.
const roundTripTitle = "Adopt HTTP/2 server-push for project pages"

func newDecisionPayload(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(proposal.DecisionProposerPayload{
		Title:        roundTripTitle,
		Decision:     "Use SSE-style streaming via Echo's c.Response().Flush()",
		Rationale:    "Avoids websocket complexity; covers our 99% read pattern.",
		Alternatives: []string{"raw WebSocket", "long-poll", "GraphQL subscriptions"},
		SessionID:    "round-trip-test-session",
		TriggerTool:  "complete_task",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return body
}

// ---------------------------------------------------------------------------
// SQLite path — exercises wbtsqlite.DecisionStore.LogTx end-to-end inside a
// transaction, mirroring the MCP confirm_proposal accept flow.
// ---------------------------------------------------------------------------

// TestProposal_Accept_TypeDecision_Roundtrip_SQLite verifies the SQLite Tx
// path: a pending_proposals row of type='decision' is materialised inside a
// single *sql.Tx, the proposal is marked accepted in the same tx, both rows
// commit atomically. Reads back the decision via DecisionStore.All to
// confirm round-trip integrity.
func TestProposal_Accept_TypeDecision_Roundtrip_SQLite(t *testing.T) {
	ctx := context.Background()
	sdb, err := wbtsqlite.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })

	propStore := wbtsqlite.NewProposalStore(sdb)
	decStore := wbtsqlite.NewDecisionStore(sdb)

	// 1. Insert a pending TypeDecision row.
	prop, err := propStore.Create(ctx, proposal.CreateParams{
		Type:       proposal.TypeDecision,
		Payload:    newDecisionPayload(t),
		ProposedBy: "test",
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}

	// 2. Begin tx, insert decision, resolve proposal, commit — mirrors
	//    Server.acceptProposalSQLite + materializeFromPayloadSQLiteTx.
	tx, err := sdb.BeginTx(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })

	var dp proposal.DecisionProposerPayload
	if err := json.Unmarshal(prop.Payload, &dp); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	logParams := decision.LogParams{
		Title:     dp.Title,
		Context:   "auto-proposed by trigger_tool=" + dp.TriggerTool + " session=" + dp.SessionID,
		Decision:  dp.Decision,
		Rationale: dp.Rationale,
		Source:    decision.SourceAuto,
	}
	if _, err := decStore.LogTx(ctx, tx, logParams); err != nil {
		t.Fatalf("LogTx: %v", err)
	}
	if err := propStore.ResolveTx(ctx, tx, prop.ID, proposal.StatusAccepted); err != nil {
		t.Fatalf("ResolveTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// 3. Verify decision was written and matches proposer payload.
	rows, err := decStore.All(ctx, 10)
	if err != nil {
		t.Fatalf("decisions.All: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("decisions count = %d, want 1", len(rows))
	}
	if rows[0].Title != roundTripTitle {
		t.Errorf("title = %q, want %q", rows[0].Title, roundTripTitle)
	}
	if !strings.Contains(rows[0].Decision, "SSE-style streaming") {
		t.Errorf("decision body unexpected: %q", rows[0].Decision)
	}

	// 4. Verify proposal is accepted (single source of truth).
	pp, err := propStore.Get(ctx, prop.ID)
	if err != nil {
		t.Fatalf("proposal Get post-commit: %v", err)
	}
	if pp.Status != string(proposal.StatusAccepted) {
		t.Errorf("proposal status = %q, want accepted", pp.Status)
	}
}

// TestProposal_Accept_TypeDecision_Roundtrip_PG runs the same materialise +
// resolve sequence against a real Postgres container so the new
// pending_proposals_type_check (000038) is verified against the same engine
// production uses. Reuses openBatchTestPgPool from
// batch_confirm_postgres_test.go (same package; same skipMigrations map).
//
// When SQLite + PG
// integration tests both exist for the same flow, BOTH must run — testcontainers
// PG covers dialect-specific behaviour (CHECK constraint, transaction
// isolation, ROW count) that the SQLite path can't.
func TestProposal_Accept_TypeDecision_Roundtrip_PG(t *testing.T) {
	pool := openBatchTestPgPool(t)
	ctx := context.Background()
	store := proposal.NewStore(pool, nil)

	// 1. Insert a TypeDecision pending row through the public API. This
	//    exercises the new CHECK constraint allowlist.
	created, err := store.Create(ctx, proposal.CreateParams{
		Type:    proposal.TypeDecision,
		Payload: newDecisionPayload(t),
	})
	if err != nil {
		t.Fatalf("Create TypeDecision: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", created.ID)
	})

	// 2. Read the row back as the materialiser would and decode the payload.
	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Type != string(proposal.TypeDecision) {
		t.Errorf("type = %q, want decision", got.Type)
	}
	var dp proposal.DecisionProposerPayload
	if err := json.Unmarshal(got.Payload, &dp); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if dp.Title != roundTripTitle {
		t.Errorf("payload title = %q, want %q", dp.Title, roundTripTitle)
	}

	// 3. Run the full pg-tx materialise: BEGIN → INSERT decisions → UPDATE
	//    pending_proposals → COMMIT. This mirrors Server.acceptProposalPg.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after commit

	// Insert decision row directly (the test purposely bypasses
	// internal/decision/Store to keep this proposal-package-only and avoid
	// the embedding/cosine paths). Workspace_id NULL = unscoped, matches
	// the proposal row's workspace.
	decID := uuid.New()
	_, err = tx.Exec(
		ctx, `INSERT INTO decisions (id, title, context, decision, rationale)
		VALUES ($1, $2, $3, $4, $5)`,
		decID, dp.Title,
		"auto-proposed by trigger_tool="+dp.TriggerTool+" session="+dp.SessionID,
		dp.Decision, dp.Rationale,
	)
	if err != nil {
		t.Fatalf("INSERT decisions: %v", err)
	}
	// Resolve via the tx-scoped store so the WHERE status='pending' guard
	// runs inside the same tx as the decision insert.
	if _, err := store.WithTx(tx).Resolve(ctx, created.ID, proposal.StatusAccepted); err != nil {
		t.Fatalf("ResolveTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// 4. Verify post-commit state: proposal accepted, decision visible.
	post, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get post-commit: %v", err)
	}
	if post.Status != string(proposal.StatusAccepted) {
		t.Errorf("status = %q, want accepted", post.Status)
	}

	var (
		gotTitle    string
		gotDecision string
	)
	row := pool.QueryRow(ctx, "SELECT title, decision FROM decisions WHERE id = $1", decID)
	if err := row.Scan(&gotTitle, &gotDecision); err != nil {
		t.Fatalf("SELECT decisions: %v", err)
	}
	if gotTitle != roundTripTitle {
		t.Errorf("decisions.title = %q, want %q", gotTitle, roundTripTitle)
	}
	if !strings.Contains(gotDecision, "SSE-style streaming") {
		t.Errorf("decisions.decision = %q, want contains 'SSE-style streaming'", gotDecision)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM decisions WHERE id = $1", decID)
	})
}

// TestProposal_Accept_TypeDecision_Roundtrip_SQLite_RollbackOnResolveFail
// simulates the failure branch: Resolve fails (e.g. concurrent accept
// already flipped the row), the entire transaction rolls back, and the
// decision row MUST NOT exist after rollback. This is the core safety
// property of the Tx path — better an orphan rollback than a half-state.
func TestProposal_Accept_TypeDecision_Roundtrip_SQLite_RollbackOnResolveFail(t *testing.T) {
	ctx := context.Background()
	sdb, err := wbtsqlite.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })

	propStore := wbtsqlite.NewProposalStore(sdb)
	decStore := wbtsqlite.NewDecisionStore(sdb)

	prop, err := propStore.Create(ctx, proposal.CreateParams{
		Type:    proposal.TypeDecision,
		Payload: newDecisionPayload(t),
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	// Pre-resolve the proposal so the in-tx ResolveTx sees a stale state
	// and returns ErrNotFound — simulating a concurrent accept.
	if _, err := propStore.Resolve(ctx, prop.ID, proposal.StatusAccepted); err != nil {
		t.Fatalf("pre-resolve: %v", err)
	}

	tx, err := sdb.BeginTx(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}

	if _, err := decStore.LogTx(ctx, tx, decision.LogParams{
		Title: roundTripTitle, Decision: "x", Rationale: "y", Source: decision.SourceAuto,
	}); err != nil {
		t.Fatalf("LogTx: %v", err)
	}
	resolveErr := propStore.ResolveTx(ctx, tx, prop.ID, proposal.StatusAccepted)
	if resolveErr == nil {
		t.Fatal("ResolveTx should fail on already-resolved row")
	}
	// Roll back; the decision MUST disappear.
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	rows, err := decStore.All(ctx, 10)
	if err != nil {
		t.Fatalf("decisions.All post-rollback: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 decisions after rollback, got %d", len(rows))
	}
}
