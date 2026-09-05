package mcp

import (
	"context"
	"encoding/json"
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

// confirmProposalTaskCreated decodes confirm_proposal's response body for the
// TypeTask warn-mode shape (confirmResult.Created = {"task":{...},"warnings":
// [...]}), anchoring assertions to the structured warnings field instead of
// the raw response text. [F173-05] A raw-text substring check on "bogus" can
// be satisfied by an unrelated echo — confirmResult.Proposal carries the
// original pending_proposals payload (including suggested_kind="bogus")
// verbatim — even when ResolveTaskKind emits no warning at all, so it never
// actually pinned down the mechanism it claimed to guard (r2 spec-verifier
// trc-1).
type confirmProposalTaskCreated struct {
	Created struct {
		Task struct {
			Kind string `json:"kind"`
		} `json:"task"`
		Warnings []string `json:"warnings"`
	} `json:"created"`
}

func decodeConfirmProposalTaskCreated(t *testing.T, body string) confirmProposalTaskCreated {
	t.Helper()
	var decoded confirmProposalTaskCreated
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode confirm_proposal response JSON: %v; body: %s", err, body)
	}
	return decoded
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
	// [F173-05] Structural assertion on created.{task.kind,warnings} — see
	// confirmProposalTaskCreated's doc comment for why the raw-body
	// substring check this replaces was a false guard.
	body := resultText(result)
	decoded := decodeConfirmProposalTaskCreated(t, body)
	if decoded.Created.Task.Kind != wantKindGeneral {
		t.Errorf("created.task.kind = %q, want %q", decoded.Created.Task.Kind, wantKindGeneral)
	}
	if len(decoded.Created.Warnings) != 1 || !strings.Contains(decoded.Created.Warnings[0], "bogus") {
		t.Errorf("created.warnings = %v, want exactly 1 warning mentioning %q", decoded.Created.Warnings, "bogus")
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
	// [F173-05] Same structural assertion as the SQLite WarnMode test above —
	// see confirmProposalTaskCreated's doc comment for why the raw-body
	// substring check this replaces was a false guard.
	got := resultText(acceptResult)
	decoded := decodeConfirmProposalTaskCreated(t, got)
	if decoded.Created.Task.Kind != wantKindGeneral {
		t.Errorf("created.task.kind = %q, want %q", decoded.Created.Task.Kind, wantKindGeneral)
	}
	if len(decoded.Created.Warnings) != 1 || !strings.Contains(decoded.Created.Warnings[0], "bogus") {
		t.Errorf("created.warnings = %v, want exactly 1 warning mentioning %q", decoded.Created.Warnings, "bogus")
	}
}

// markerBearingSuggestedKind is the shared input for the two tests below:
// a suggested_kind that embeds a boundary marker (storedContextMarkerEnd,
// boundary_markers.go) while staying well under maxKindWarningRunes (80,
// task_kind.go:33) so ResolveTaskKind's truncation never fires and the
// marker survives into the warning text intact. storedContextMarkerEnd is
// 26 runes and this whole value is 31 — deliberately NOT storedDataNotice
// (218 runes), which would be truncated away before reaching the warning,
// making "warnings has no marker text" trivially true even with zero
// sanitisation (r2 spec-verifier finding).
const markerBearingSuggestedKind = "bogus" + storedContextMarkerEnd

// TestHandleConfirmProposal_TypeTask_InvalidKind_WarnMode_SanitisesBoundaryMarker
// pins F173-04: neutralizeCreatedEntity's map[string]any branch
// (tools_proposal.go) must strip a boundary marker embedded in
// suggested_kind out of the warn-channel response before it reaches
// created.warnings. Asserts both that the raw marker is gone (negative)
// AND that boundaryMarkerPlaceholder is present (positive) — the positive
// assertion is what proves the marker was actually neutralised rather than
// never having reached the response at all.
func TestHandleConfirmProposal_TypeTask_InvalidKind_WarnMode_SanitisesBoundaryMarker(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", "")
	s := newProposalTestServer(t)

	propID := createTaskProposal(t, s, "Marker kind MCP task", invalidKindTestDescription, markerBearingSuggestedKind)

	result := callConfirmProposal(t, s, propID, "accept")
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(result))
	}
	decoded := decodeConfirmProposalTaskCreated(t, resultText(result))
	if len(decoded.Created.Warnings) != 1 {
		t.Fatalf("created.warnings = %v, want exactly 1 warning", decoded.Created.Warnings)
	}
	warning := decoded.Created.Warnings[0]
	if strings.Contains(warning, storedContextMarkerEnd) {
		t.Errorf("[F173-04] created.warnings[0] = %q, must not contain the raw boundary marker %q", warning, storedContextMarkerEnd)
	}
	if !strings.Contains(warning, boundaryMarkerPlaceholder) {
		t.Errorf("[F173-04] created.warnings[0] = %q, want it to contain %q "+
			"(proof the marker was neutralised, not merely absent)",
			warning, boundaryMarkerPlaceholder)
	}
}

// TestHandleConfirmProposal_TypeTask_InvalidKind_StrictMode_SanitisesBoundaryMarker
// pins F173-08: the same marker-bearing suggested_kind, taken through the
// strict channel's single construction point (tools_proposal.go:1324),
// must still reject the proposal (strict semantics unchanged) while the
// rejection message is sanitised the same way as the warn channel above.
// This replaces the pre-r2 draft's "StrictMode behaviour unchanged" framing,
// which would have pinned the hole in place instead of closing it.
func TestHandleConfirmProposal_TypeTask_InvalidKind_StrictMode_SanitisesBoundaryMarker(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", "true")
	s := newProposalTestServer(t)
	ctx := context.Background()

	propID := createTaskProposal(t, s, "Marker kind MCP task", invalidKindTestDescription, markerBearingSuggestedKind)

	result := callConfirmProposal(t, s, propID, "accept")
	if !result.IsError {
		t.Fatalf("expected tool error in strict mode, got success: %s", resultText(result))
	}
	body := resultText(result)
	if strings.Contains(body, storedContextMarkerEnd) {
		t.Errorf("[F173-08] error text = %q, must not contain the raw boundary marker %q", body, storedContextMarkerEnd)
	}
	if !strings.Contains(body, boundaryMarkerPlaceholder) {
		t.Errorf("[F173-08] error text = %q, want it to contain %q "+
			"(proof the marker was neutralised, not merely absent)",
			body, boundaryMarkerPlaceholder)
	}

	pp, err := s.proposal.Get(ctx, parseTestUUID(t, propID))
	if err != nil {
		t.Fatalf("proposal.Get: %v", err)
	}
	if pp.Status != string(proposal.StatusPending) {
		t.Errorf("proposal.Status = %q, want still pending after strict-mode rejection", pp.Status)
	}
}
