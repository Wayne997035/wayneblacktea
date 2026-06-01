//go:build !integration

package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/skill"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// --- stub skill store ---

// stubSkillStore is an in-memory stub that satisfies skill.StoreIface.
// Tests configure returnSkill / returnList / returnErr to drive each path.
type stubSkillStore struct {
	returnSkill *skill.Skill
	returnList  []*skill.Skill
	returnErr   error
	// captured params for assertion
	lastAddParams    skill.AddParams
	lastSearchFilter skill.SearchFilter
}

var _ skill.StoreIface = (*stubSkillStore)(nil)

func (s *stubSkillStore) Add(_ context.Context, p skill.AddParams) (*skill.Skill, error) {
	s.lastAddParams = p
	return s.returnSkill, s.returnErr
}

func (s *stubSkillStore) GetByID(_ context.Context, _ string, _ *string) (*skill.Skill, error) {
	if s.returnErr != nil {
		return nil, s.returnErr
	}
	return s.returnSkill, nil
}

func (s *stubSkillStore) Search(_ context.Context, f skill.SearchFilter) ([]*skill.Skill, error) {
	s.lastSearchFilter = f
	return s.returnList, s.returnErr
}

func (s *stubSkillStore) IncrementSuccess(_ context.Context, _ string, _ *string) (*skill.Skill, error) {
	if s.returnErr != nil {
		return nil, s.returnErr
	}
	return s.returnSkill, nil
}

func (s *stubSkillStore) UpdateFromOutcome(_ context.Context, _ skill.UpdateFromOutcomeParams, _ *string) (*skill.Skill, error) {
	if s.returnErr != nil {
		return nil, s.returnErr
	}
	return s.returnSkill, nil
}

func (s *stubSkillStore) ListRelevant(_ context.Context, _ *string, _ string, _ int) ([]*skill.Skill, error) {
	return s.returnList, s.returnErr
}

// --- helpers ---

func newSkillServer(store skill.StoreIface) *Server {
	return &Server{skill: store}
}

func callExtractSkill(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleExtractSkill(context.Background(), req)
	if err != nil {
		t.Fatalf("handleExtractSkill returned unexpected Go error: %v", err)
	}
	return r
}

func callSearchSkills(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleSearchSkills(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSearchSkills returned unexpected Go error: %v", err)
	}
	return r
}

func callUseSkill(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleUseSkill(context.Background(), req)
	if err != nil {
		t.Fatalf("handleUseSkill returned unexpected Go error: %v", err)
	}
	return r
}

// --- handleExtractSkill tests ---

func TestHandleExtractSkill_HappyPath(t *testing.T) {
	store := &stubSkillStore{
		returnSkill: &skill.Skill{
			ID:          "test-skill-id",
			Name:        "go-test-pattern",
			Description: "How to write table-driven tests in Go",
		},
	}
	s := newSkillServer(store)

	r := callExtractSkill(t, s, map[string]any{
		"name":        "go-test-pattern",
		"description": "How to write table-driven tests in Go",
	})

	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if store.lastAddParams.Name != "go-test-pattern" {
		t.Errorf("Name = %q, want %q", store.lastAddParams.Name, "go-test-pattern")
	}
	if store.lastAddParams.Description != "How to write table-driven tests in Go" {
		t.Errorf("Description = %q, want %q", store.lastAddParams.Description, "How to write table-driven tests in Go")
	}
}

func TestHandleExtractSkill_InvalidName(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"empty name", ""},
		{"null byte", "name with \x00 null"},
		{"carriage return", "name with \r"},
		{"newline", "name with \n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSkillServer(&stubSkillStore{})
			r := callExtractSkill(t, s, map[string]any{
				"name":        tc.value,
				"description": "valid description",
			})
			if !r.IsError {
				t.Fatalf("expected IsError=true for name=%q, got: %s", tc.value, resultText(r))
			}
		})
	}
}

func TestHandleExtractSkill_InvalidDescription(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"null byte", "description with \x00"},
		{"CRLF", "description with \r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSkillServer(&stubSkillStore{})
			r := callExtractSkill(t, s, map[string]any{
				"name":        "valid-name",
				"description": tc.value,
			})
			if !r.IsError {
				t.Fatalf("expected IsError=true for description with control chars, got: %s", resultText(r))
			}
		})
	}
}

func TestHandleExtractSkill_NameTooLong(t *testing.T) {
	s := newSkillServer(&stubSkillStore{})
	longName := strings.Repeat("a", 201) // 201 runes > 200 limit

	r := callExtractSkill(t, s, map[string]any{
		"name":        longName,
		"description": "valid description",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true for name > 200 chars, got: %s", resultText(r))
	}
}

func TestHandleExtractSkill_ExactlyAtNameLimit(t *testing.T) {
	store := &stubSkillStore{
		returnSkill: &skill.Skill{
			ID:   "skill-id",
			Name: strings.Repeat("a", 200),
		},
	}
	s := newSkillServer(store)
	exactName := strings.Repeat("a", 200) // exactly 200 — allowed

	r := callExtractSkill(t, s, map[string]any{
		"name":        exactName,
		"description": "valid description",
	})

	if r.IsError {
		t.Fatalf("unexpected error for name at exactly 200 chars: %s", resultText(r))
	}
}

func TestHandleExtractSkill_StoreError(t *testing.T) {
	store := &stubSkillStore{returnErr: errors.New("db down")}
	s := newSkillServer(store)

	r := callExtractSkill(t, s, map[string]any{
		"name":        "valid-skill-name",
		"description": "valid description",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true when store returns error")
	}
}

// --- handleSearchSkills tests ---

func TestHandleSearchSkills_HappyPath(t *testing.T) {
	store := &stubSkillStore{
		returnList: []*skill.Skill{
			{ID: "id-1", Name: "testing-pattern", Description: "test desc"},
			{ID: "id-2", Name: "other-pattern", Description: "other desc"},
		},
	}
	s := newSkillServer(store)

	r := callSearchSkills(t, s, map[string]any{
		"query": "pattern",
		"limit": float64(5),
	})

	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if store.lastSearchFilter.Query != "pattern" {
		t.Errorf("Query = %q, want %q", store.lastSearchFilter.Query, "pattern")
	}
	if !strings.Contains(resultText(r), "id-1") {
		t.Errorf("response does not contain first skill id")
	}
}

func TestHandleSearchSkills_EmptyQuery(t *testing.T) {
	s := newSkillServer(&stubSkillStore{})

	r := callSearchSkills(t, s, map[string]any{
		"query": "",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true for empty query")
	}
}

func TestHandleSearchSkills_StoreError(t *testing.T) {
	store := &stubSkillStore{returnErr: errors.New("search failed")}
	s := newSkillServer(store)

	r := callSearchSkills(t, s, map[string]any{
		"query": "some-query",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true when store returns error")
	}
}

func TestHandleSearchSkills_EmptyResultNilSafe(t *testing.T) {
	// Store returns nil slice — handler must return [] not null.
	store := &stubSkillStore{returnList: nil}
	s := newSkillServer(store)

	r := callSearchSkills(t, s, map[string]any{
		"query": "nothing",
	})

	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	txt := resultText(r)
	if txt == "null" {
		t.Error("response must be [] not null when store returns nil slice")
	}
}

// --- handleUseSkill tests ---

func TestHandleUseSkill_HappyPath(t *testing.T) {
	store := &stubSkillStore{
		returnSkill: &skill.Skill{
			ID:           "skill-abc",
			Name:         "my-skill",
			SuccessCount: 1,
		},
	}
	s := newSkillServer(store)

	r := callUseSkill(t, s, map[string]any{
		"skill_id": "skill-abc",
	})

	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "skill-abc") {
		t.Errorf("response does not contain skill id")
	}
}

func TestHandleUseSkill_NotFound(t *testing.T) {
	store := &stubSkillStore{returnErr: skill.ErrNotFound}
	s := newSkillServer(store)

	r := callUseSkill(t, s, map[string]any{
		"skill_id": "unknown-skill-id",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true for unknown skill ID")
	}
}

func TestHandleUseSkill_EmptySkillID(t *testing.T) {
	s := newSkillServer(&stubSkillStore{})

	r := callUseSkill(t, s, map[string]any{
		"skill_id": "",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true for empty skill_id")
	}
}

func TestHandleUseSkill_StoreError(t *testing.T) {
	store := &stubSkillStore{returnErr: errors.New("db unavailable")}
	s := newSkillServer(store)

	r := callUseSkill(t, s, map[string]any{
		"skill_id": "some-skill-id",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true for store error")
	}
}
