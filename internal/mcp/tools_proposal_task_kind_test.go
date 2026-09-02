package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// GTD f457740e / [F0902-54]: decodeTaskProposalParams previously coerced an
// invalid suggested_kind to "general" and silently discarded the "invalid"
// signal — this file verifies the shared helper now surfaces a warning
// (validator.ResolveTaskKind) that all three MCP backends (materializeTaskPg,
// materializeTaskIface, materializeTaskSQLite's pre-commit gate) inherit for
// free since they all call this one function.
// ---------------------------------------------------------------------------

const invalidKindTestDescription = "Fix kind coercion at internal/handler/proposal_handler.go:240"

// wantKindGeneral names the fallback kind ResolveTaskKind coerces an
// invalid/empty suggested_kind to; named to avoid a goconst duplicate-string
// finding across the tests below.
const wantKindGeneral = "general"

// TestDecodeTaskProposalParams_InvalidKind is a same-package unexported unit
// test — dispositive for all 3 MCP backends per spec f457740e's Current
// behaviour §3, since materializeTaskPg / materializeTaskIface /
// materializeTaskSQLite's pre-commit gate all call this one helper.
func TestDecodeTaskProposalParams_InvalidKind(t *testing.T) {
	t.Run("non-strict bogus kind → general + warning", func(t *testing.T) {
		payload := mustMarshal(t, proposal.TaskPayload{
			Title:         "Bogus kind task",
			Description:   invalidKindTestDescription,
			SuggestedKind: "bogus",
			SourceTool:    "test",
		})
		params, warnings, errMsg := decodeTaskProposalParams(payload, false)
		if errMsg != "" {
			t.Fatalf("errMsg = %q, want empty (warn mode must not fail)", errMsg)
		}
		if params.Kind != wantKindGeneral {
			t.Errorf("params.Kind = %q, want %q", params.Kind, wantKindGeneral)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "bogus") {
			t.Errorf("warnings = %v, want exactly 1 warning mentioning %q", warnings, "bogus")
		}
	})

	t.Run("empty kind → general, no warning", func(t *testing.T) {
		payload := mustMarshal(t, proposal.TaskPayload{
			Title:       "Empty kind task",
			Description: invalidKindTestDescription,
			SourceTool:  "test",
		})
		params, warnings, errMsg := decodeTaskProposalParams(payload, false)
		if errMsg != "" {
			t.Fatalf("errMsg = %q, want empty", errMsg)
		}
		if params.Kind != wantKindGeneral {
			t.Errorf("params.Kind = %q, want %q", params.Kind, wantKindGeneral)
		}
		if len(warnings) != 0 {
			t.Errorf("warnings = %v, want empty (empty kind stays silent)", warnings)
		}
	})

	t.Run("strict bogus kind → errMsg contains bogus", func(t *testing.T) {
		payload := mustMarshal(t, proposal.TaskPayload{
			Title:         "Bogus kind task",
			Description:   invalidKindTestDescription,
			SuggestedKind: "bogus",
			SourceTool:    "test",
		})
		params, warnings, errMsg := decodeTaskProposalParams(payload, true)
		if errMsg == "" {
			t.Fatal("errMsg = empty, want non-empty (strict mode must reject)")
		}
		if params != (gtd.CreateTaskParams{}) {
			t.Errorf("params = %+v, want zero value on strict-mode rejection", params)
		}
		if !strings.Contains(errMsg, "vagueness check failed") {
			t.Errorf("errMsg = %q, want it to contain %q", errMsg, "vagueness check failed")
		}
		if !strings.Contains(errMsg, "bogus") {
			t.Errorf("errMsg = %q, want it to mention %q", errMsg, "bogus")
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "bogus") {
			t.Errorf("warnings = %v, want exactly 1 warning mentioning %q", warnings, "bogus")
		}
	})
}

// createTaskProposal inserts a TypeTask pending_proposals row and returns its
// UUID string. Mirrors createKnowledgeProposal / createDecisionProposal.
func createTaskProposal(t *testing.T, s *Server, title, description, suggestedKind string) string {
	t.Helper()
	payload := mustMarshal(t, proposal.TaskPayload{
		Title:         title,
		Description:   description,
		SuggestedKind: suggestedKind,
		SourceTool:    "test",
	})
	row, err := s.proposal.Create(context.Background(), proposal.CreateParams{
		Type:    proposal.TypeTask,
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("create task proposal: %v", err)
	}
	return row.ID.String()
}

// TestHandleConfirmProposal_TypeTask_InvalidKind_WarnMode covers the
// SQLite-backed confirm_proposal path (acceptProposalSQLite ->
// runSQLitePostCommitMaterialisers -> materializeTaskIface ->
// decodeTaskProposalParams): a bogus suggested_kind in warn mode still
// succeeds, materialises the task with kind=general, and the response's
// {"task":..., "warnings":[...]} shape (neutralizeCreatedEntity's existing
// special case) now carries the kind message.
func TestHandleConfirmProposal_TypeTask_InvalidKind_WarnMode(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", "")
	s := newProposalTestServer(t)
	ctx := context.Background()

	propID := createTaskProposal(t, s, "Bogus kind MCP task", invalidKindTestDescription, "bogus")

	result := callConfirmProposal(t, s, propID, "accept")
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(result))
	}
	body := resultText(result)
	if !strings.Contains(body, "bogus") {
		t.Errorf("response missing kind warning mentioning %q: %s", "bogus", body)
	}
	if !strings.Contains(body, `"general"`) {
		t.Errorf("response missing kind=general fallback: %s", body)
	}

	pp, err := s.proposal.Get(ctx, parseTestUUID(t, propID))
	if err != nil {
		t.Fatalf("proposal.Get post-accept: %v", err)
	}
	if pp.Status != string(proposal.StatusAccepted) {
		t.Errorf("proposal.Status = %q, want accepted", pp.Status)
	}
}

// TestHandleConfirmProposal_TypeTask_InvalidKind_StrictMode covers the same
// SQLite path under strict mode: materializeTaskSQLite's pre-commit gate
// (tools_proposal.go:820-824, NOT modified by this task) rejects inside the
// open tx via decodeTaskProposalParams's strict branch, so the tx never
// commits — the proposal stays pending and no task row is created.
func TestHandleConfirmProposal_TypeTask_InvalidKind_StrictMode(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", "true")
	s := newProposalTestServer(t)
	ctx := context.Background()

	propID := createTaskProposal(t, s, "Bogus kind MCP task", invalidKindTestDescription, "bogus")

	result := callConfirmProposal(t, s, propID, "accept")
	if !result.IsError {
		t.Fatalf("expected tool error in strict mode, got success: %s", resultText(result))
	}
	body := resultText(result)
	if !strings.Contains(body, "bogus") {
		t.Errorf("error text missing kind message mentioning %q: %s", "bogus", body)
	}

	pp, err := s.proposal.Get(ctx, parseTestUUID(t, propID))
	if err != nil {
		t.Fatalf("proposal.Get: %v", err)
	}
	if pp.Status != string(proposal.StatusPending) {
		t.Errorf("proposal.Status = %q, want still pending after strict-mode rejection", pp.Status)
	}
}

// TestHandleConfirmProposal_TypeTask_InvalidKind_Pg_WarnMode covers the
// Postgres accept path (acceptProposalPg -> materializeFromPayloadPg ->
// materializeTaskPg -> decodeTaskProposalParams), the one accept path the
// SQLite-backed newProposalTestServer cannot reach. Reuses mcpPlanTestPgPool
// rather than starting a second Postgres container — same pattern as
// TestHandleConfirmProposal_AcceptPg_NeutralizeForgedMarkerInPayloadAndCreatedGoal
// (u13_phase_b_b2_test.go). decodeTaskProposalParams is a single
// backend-agnostic helper (Current behaviour §3), so this is additional
// belt-and-braces coverage beyond spec's "no PG-specific test needed"
// disclaimer, closing the sprint dispatch's explicit PG-response ask.
func TestHandleConfirmProposal_TypeTask_InvalidKind_Pg_WarnMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	t.Setenv("WBT_STRICT_VAGUENESS", "")
	wsID := uuid.New()
	s := &Server{
		pool:       mcpPlanTestPgPool,
		proposal:   proposal.NewStore(mcpPlanTestPgPool, &wsID),
		pgProposal: proposal.NewStore(mcpPlanTestPgPool, &wsID),
		pgGTD:      gtd.NewStore(mcpPlanTestPgPool, &wsID),
	}
	ctx := context.Background()

	payloadBytes := mustMarshal(t, proposal.TaskPayload{
		Title:         "Bogus kind PG task",
		Description:   invalidKindTestDescription,
		SuggestedKind: "bogus",
		SourceTool:    "test",
	})
	row, err := s.proposal.Create(ctx, proposal.CreateParams{Type: proposal.TypeTask, Payload: payloadBytes})
	if err != nil {
		t.Fatalf("seeding PG TypeTask proposal: %v", err)
	}

	acceptReq := mcpmsg.CallToolRequest{}
	acceptReq.Params.Arguments = map[string]any{"proposal_id": row.ID.String(), "action": "accept"}
	acceptResult, err := s.handleConfirmProposal(ctx, acceptReq)
	if err != nil {
		t.Fatalf("handleConfirmProposal (accept, PG): %v", err)
	}
	if acceptResult.IsError {
		t.Fatalf("confirm_proposal accept (PG) returned tool error: %s", resultText(acceptResult))
	}
	got := resultText(acceptResult)
	if !strings.Contains(got, "bogus") {
		t.Errorf("PG response missing kind warning mentioning %q: %s", "bogus", got)
	}
	if !strings.Contains(got, `"general"`) {
		t.Errorf("PG response missing kind=general fallback: %s", got)
	}
}
