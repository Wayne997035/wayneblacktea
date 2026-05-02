package mcp

import (
	"context"
	"strings"
	"testing"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// newMinimalPlanServer creates a Server with a GTD store only (no work session),
// backed by the full in-memory SQLite test helper from tools_worksession_test.go.
// It re-uses newTestWorkSessionServer because confirm_plan needs at least gtd.
func newMinimalPlanServer(t *testing.T) *Server {
	t.Helper()
	return newTestWorkSessionServer(t)
}

func callConfirmPlan(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleConfirmPlan(context.Background(), req)
	if err != nil {
		t.Fatalf("handleConfirmPlan error: %v", err)
	}
	return result
}

// ---- input validation ----

func TestHandleConfirmPlan_MissingPhases(t *testing.T) {
	s := newMinimalPlanServer(t)
	r := callConfirmPlan(t, s, map[string]any{})
	if !r.IsError {
		t.Error("expected error for missing phases")
	}
	if !strings.Contains(resultText(r), "phases") {
		t.Errorf("error should mention 'phases', got: %s", resultText(r))
	}
}

func TestHandleConfirmPlan_InvalidPhasesJSON(t *testing.T) {
	s := newMinimalPlanServer(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases": "{not-valid-json}",
	})
	if !r.IsError {
		t.Error("expected error for invalid phases JSON")
	}
}

func TestHandleConfirmPlan_EmptyPhasesArray(t *testing.T) {
	s := newMinimalPlanServer(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases": "[]",
	})
	if !r.IsError {
		t.Error("expected error for empty phases array")
	}
}

func TestHandleConfirmPlan_InvalidProjectIDUUID(t *testing.T) {
	s := newMinimalPlanServer(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases":     `[{"title":"T","description":"D","priority":2}]`,
		"project_id": "not-a-uuid",
	})
	if !r.IsError {
		t.Error("expected error for invalid project_id UUID")
	}
}

func TestHandleConfirmPlan_InvalidDecisionsJSON(t *testing.T) {
	s := newMinimalPlanServer(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases":    `[{"title":"T","description":"D","priority":2}]`,
		"decisions": "{bad-json}",
	})
	if !r.IsError {
		t.Error("expected error for invalid decisions JSON")
	}
}

// ---- happy path: task creation ----

func TestHandleConfirmPlan_SinglePhase(t *testing.T) {
	s := newMinimalPlanServer(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases": `[{"title":"Implement auth","description":"JWT auth","priority":1}]`,
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "Plan confirmed") {
		t.Errorf("response missing 'Plan confirmed': %s", text)
	}
	if !strings.Contains(text, "Tasks created (1)") {
		t.Errorf("response missing 'Tasks created (1)': %s", text)
	}
	if !strings.Contains(text, "Implement auth") {
		t.Errorf("response missing task title 'Implement auth': %s", text)
	}
}

func TestHandleConfirmPlan_MultiplePhases(t *testing.T) {
	s := newMinimalPlanServer(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases": `[
			{"title":"Phase 1","description":"First","priority":1},
			{"title":"Phase 2","description":"Second","priority":2},
			{"title":"Phase 3","description":"Third","priority":3}
		]`,
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "Tasks created (3)") {
		t.Errorf("response missing 'Tasks created (3)': %s", text)
	}
	for _, title := range []string{"Phase 1", "Phase 2", "Phase 3"} {
		if !strings.Contains(text, title) {
			t.Errorf("response missing phase title %q: %s", title, text)
		}
	}
}

// ---- decisions logging ----

func TestHandleConfirmPlan_WithDecisions(t *testing.T) {
	s := newMinimalPlanServer(t)
	decisions := `[{"title":"Use Echo","context":"HTTP framework","decision":"Echo","rationale":"Fast"}]`
	r := callConfirmPlan(t, s, map[string]any{
		"phases":    `[{"title":"Build API","description":"REST API","priority":2}]`,
		"decisions": decisions,
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "Decisions logged (1)") {
		t.Errorf("response missing 'Decisions logged (1)': %s", text)
	}
	if !strings.Contains(text, "Use Echo") {
		t.Errorf("response missing decision title 'Use Echo': %s", text)
	}
}

func TestHandleConfirmPlan_DecisionMissingTitle_Skipped(t *testing.T) {
	// A decision with empty title should be skipped (not logged).
	s := newMinimalPlanServer(t)
	decisions := `[{"title":"","context":"x","decision":"y","rationale":"z"}]`
	r := callConfirmPlan(t, s, map[string]any{
		"phases":    `[{"title":"Do something","description":"x","priority":2}]`,
		"decisions": decisions,
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	text := resultText(r)
	// No "Decisions logged" since the one decision has empty title → skipped.
	if strings.Contains(text, "Decisions logged") {
		t.Errorf("empty-title decision should be skipped; got: %s", text)
	}
}

// ---- no-work-session guard: confirm_plan works without workSession store ----

func TestHandleConfirmPlan_NoWorkSessionStore(t *testing.T) {
	// Explicitly create a server without workSession set — confirm_plan must
	// still succeed (best-effort: missing work session store is not fatal).
	s := newTestWorkSessionServer(t)
	s.workSession = nil // remove work session store

	r := callConfirmPlan(t, s, map[string]any{
		"phases":    `[{"title":"No-session phase","description":"x","priority":2}]`,
		"repo_name": "test-repo",
	})
	if r.IsError {
		t.Fatalf("expected success even without workSession store, got: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "Plan confirmed") {
		t.Errorf("response missing 'Plan confirmed': %s", text)
	}
	// No session started (workSession is nil).
	if strings.Contains(text, "Work session started") {
		t.Errorf("should not report session when workSession=nil, got: %s", text)
	}
}

// ---- no repo_name: no session created ----

func TestHandleConfirmPlan_NoRepoName_NoSession(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callConfirmPlan(t, s, map[string]any{
		"phases": `[{"title":"Anon phase","description":"x","priority":2}]`,
		// no repo_name
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	text := resultText(r)
	// No repo_name → createWorkSessionForPlan skips silently.
	if strings.Contains(text, "Work session started") {
		t.Errorf("should not create session without repo_name, got: %s", text)
	}
}

// ---- phase title with empty string is skipped ----

func TestHandleConfirmPlan_EmptyPhaseTitleSkipped(t *testing.T) {
	s := newMinimalPlanServer(t)
	// 3 phases but one has an empty title — should produce 2 tasks.
	r := callConfirmPlan(t, s, map[string]any{
		"phases": `[
			{"title":"Do A","description":"A","priority":1},
			{"title":"","description":"skip","priority":2},
			{"title":"Do C","description":"C","priority":3}
		]`,
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "Tasks created (2)") {
		t.Errorf("empty-title phase should be skipped; expected 2 tasks: %s", text)
	}
}
