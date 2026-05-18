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

// trackingDecisionStore records which query method was called, allowing
// handleListDecisions tests to assert routing behaviour without a real DB.
type trackingDecisionStore struct {
	allCalled    bool
	byRepoCalled bool
	lastRepoName string
}

func (d *trackingDecisionStore) Log(_ context.Context, _ decision.LogParams) (*db.Decision, error) {
	return &db.Decision{ID: uuid.New()}, nil
}

func (d *trackingDecisionStore) All(_ context.Context, _ int32) ([]db.Decision, error) {
	d.allCalled = true
	return nil, nil
}

func (d *trackingDecisionStore) ByRepo(_ context.Context, repoName string, _ int32) ([]db.Decision, error) {
	d.byRepoCalled = true
	d.lastRepoName = repoName
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
// routes to decision.All() rather than decision.ByRepo().
func TestHandleListDecisions_EmptyRepoName(t *testing.T) {
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{
		"repo_name": "",
	})
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	if !dec.allCalled {
		t.Error("expected decision.All() to be called for empty repo_name")
	}
	if dec.byRepoCalled {
		t.Error("expected decision.ByRepo() NOT to be called for empty repo_name")
	}
	// Response must be valid JSON.
	if txt := resultText(r); txt != "" {
		var out any
		if err := json.Unmarshal([]byte(txt), &out); err != nil {
			t.Errorf("response is not valid JSON: %v", err)
		}
	}
}

// TestHandleListDecisions_NonEmptyRepoName verifies that a non-empty repo_name
// routes to decision.ByRepo() and NOT to decision.All().
func TestHandleListDecisions_NonEmptyRepoName(t *testing.T) {
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{
		"repo_name": "wayneblacktea",
	})
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	if !dec.byRepoCalled {
		t.Error("expected decision.ByRepo() to be called for non-empty repo_name")
	}
	if dec.lastRepoName != "wayneblacktea" {
		t.Errorf("ByRepo called with %q, want %q", dec.lastRepoName, "wayneblacktea")
	}
	if dec.allCalled {
		t.Error("expected decision.All() NOT to be called when repo_name is set")
	}
}

// TestHandleListDecisions_OmittedRepoName verifies that a missing repo_name key
// (not present in args at all) also routes to decision.All().
func TestHandleListDecisions_OmittedRepoName(t *testing.T) {
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{}) // no repo_name key
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	if !dec.allCalled {
		t.Error("expected decision.All() to be called when repo_name is absent")
	}
	if dec.byRepoCalled {
		t.Error("expected decision.ByRepo() NOT to be called when repo_name is absent")
	}
}
