//go:build !integration

package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// --- stub reflection store ---

// stubReflectionStore is an in-memory stub that satisfies reflection.StoreIface.
// Tests configure returnReflection / returnList / returnErr to drive each path.
type stubReflectionStore struct {
	returnReflection *reflection.Reflection
	returnList       []*reflection.Reflection
	returnErr        error
	// captured params for assertion
	lastCreateParams  reflection.CreateParams
	lastListParams    reflection.ListParams
	lastGetLatestType string
}

var _ reflection.StoreIface = (*stubReflectionStore)(nil)

func (s *stubReflectionStore) Create(_ context.Context, p reflection.CreateParams) (*reflection.Reflection, error) {
	s.lastCreateParams = p
	return s.returnReflection, s.returnErr
}

func (s *stubReflectionStore) List(_ context.Context, p reflection.ListParams) ([]*reflection.Reflection, error) {
	s.lastListParams = p
	return s.returnList, s.returnErr
}

func (s *stubReflectionStore) GetLatest(_ context.Context, _ *uuid.UUID, reflType string) (*reflection.Reflection, error) {
	s.lastGetLatestType = reflType
	if s.returnErr != nil {
		return nil, s.returnErr
	}
	return s.returnReflection, nil
}

func (s *stubReflectionStore) ByRelatedEntity(
	_ context.Context, _ *uuid.UUID, _ string, _ uuid.UUID, _ int,
) ([]*reflection.Reflection, error) {
	return s.returnList, s.returnErr
}

func (s *stubReflectionStore) RecentWithPatterns(_ context.Context, _ *uuid.UUID, _ time.Time, _ int) ([]*reflection.Reflection, error) {
	return s.returnList, s.returnErr
}

func (s *stubReflectionStore) PruneOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, s.returnErr
}

// --- helpers ---

func newReflectionServer(store reflection.StoreIface) *Server {
	return &Server{reflection: store}
}

func callGenerateReflection(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleGenerateReflection(context.Background(), req)
	if err != nil {
		t.Fatalf("handleGenerateReflection returned unexpected Go error: %v", err)
	}
	return r
}

func callListReflections(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleListReflections(context.Background(), req)
	if err != nil {
		t.Fatalf("handleListReflections returned unexpected Go error: %v", err)
	}
	return r
}

func callGetLatestReflection(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleGetLatestReflection(context.Background(), req)
	if err != nil {
		t.Fatalf("handleGetLatestReflection returned unexpected Go error: %v", err)
	}
	return r
}

// --- handleGenerateReflection tests ---

func TestHandleGenerateReflection_HappyPath(t *testing.T) {
	id := uuid.New()
	store := &stubReflectionStore{
		returnReflection: &reflection.Reflection{
			ID:      id,
			Type:    "task",
			Summary: "task completed successfully",
		},
	}
	s := newReflectionServer(store)

	r := callGenerateReflection(t, s, map[string]any{
		"type":    "task",
		"summary": "task completed successfully",
	})

	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	// Response must contain "id" field.
	if !strings.Contains(resultText(r), "id") {
		t.Errorf("response does not contain 'id' field, got: %s", resultText(r))
	}
	if store.lastCreateParams.Type != "task" {
		t.Errorf("Type = %q, want %q", store.lastCreateParams.Type, "task")
	}
	if store.lastCreateParams.Summary != "task completed successfully" {
		t.Errorf("Summary = %q, want %q", store.lastCreateParams.Summary, "task completed successfully")
	}
}

func TestHandleGenerateReflection_InvalidType(t *testing.T) {
	s := newReflectionServer(&stubReflectionStore{})

	r := callGenerateReflection(t, s, map[string]any{
		"type":    "",
		"summary": "some summary",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true for empty type, got: %s", resultText(r))
	}
}

func TestHandleGenerateReflection_UnknownType(t *testing.T) {
	s := newReflectionServer(&stubReflectionStore{})

	r := callGenerateReflection(t, s, map[string]any{
		"type":    "bogus_type",
		"summary": "some summary",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true for unknown type, got: %s", resultText(r))
	}
}

func TestHandleGenerateReflection_NullByteSummary(t *testing.T) {
	s := newReflectionServer(&stubReflectionStore{})

	r := callGenerateReflection(t, s, map[string]any{
		"type":    "daily",
		"summary": "summary with null byte \x00 inside",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true for null byte in summary, got: %s", resultText(r))
	}
}

func TestHandleGenerateReflection_CRLFSummary(t *testing.T) {
	s := newReflectionServer(&stubReflectionStore{})

	r := callGenerateReflection(t, s, map[string]any{
		"type":    "weekly",
		"summary": "summary with\r\nnewlines",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true for CRLF in summary, got: %s", resultText(r))
	}
}

func TestHandleGenerateReflection_AllValidTypes(t *testing.T) {
	for reflType := range reflection.AllowedTypes {
		t.Run(reflType, func(t *testing.T) {
			store := &stubReflectionStore{
				returnReflection: &reflection.Reflection{
					ID:   uuid.New(),
					Type: reflType,
				},
			}
			s := newReflectionServer(store)
			r := callGenerateReflection(t, s, map[string]any{
				"type":    reflType,
				"summary": "valid summary",
			})
			if r.IsError {
				t.Errorf("type %q: unexpected error: %s", reflType, resultText(r))
			}
		})
	}
}

func TestHandleGenerateReflection_StoreError(t *testing.T) {
	store := &stubReflectionStore{returnErr: errors.New("db down")}
	s := newReflectionServer(store)

	r := callGenerateReflection(t, s, map[string]any{
		"type":    "system",
		"summary": "some summary",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true when store returns error")
	}
}

func TestHandleGenerateReflection_ConfidenceOutOfRange(t *testing.T) {
	cases := []struct {
		name       string
		confidence float64
	}{
		{"too high", 1.5},
		{"negative", -0.1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newReflectionServer(&stubReflectionStore{})
			r := callGenerateReflection(t, s, map[string]any{
				"type":       "daily",
				"summary":    "some summary",
				"confidence": tc.confidence,
			})
			if !r.IsError {
				t.Fatalf("expected IsError=true for confidence=%f, got: %s", tc.confidence, resultText(r))
			}
		})
	}
}

func TestHandleGenerateReflection_InvalidRelatedEntityID(t *testing.T) {
	s := newReflectionServer(&stubReflectionStore{})

	r := callGenerateReflection(t, s, map[string]any{
		"type":              "task",
		"summary":           "some summary",
		"related_entity_id": "not-a-uuid",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid related_entity_id UUID")
	}
}

// --- handleListReflections tests ---

func TestHandleListReflections_HappyPath(t *testing.T) {
	id1 := uuid.New()
	id2 := uuid.New()
	store := &stubReflectionStore{
		returnList: []*reflection.Reflection{
			{ID: id1, Type: "daily"},
			{ID: id2, Type: "weekly"},
		},
	}
	s := newReflectionServer(store)

	r := callListReflections(t, s, map[string]any{})

	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	txt := resultText(r)
	if !strings.Contains(txt, id1.String()) {
		t.Errorf("response does not contain first reflection id")
	}
}

func TestHandleListReflections_FilterByType(t *testing.T) {
	store := &stubReflectionStore{
		returnList: []*reflection.Reflection{
			{ID: uuid.New(), Type: "decision"},
		},
	}
	s := newReflectionServer(store)

	r := callListReflections(t, s, map[string]any{
		"type": "decision",
	})

	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
}

func TestHandleListReflections_InvalidTypeFilter(t *testing.T) {
	s := newReflectionServer(&stubReflectionStore{})

	r := callListReflections(t, s, map[string]any{
		"type": "invalid_type",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid type filter")
	}
}

func TestHandleListReflections_EmptyResultNilSafe(t *testing.T) {
	// Store returns nil slice — handler must return [] not null.
	store := &stubReflectionStore{returnList: nil}
	s := newReflectionServer(store)

	r := callListReflections(t, s, map[string]any{})

	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	txt := resultText(r)
	if txt == "null" {
		t.Error("response must be [] not null when store returns nil slice")
	}
}

// --- handleGetLatestReflection tests ---

func TestHandleGetLatestReflection_HappyPath(t *testing.T) {
	id := uuid.New()
	store := &stubReflectionStore{
		returnReflection: &reflection.Reflection{
			ID:   id,
			Type: "daily",
		},
	}
	s := newReflectionServer(store)

	r := callGetLatestReflection(t, s, map[string]any{
		"type": "daily",
	})

	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), id.String()) {
		t.Errorf("response does not contain reflection id")
	}
}

func TestHandleGetLatestReflection_NotFound(t *testing.T) {
	store := &stubReflectionStore{returnErr: reflection.ErrNotFound}
	s := newReflectionServer(store)

	r := callGetLatestReflection(t, s, map[string]any{
		"type": "daily",
	})

	if r.IsError {
		t.Fatalf("expected IsError=false when no reflection exists (empty state, not failure), got error: %s", resultText(r))
	}
	if got := strings.TrimSpace(resultText(r)); got != nullStr {
		t.Errorf("expected null content for no-data state, got %q", got)
	}
}

func TestHandleGetLatestReflection_EmptyType(t *testing.T) {
	s := newReflectionServer(&stubReflectionStore{})

	r := callGetLatestReflection(t, s, map[string]any{
		"type": "",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true for empty type")
	}
}

func TestHandleGetLatestReflection_InvalidType(t *testing.T) {
	s := newReflectionServer(&stubReflectionStore{})

	r := callGetLatestReflection(t, s, map[string]any{
		"type": "bogus",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid type")
	}
}

func TestHandleGetLatestReflection_StoreError(t *testing.T) {
	store := &stubReflectionStore{returnErr: errors.New("db unavailable")}
	s := newReflectionServer(store)

	r := callGetLatestReflection(t, s, map[string]any{
		"type": "system",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true for store error")
	}
}
