package mcp

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// stubKnowledgeStore is a no-op implementation of knowledge.StoreIface used
// to build a minimal Server for UUID validation path tests. UUID checks happen
// before AddItem is ever called, so all methods can be no-ops.
type stubKnowledgeStore struct{}

func (s *stubKnowledgeStore) AddItem(_ context.Context, _ knowledge.AddItemParams) (*db.KnowledgeItem, error) {
	return &db.KnowledgeItem{ID: uuid.New()}, nil
}

func (s *stubKnowledgeStore) Search(_ context.Context, _ string, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) SearchCoarse(_ context.Context, _ string, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) List(_ context.Context, _, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) GetByID(_ context.Context, _ uuid.UUID) (*db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) UpdateLearningValue(_ context.Context, _ uuid.UUID, _ int) error {
	return nil
}

func (s *stubKnowledgeStore) SearchByCosine(_ context.Context, _ []float32, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) ListChildren(_ context.Context, _ uuid.UUID) ([]*db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) ListRoots(_ context.Context) ([]*db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) ListByProjectID(_ context.Context, _ uuid.UUID, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (s *stubKnowledgeStore) ListByTaskID(_ context.Context, _ uuid.UUID, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

// Compile-time assertion: stubKnowledgeStore implements knowledge.StoreIface.
var _ knowledge.StoreIface = (*stubKnowledgeStore)(nil)

// callAddKnowledge invokes handleAddKnowledge with the given args.
func callAddKnowledge(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleAddKnowledge(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAddKnowledge returned unexpected Go error: %v", err)
	}
	return result
}

// TestHandleAddKnowledge_InvalidProjectIDUUID verifies that a malformed
// project_id UUID value causes handleAddKnowledge to return a tool-level
// error result (IsError=true) rather than a Go error, matching the MCP
// contract.
func TestHandleAddKnowledge_InvalidProjectIDUUID(t *testing.T) {
	s := &Server{knowledge: &stubKnowledgeStore{}}

	r := callAddKnowledge(t, s, map[string]any{
		"type":       "til",
		"title":      "Test item",
		"project_id": "not-a-valid-uuid",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid project_id UUID, got result: %s", resultText(r))
	}
	if txt := resultText(r); txt != errMsgInvalidProjectIDUUID {
		t.Errorf("unexpected error message: %q", txt)
	}
}

// TestHandleAddKnowledge_InvalidTaskIDUUID verifies that a malformed task_id
// UUID value causes handleAddKnowledge to return a tool-level error result.
func TestHandleAddKnowledge_InvalidTaskIDUUID(t *testing.T) {
	s := &Server{knowledge: &stubKnowledgeStore{}}

	r := callAddKnowledge(t, s, map[string]any{
		"type":    "article",
		"title":   "Test item",
		"task_id": "bad-uuid-value",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid task_id UUID, got result: %s", resultText(r))
	}
	if txt := resultText(r); txt != errMsgInvalidTaskIDUUID {
		t.Errorf("unexpected error message: %q", txt)
	}
}
