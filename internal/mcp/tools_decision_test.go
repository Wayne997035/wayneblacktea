package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// trackingDecisionStore records the List call made by handleListDecisions,
// allowing tests to assert the ListParams built from MCP tool arguments
// without a real DB. All/ByRepo/ByProject/ByTask/SearchByCosine are no-ops —
// handleListDecisions no longer calls them directly (P3.0a Stage B routes
// list_decisions exclusively through List).
type trackingDecisionStore struct {
	listCalled     bool
	lastListParams decision.ListParams
	listResult     []db.Decision
	listErr        error
	lastLogged     decision.LogParams
}

func (d *trackingDecisionStore) Log(_ context.Context, p decision.LogParams) (*db.Decision, error) {
	d.lastLogged = p
	return &db.Decision{ID: uuid.New()}, nil
}

func (d *trackingDecisionStore) All(_ context.Context, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (d *trackingDecisionStore) ByRepo(_ context.Context, _ string, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (d *trackingDecisionStore) ByProject(_ context.Context, _ uuid.UUID, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (d *trackingDecisionStore) ByTask(_ context.Context, _ uuid.UUID, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (d *trackingDecisionStore) SearchByCosine(_ context.Context, _ []float32, _ int) ([]db.Decision, error) {
	return nil, nil
}

func (d *trackingDecisionStore) List(_ context.Context, p decision.ListParams) ([]db.Decision, error) {
	d.listCalled = true
	d.lastListParams = p
	return d.listResult, d.listErr
}

// Compile-time: trackingDecisionStore must satisfy decision.StoreIface.
var _ decision.StoreIface = (*trackingDecisionStore)(nil)

// callListDecisions invokes handleListDecisions with the given args.
func callListDecisions(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleListDecisions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleListDecisions returned unexpected error: %v", err)
	}
	return result
}

// TestHandleListDecisions_EmptyRepoName verifies that an empty repo_name
// (with no project_id) is treated the same as "neither" — a workspace-wide
// List call with both filters empty (P3.0a Stage B truth table row "neither").
func TestHandleListDecisions_EmptyRepoName(t *testing.T) {
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{
		"repo_name": "",
	})
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	if !dec.listCalled {
		t.Fatal("expected decision.List() to be called")
	}
	if dec.lastListParams.ProjectID != nil || dec.lastListParams.RepoName != "" {
		t.Errorf("expected workspace-wide ListParams (no project, no repo), got %+v", dec.lastListParams)
	}
	// Response must be valid JSON.
	if txt := resultText(r); txt != "" {
		var out any
		if err := json.Unmarshal([]byte(txt), &out); err != nil {
			t.Errorf("response is not valid JSON: %v", err)
		}
	}
}

// TestHandleListDecisions_NonEmptyRepoName verifies "repo only" — the
// ListParams built from a non-empty repo_name carries RepoName and no
// ProjectID (P3.0a Stage B truth table row "repo only").
func TestHandleListDecisions_NonEmptyRepoName(t *testing.T) {
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{
		"repo_name": "wayneblacktea",
	})
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	if !dec.listCalled {
		t.Fatal("expected decision.List() to be called")
	}
	if dec.lastListParams.RepoName != "wayneblacktea" {
		t.Errorf("ListParams.RepoName = %q, want %q", dec.lastListParams.RepoName, "wayneblacktea")
	}
	if dec.lastListParams.ProjectID != nil {
		t.Errorf("ListParams.ProjectID = %v, want nil", dec.lastListParams.ProjectID)
	}
}

// TestHandleListDecisions_OmittedRepoName verifies a missing repo_name key
// (not present in args at all) is also treated as "neither" (P3.0a Stage B
// truth table row "neither").
func TestHandleListDecisions_OmittedRepoName(t *testing.T) {
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{}) // no repo_name key
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	if !dec.listCalled {
		t.Fatal("expected decision.List() to be called")
	}
	if dec.lastListParams.ProjectID != nil || dec.lastListParams.RepoName != "" {
		t.Errorf("expected workspace-wide ListParams (no project, no repo), got %+v", dec.lastListParams)
	}
}

// TestHandleListDecisions_InvalidProjectUUID verifies the truth table row
// "invalid project UUID -> tool error, store never called".
func TestHandleListDecisions_InvalidProjectUUID(t *testing.T) {
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{
		"project_id": "not-a-valid-uuid",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid project_id UUID, got result: %s", resultText(r))
	}
	if txt := resultText(r); txt != errMsgInvalidProjectIDUUID {
		t.Errorf("unexpected error message: %q", txt)
	}
	if dec.listCalled {
		t.Error("expected decision.List() NOT to be called when project_id is malformed")
	}
}

// TestHandleListDecisions_ProjectWinsOverRepo verifies the truth table row
// "project + repo both given -> project wins" — RepoName must be cleared
// from ListParams once a valid project_id is present.
func TestHandleListDecisions_ProjectWinsOverRepo(t *testing.T) {
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}
	projectID := uuid.New()

	r := callListDecisions(t, s, map[string]any{
		"project_id": projectID.String(),
		"repo_name":  "wayneblacktea",
	})
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	if dec.lastListParams.ProjectID == nil || *dec.lastListParams.ProjectID != projectID {
		t.Errorf("ListParams.ProjectID = %v, want %s", dec.lastListParams.ProjectID, projectID)
	}
	if dec.lastListParams.RepoName != "" {
		t.Errorf("ListParams.RepoName = %q, want empty (project must win over repo_name)", dec.lastListParams.RepoName)
	}
}

// TestHandleListDecisions_NonexistentProjectReturnsEmpty verifies the truth
// table row "project not owned / nonexistent -> returns [] (not an error)" —
// when the store returns an empty result for a well-formed-but-unmatched
// project_id, handleListDecisions must NOT turn that into a tool error.
func TestHandleListDecisions_NonexistentProjectReturnsEmpty(t *testing.T) {
	dec := &trackingDecisionStore{listResult: nil} // store found nothing
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{
		"project_id": uuid.New().String(),
	})
	if r.IsError {
		t.Fatalf("expected IsError=false for nonexistent project, got: %s", resultText(r))
	}
	var out []db.Decision
	if err := json.Unmarshal([]byte(resultText(r)), &out); err != nil {
		t.Fatalf("response is not valid JSON array: %v (%s)", err, resultText(r))
	}
	if len(out) != 0 {
		t.Errorf("expected empty array, got %d rows", len(out))
	}
}

// TestHandleListDecisions_IncludeAutoOmittedDefaultsFalse verifies the truth
// table row "include_auto omitted or non-bool -> treated as false".
func TestHandleListDecisions_IncludeAutoOmittedDefaultsFalse(t *testing.T) {
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	callListDecisions(t, s, map[string]any{}) // include_auto absent
	if dec.lastListParams.IncludeAuto {
		t.Error("expected IncludeAuto=false when include_auto is omitted")
	}
}

// TestHandleListDecisions_IncludeAutoNonBoolDefaultsFalse verifies the truth
// table row "include_auto omitted or non-bool -> treated as false" for the
// non-bool case specifically (fail-closed, not fail-open).
func TestHandleListDecisions_IncludeAutoNonBoolDefaultsFalse(t *testing.T) {
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	callListDecisions(t, s, map[string]any{"include_auto": "true"}) // string, not bool
	if dec.lastListParams.IncludeAuto {
		t.Error("expected IncludeAuto=false when include_auto is a non-bool value")
	}
}

// TestHandleListDecisions_IncludeAutoTrue verifies the truth table row
// "include_auto=true -> returns manual + auto" at the ListParams-plumbing
// level (actual filtering is a store-layer concern, covered by store tests).
func TestHandleListDecisions_IncludeAutoTrue(t *testing.T) {
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	callListDecisions(t, s, map[string]any{"include_auto": true})
	if !dec.lastListParams.IncludeAuto {
		t.Error("expected IncludeAuto=true when include_auto=true")
	}
}

// callLogDecision invokes handleLogDecision with the given args.
func callLogDecision(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleLogDecision(context.Background(), req)
	if err != nil {
		t.Fatalf("handleLogDecision returned unexpected Go error: %v", err)
	}
	return result
}

// TestHandleLogDecision_InvalidTaskIDUUID verifies that a malformed task_id
// UUID causes handleLogDecision to return a tool-level error result
// (IsError=true) rather than a Go error, matching the MCP contract.
func TestHandleLogDecision_InvalidTaskIDUUID(t *testing.T) {
	s := &Server{decision: &trackingDecisionStore{}}

	r := callLogDecision(t, s, map[string]any{
		"title":     "ADR: use SQLite for local dev",
		"context":   "we want zero-dependency local setup",
		"decision":  "ship SQLite backend",
		"rationale": "no Postgres required",
		"task_id":   "not-a-valid-uuid",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid task_id UUID, got result: %s", resultText(r))
	}
	if txt := resultText(r); txt != errMsgInvalidTaskIDUUID {
		t.Errorf("unexpected error message: %q", txt)
	}
}

// TestHandleLogDecision_ForgedSourceArgIgnored verifies the provenance
// contract for the manual MCP path (P3.0a producer #1): a caller cannot
// influence decisions.source by adding an arbitrary "source" argument.
// handleLogDecision never reads a "source" key at all — Source is bound to
// decision.SourceManual as a path constant — so a forged "source":"auto" arg
// (attempting to masquerade a manual log_decision call as system-inferred)
// is silently ignored and the persisted LogParams.Source stays "manual".
func TestHandleLogDecision_ForgedSourceArgIgnored(t *testing.T) {
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	r := callLogDecision(t, s, map[string]any{
		"title":     "ADR: use SQLite for local dev",
		"context":   "we want zero-dependency local setup",
		"decision":  "ship SQLite backend",
		"rationale": "no Postgres required",
		"source":    "auto", // forged: not a real tool arg, must be ignored
	})
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	if dec.lastLogged.Source != decision.SourceManual {
		t.Errorf("Source = %q, want %q (forged arg must not override the path constant)",
			dec.lastLogged.Source, decision.SourceManual)
	}
}
