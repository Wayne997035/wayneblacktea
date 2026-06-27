//go:build !integration

package mcp

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// outcomeSuccess / outcomeFailure are goconst-required constants for the
// outcome string values used in ≥3 test assertions.
// ruleStatusActive is the expected status after a successful outcome application.
const (
	outcomeSuccess   = "success"
	outcomeFailure   = "failure"
	ruleStatusActive = "active"
)

// ---------------------------------------------------------------------------
// stub behaviorrule.StoreIface
// ---------------------------------------------------------------------------

type stubBehaviorRuleStore struct {
	proposeReturn   *behaviorrule.BehaviorRule
	listReturn      []*behaviorrule.BehaviorRule
	applyReturn     *behaviorrule.BehaviorRule
	deprecateReturn *behaviorrule.BehaviorRule
	returnErr       error

	lastCreateParams behaviorrule.CreateParams
	lastListParams   behaviorrule.ListParams
	lastApplyID      uuid.UUID
	lastApplyOutcome string
	lastDeprecateID  uuid.UUID
}

var _ behaviorrule.StoreIface = (*stubBehaviorRuleStore)(nil)

func (s *stubBehaviorRuleStore) Propose(_ context.Context, p behaviorrule.CreateParams) (*behaviorrule.BehaviorRule, error) {
	s.lastCreateParams = p
	return s.proposeReturn, s.returnErr
}

func (s *stubBehaviorRuleStore) List(_ context.Context, p behaviorrule.ListParams) ([]*behaviorrule.BehaviorRule, error) {
	s.lastListParams = p
	return s.listReturn, s.returnErr
}

func (s *stubBehaviorRuleStore) GetByID(_ context.Context, _ uuid.UUID) (*behaviorrule.BehaviorRule, error) {
	return nil, s.returnErr
}

func (s *stubBehaviorRuleStore) ApplyOutcome(_ context.Context, id uuid.UUID, outcome string) (*behaviorrule.BehaviorRule, error) {
	s.lastApplyID = id
	s.lastApplyOutcome = outcome
	return s.applyReturn, s.returnErr
}

func (s *stubBehaviorRuleStore) Deprecate(_ context.Context, id uuid.UUID) (*behaviorrule.BehaviorRule, error) {
	s.lastDeprecateID = id
	return s.deprecateReturn, s.returnErr
}

func (s *stubBehaviorRuleStore) PruneOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, s.returnErr
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newBehaviorRuleServer(store behaviorrule.StoreIface) *Server {
	return &Server{behaviorRule: store}
}

func callProposeBehaviorRule(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleProposeBehaviorRule(context.Background(), req)
	if err != nil {
		t.Fatalf("handleProposeBehaviorRule returned unexpected Go error: %v", err)
	}
	return r
}

func callListBehaviorRules(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleListBehaviorRules(context.Background(), req)
	if err != nil {
		t.Fatalf("handleListBehaviorRules returned unexpected Go error: %v", err)
	}
	return r
}

func callApplyBehaviorRules(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleApplyBehaviorRules(context.Background(), req)
	if err != nil {
		t.Fatalf("handleApplyBehaviorRules returned unexpected Go error: %v", err)
	}
	return r
}

func callDeprecateBehaviorRule(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleDeprecateBehaviorRule(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDeprecateBehaviorRule returned unexpected Go error: %v", err)
	}
	return r
}

// ---------------------------------------------------------------------------
// handleProposeBehaviorRule tests
// ---------------------------------------------------------------------------

func TestHandleProposeBehaviorRule_NilStore(t *testing.T) {
	s := newBehaviorRuleServer(nil)
	r := callProposeBehaviorRule(t, s, map[string]any{
		"condition":   "when task is stuck",
		"action":      "ping user",
		"source_type": "manual",
	})
	if !r.IsError {
		t.Fatal("expected IsError=true for nil store, got success")
	}
}

func TestHandleProposeBehaviorRule_HappyPath(t *testing.T) {
	id := uuid.New()
	store := &stubBehaviorRuleStore{
		proposeReturn: &behaviorrule.BehaviorRule{
			ID:         id,
			Condition:  "when task is stuck",
			Action:     "ping user",
			SourceType: "manual",
			Confidence: 0.50,
			Status:     "proposed",
		},
	}
	s := newBehaviorRuleServer(store)
	r := callProposeBehaviorRule(t, s, map[string]any{
		"condition":   "when task is stuck",
		"action":      "ping user",
		"source_type": "manual",
	})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "id") {
		t.Errorf("response missing 'id' field, got: %s", resultText(r))
	}
	if store.lastCreateParams.Condition != "when task is stuck" {
		t.Errorf("Condition = %q, want %q", store.lastCreateParams.Condition, "when task is stuck")
	}
}

func TestHandleProposeBehaviorRule_ConditionNullByte(t *testing.T) {
	store := &stubBehaviorRuleStore{}
	s := newBehaviorRuleServer(store)
	r := callProposeBehaviorRule(t, s, map[string]any{
		"condition":   "bad\x00condition",
		"action":      "do something",
		"source_type": "manual",
	})
	if !r.IsError {
		t.Fatal("expected IsError=true for null byte in condition")
	}
}

func TestHandleProposeBehaviorRule_ActionTooLong(t *testing.T) {
	store := &stubBehaviorRuleStore{}
	s := newBehaviorRuleServer(store)
	longAction := strings.Repeat("x", 2001)
	r := callProposeBehaviorRule(t, s, map[string]any{
		"condition":   "valid condition",
		"action":      longAction,
		"source_type": "manual",
	})
	if !r.IsError {
		t.Fatal("expected IsError=true for action exceeding 2000 chars")
	}
}

func TestHandleProposeBehaviorRule_ConfidenceNaN(t *testing.T) {
	store := &stubBehaviorRuleStore{}
	s := newBehaviorRuleServer(store)
	r := callProposeBehaviorRule(t, s, map[string]any{
		"condition":   "valid condition",
		"action":      "valid action",
		"source_type": "manual",
		"confidence":  math.NaN(),
	})
	if !r.IsError {
		t.Fatal("expected IsError=true for NaN confidence")
	}
}

func TestHandleProposeBehaviorRule_ConfidenceInf(t *testing.T) {
	store := &stubBehaviorRuleStore{}
	s := newBehaviorRuleServer(store)
	r := callProposeBehaviorRule(t, s, map[string]any{
		"condition":   "valid condition",
		"action":      "valid action",
		"source_type": "manual",
		"confidence":  math.Inf(1),
	})
	if !r.IsError {
		t.Fatal("expected IsError=true for Inf confidence")
	}
}

func TestHandleProposeBehaviorRule_ConfidenceValidPropagated(t *testing.T) {
	id := uuid.New()
	store := &stubBehaviorRuleStore{
		proposeReturn: &behaviorrule.BehaviorRule{
			ID:         id,
			Condition:  "valid condition",
			Action:     "valid action",
			SourceType: "reflection",
			Confidence: 0.75,
			Status:     "proposed",
		},
	}
	s := newBehaviorRuleServer(store)
	r := callProposeBehaviorRule(t, s, map[string]any{
		"condition":   "valid condition",
		"action":      "valid action",
		"source_type": "reflection",
		"confidence":  float64(0.75),
	})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if store.lastCreateParams.Confidence < 0.749 || store.lastCreateParams.Confidence > 0.751 {
		t.Errorf("Confidence = %v, want ~0.75", store.lastCreateParams.Confidence)
	}
}

func TestHandleProposeBehaviorRule_InvalidSourceType(t *testing.T) {
	s := newBehaviorRuleServer(&stubBehaviorRuleStore{})
	r := callProposeBehaviorRule(t, s, map[string]any{
		"condition":   "valid condition",
		"action":      "valid action",
		"source_type": "unknown",
	})
	if !r.IsError {
		t.Fatal("expected IsError=true for invalid source_type")
	}
}

func TestHandleProposeBehaviorRule_StoreError(t *testing.T) {
	store := &stubBehaviorRuleStore{returnErr: errors.New("db down")}
	s := newBehaviorRuleServer(store)
	r := callProposeBehaviorRule(t, s, map[string]any{
		"condition":   "valid condition",
		"action":      "valid action",
		"source_type": "manual",
	})
	if !r.IsError {
		t.Fatal("expected IsError=true when store returns error")
	}
}

// ---------------------------------------------------------------------------
// handleListBehaviorRules tests
// ---------------------------------------------------------------------------

func TestHandleListBehaviorRules_NilStore(t *testing.T) {
	s := newBehaviorRuleServer(nil)
	r := callListBehaviorRules(t, s, map[string]any{})
	if !r.IsError {
		t.Fatal("expected IsError=true for nil store")
	}
}

func TestHandleListBehaviorRules_HappyPath(t *testing.T) {
	id := uuid.New()
	store := &stubBehaviorRuleStore{
		listReturn: []*behaviorrule.BehaviorRule{
			{ID: id, Condition: "cond", Action: "act", Status: ruleStatusActive},
		},
	}
	s := newBehaviorRuleServer(store)
	r := callListBehaviorRules(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), id.String()) {
		t.Errorf("response missing rule ID")
	}
}

func TestHandleListBehaviorRules_StatusFilter(t *testing.T) {
	store := &stubBehaviorRuleStore{listReturn: []*behaviorrule.BehaviorRule{}}
	s := newBehaviorRuleServer(store)
	r := callListBehaviorRules(t, s, map[string]any{"status": ruleStatusActive})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if store.lastListParams.Status == nil || *store.lastListParams.Status != ruleStatusActive {
		t.Errorf("Status filter not propagated, got %v", store.lastListParams.Status)
	}
}

func TestHandleListBehaviorRules_InvalidStatus(t *testing.T) {
	s := newBehaviorRuleServer(&stubBehaviorRuleStore{})
	r := callListBehaviorRules(t, s, map[string]any{"status": "unknown_status"})
	if !r.IsError {
		t.Fatal("expected IsError=true for invalid status filter")
	}
}

// ---------------------------------------------------------------------------
// handleApplyBehaviorRules tests
// ---------------------------------------------------------------------------

func TestHandleApplyBehaviorRules_NilStore(t *testing.T) {
	s := newBehaviorRuleServer(nil)
	r := callApplyBehaviorRules(t, s, map[string]any{
		"rule_id": uuid.New().String(),
		"outcome": "success",
	})
	if !r.IsError {
		t.Fatal("expected IsError=true for nil store")
	}
}

func TestHandleApplyBehaviorRules_InvalidUUID(t *testing.T) {
	s := newBehaviorRuleServer(&stubBehaviorRuleStore{})
	r := callApplyBehaviorRules(t, s, map[string]any{
		"rule_id": "not-a-uuid",
		"outcome": "success",
	})
	if !r.IsError {
		t.Fatal("expected IsError=true for invalid UUID")
	}
}

func TestHandleApplyBehaviorRules_HappyPath(t *testing.T) {
	id := uuid.New()
	store := &stubBehaviorRuleStore{
		applyReturn: &behaviorrule.BehaviorRule{
			ID:         id,
			Status:     ruleStatusActive,
			Confidence: 0.55,
		},
	}
	s := newBehaviorRuleServer(store)
	r := callApplyBehaviorRules(t, s, map[string]any{
		"rule_id": id.String(),
		"outcome": outcomeSuccess,
	})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if store.lastApplyID != id {
		t.Errorf("ApplyOutcome called with wrong ID")
	}
	if store.lastApplyOutcome != outcomeSuccess {
		t.Errorf("ApplyOutcome called with wrong outcome: %q", store.lastApplyOutcome)
	}
}

func TestHandleApplyBehaviorRules_InvalidOutcome(t *testing.T) {
	s := newBehaviorRuleServer(&stubBehaviorRuleStore{})
	r := callApplyBehaviorRules(t, s, map[string]any{
		"rule_id": uuid.New().String(),
		"outcome": "bogus",
	})
	if !r.IsError {
		t.Fatal("expected IsError=true for invalid outcome")
	}
}

// ---------------------------------------------------------------------------
// handleDeprecateBehaviorRule tests
// ---------------------------------------------------------------------------

func TestHandleDeprecateBehaviorRule_NilStore(t *testing.T) {
	s := newBehaviorRuleServer(nil)
	r := callDeprecateBehaviorRule(t, s, map[string]any{
		"rule_id": uuid.New().String(),
	})
	if !r.IsError {
		t.Fatal("expected IsError=true for nil store")
	}
}

func TestHandleDeprecateBehaviorRule_InvalidUUID(t *testing.T) {
	s := newBehaviorRuleServer(&stubBehaviorRuleStore{})
	r := callDeprecateBehaviorRule(t, s, map[string]any{
		"rule_id": "bad-uuid",
	})
	if !r.IsError {
		t.Fatal("expected IsError=true for invalid UUID")
	}
}

func TestHandleDeprecateBehaviorRule_HappyPath(t *testing.T) {
	id := uuid.New()
	store := &stubBehaviorRuleStore{
		deprecateReturn: &behaviorrule.BehaviorRule{
			ID:     id,
			Status: "deprecated",
		},
	}
	s := newBehaviorRuleServer(store)
	r := callDeprecateBehaviorRule(t, s, map[string]any{
		"rule_id": id.String(),
	})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if store.lastDeprecateID != id {
		t.Errorf("Deprecate called with wrong ID")
	}
}

func TestHandleDeprecateBehaviorRule_NotFound(t *testing.T) {
	store := &stubBehaviorRuleStore{returnErr: behaviorrule.ErrNotFound}
	s := newBehaviorRuleServer(store)
	r := callDeprecateBehaviorRule(t, s, map[string]any{
		"rule_id": uuid.New().String(),
	})
	if !r.IsError {
		t.Fatal("expected IsError=true when rule not found")
	}
}
