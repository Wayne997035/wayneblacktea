package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// entityTypeTask is the repeated "task" string used across outcome test cases.
const entityTypeTask = "task"

// nullStr is the "null" literal used in nil-safe JSON response assertions.
const nullStr = "null"

// --- stub store ---

// stubOutcomeStore is an in-memory stub that satisfies outcome.StoreIface.
// Tests configure returnOutcome / returnEvals / returnErr to drive each path.
type stubOutcomeStore struct {
	returnOutcome outcome.Outcome
	returnEval    outcome.Evaluation
	returnList    []outcome.Outcome
	returnEvals   []outcome.Evaluation
	returnErr     error
	// captured params for assertion
	lastCreateParams   outcome.CreateOutcomeParams
	lastEvalParams     outcome.CreateEvaluationParams
	lastGetID          uuid.UUID
	lastListEntityType string
	lastListLimit      int
	lastFailedLimit    int
	lastPruneCutoff    time.Time
	// latestOutcome / latestConfigured drive GetLatestForEntity. Left
	// unconfigured (the zero value), GetLatestForEntity returns
	// outcome.ErrNotFound so existing tests exercising CreateOutcome via
	// RecordExecutionResult's "no prior outcome" branch are unaffected by
	// this stub's growth to the full 10-method StoreIface.
	latestOutcome      outcome.Outcome
	latestConfigured   bool
	lastFinalizeID     uuid.UUID
	lastFinalizeParams outcome.CreateOutcomeParams
}

var _ outcome.StoreIface = (*stubOutcomeStore)(nil)

func (s *stubOutcomeStore) CreateOutcome(_ context.Context, p outcome.CreateOutcomeParams) (outcome.Outcome, error) {
	s.lastCreateParams = p
	return s.returnOutcome, s.returnErr
}

func (s *stubOutcomeStore) GetOutcomeByID(_ context.Context, id uuid.UUID, _ *uuid.UUID) (outcome.Outcome, error) {
	s.lastGetID = id
	if s.returnErr != nil {
		return outcome.Outcome{}, s.returnErr
	}
	return s.returnOutcome, nil
}

func (s *stubOutcomeStore) ListRecentOutcomes(_ context.Context, _ *uuid.UUID, et string, limit int) ([]outcome.Outcome, error) {
	s.lastListEntityType = et
	s.lastListLimit = limit
	return s.returnList, s.returnErr
}

func (s *stubOutcomeStore) CreateEvaluation(_ context.Context, p outcome.CreateEvaluationParams) (outcome.Evaluation, error) {
	s.lastEvalParams = p
	return s.returnEval, s.returnErr
}

func (s *stubOutcomeStore) ListEvaluationsByOutcomeID(_ context.Context, _ uuid.UUID, _ *uuid.UUID) ([]outcome.Evaluation, error) {
	return s.returnEvals, s.returnErr
}

func (s *stubOutcomeStore) ListFailedOutcomes(_ context.Context, _ *uuid.UUID, limit int) ([]outcome.Outcome, error) {
	s.lastFailedLimit = limit
	return s.returnList, s.returnErr
}

func (s *stubOutcomeStore) PruneOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	s.lastPruneCutoff = cutoff
	return 0, s.returnErr
}

func (s *stubOutcomeStore) ExistsForEntity(_ context.Context, _ *uuid.UUID, _ string, _ uuid.UUID) (bool, error) {
	return false, s.returnErr
}

func (s *stubOutcomeStore) GetLatestForEntity(_ context.Context, _ *uuid.UUID, _ string, _ uuid.UUID) (outcome.Outcome, error) {
	if !s.latestConfigured {
		return outcome.Outcome{}, outcome.ErrNotFound
	}
	return s.latestOutcome, nil
}

func (s *stubOutcomeStore) FinalizeDraft(_ context.Context, id uuid.UUID, p outcome.CreateOutcomeParams) (outcome.Outcome, error) {
	s.lastFinalizeID = id
	s.lastFinalizeParams = p
	if s.returnErr != nil {
		return outcome.Outcome{}, s.returnErr
	}
	return s.returnOutcome, nil
}

func (s *stubOutcomeStore) SeedDraft(_ context.Context, _ *uuid.UUID, _ string, _ uuid.UUID) (outcome.Outcome, bool, error) {
	if s.returnErr != nil {
		return outcome.Outcome{}, false, s.returnErr
	}
	return s.returnOutcome, true, nil
}

// --- helpers ---

func callRecordOutcome(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleRecordOutcome(context.Background(), req)
	if err != nil {
		t.Fatalf("handleRecordOutcome returned unexpected Go error: %v", err)
	}
	return r
}

func callEvaluateOutcome(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleEvaluateOutcome(context.Background(), req)
	if err != nil {
		t.Fatalf("handleEvaluateOutcome returned unexpected Go error: %v", err)
	}
	return r
}

func callListRecentOutcomes(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleListRecentOutcomes(context.Background(), req)
	if err != nil {
		t.Fatalf("handleListRecentOutcomes returned unexpected Go error: %v", err)
	}
	return r
}

func callFindFailedPatterns(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleFindFailedPatterns(context.Background(), req)
	if err != nil {
		t.Fatalf("handleFindFailedPatterns returned unexpected Go error: %v", err)
	}
	return r
}

func newOutcomeServer(store outcome.StoreIface) *Server {
	return &Server{outcome: store}
}

// --- handleRecordOutcome tests ---

func TestHandleRecordOutcome_HappyPath(t *testing.T) {
	id := uuid.New()
	entityID := uuid.New()
	store := &stubOutcomeStore{
		returnOutcome: outcome.Outcome{
			ID:         id,
			EntityType: entityTypeTask,
			EntityID:   entityID,
			Result:     "success",
		},
	}
	s := newOutcomeServer(store)

	r := callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   entityID.String(),
		"result":      "success",
		"notes":       "all good",
	})

	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if store.lastCreateParams.EntityType != entityTypeTask {
		t.Errorf("EntityType = %q, want %q", store.lastCreateParams.EntityType, entityTypeTask)
	}
	if store.lastCreateParams.Result != "success" {
		t.Errorf("Result = %q, want %q", store.lastCreateParams.Result, "success")
	}
}

func TestHandleRecordOutcome_InvalidEntityType(t *testing.T) {
	s := newOutcomeServer(&stubOutcomeStore{})
	r := callRecordOutcome(t, s, map[string]any{
		"entity_type": "unknown_type",
		"entity_id":   uuid.New().String(),
		"result":      "success",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid entity_type")
	}
}

func TestHandleRecordOutcome_InvalidEntityIDUUID(t *testing.T) {
	s := newOutcomeServer(&stubOutcomeStore{})
	r := callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   "not-a-uuid",
		"result":      "success",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid entity_id UUID")
	}
}

func TestHandleRecordOutcome_InvalidResult(t *testing.T) {
	s := newOutcomeServer(&stubOutcomeStore{})
	r := callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   uuid.New().String(),
		"result":      "awesome", // not in AllowedResults
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid result")
	}
}

// ---------------------------------------------------------------------------
// wbt-2.0 P2.4 — record_outcome session_id linkage
// ---------------------------------------------------------------------------

// TestHandleRecordOutcome_InvalidSessionIDUUID verifies a malformed session_id
// is rejected BEFORE CreateOutcome is ever called (validate UUID format
// first, per backend-security-design.md's no-FK design note: a stale-but-
// well-formed UUID is tolerated, a malformed string is not).
func TestHandleRecordOutcome_InvalidSessionIDUUID(t *testing.T) {
	store := &stubOutcomeStore{}
	s := newOutcomeServer(store)
	r := callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   uuid.New().String(),
		"result":      "success",
		"session_id":  "not-a-uuid",
	})
	if !r.IsError {
		t.Fatal("expected IsError=true for invalid session_id UUID")
	}
	if store.lastCreateParams.EntityType != "" {
		t.Error("CreateOutcome must not be called when session_id is malformed")
	}
}

// TestHandleRecordOutcome_NoSessionID_Regression verifies calls without
// session_id remain fully regression-safe (WorkSessionID stays nil, no
// SetOutcomeLink attempted, no workSession store dependency required).
func TestHandleRecordOutcome_NoSessionID_Regression(t *testing.T) {
	store := &stubOutcomeStore{returnOutcome: outcome.Outcome{ID: uuid.New(), Result: "success"}}
	s := newOutcomeServer(store) // no workSession wired at all
	r := callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   uuid.New().String(),
		"result":      "success",
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	if store.lastCreateParams.WorkSessionID != nil {
		t.Errorf("expected nil WorkSessionID when session_id is absent, got %v", store.lastCreateParams.WorkSessionID)
	}
}

// TestHandleRecordOutcome_WithSessionID_LinksBothDirections verifies a valid
// session_id (1) is stored on the outcome's work_session_id and (2) triggers
// a best-effort SetOutcomeLink call that persists outcome_id back onto the
// work_sessions row — the bidirectional link.
func TestHandleRecordOutcome_WithSessionID_LinksBothDirections(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "record-outcome-link-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	r := callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   uuid.New().String(),
		"result":      "success",
		"session_id":  sessID,
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	var o map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &o); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if o["work_session_id"] != sessID {
		t.Errorf("outcome.work_session_id: got %v, want %s", o["work_session_id"], sessID)
	}

	// Verify the reverse direction: work_sessions.outcome_id got set too.
	sessIDParsed, _ := uuid.Parse(sessID)
	got, err := s.workSession.GetByID(context.Background(), s.workspaceUUIDVal(), sessIDParsed)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	outcomeIDStr, _ := o["id"].(string)
	if got.OutcomeID == nil || got.OutcomeID.String() != outcomeIDStr {
		t.Errorf("SetOutcomeLink did not persist outcome_id on the session: got %v, want %s", got.OutcomeID, outcomeIDStr)
	}
}

// TestHandleRecordOutcome_UnknownSessionID_NonFatal verifies a well-formed
// but unknown session_id still creates the outcome (non-fatal) — SetOutcomeLink
// silently tolerates worksession.ErrNotFound per the no-FK design.
func TestHandleRecordOutcome_UnknownSessionID_NonFatal(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   uuid.New().String(),
		"result":      "success",
		"session_id":  uuid.New().String(), // well-formed, but no such session exists
	})
	if r.IsError {
		t.Fatalf("expected success even with unknown session_id, got error: %s", resultText(r))
	}
}

func TestHandleRecordOutcome_InvalidMetricsJSON(t *testing.T) {
	s := newOutcomeServer(&stubOutcomeStore{})
	r := callRecordOutcome(t, s, map[string]any{
		"entity_type":  "decision",
		"entity_id":    uuid.New().String(),
		"result":       "partial",
		"metrics_json": "{not valid json",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid metrics_json")
	}
}

func TestHandleRecordOutcome_StoreError(t *testing.T) {
	store := &stubOutcomeStore{returnErr: errors.New("db down")}
	s := newOutcomeServer(store)
	r := callRecordOutcome(t, s, map[string]any{
		"entity_type": "sprint",
		"entity_id":   uuid.New().String(),
		"result":      "failure",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true when store returns error")
	}
}

// TestHandleRecordOutcome_StoreError_SanitizesInternalDetails is the
// regression test for the security-review-reproduced leak: a raw SQLite
// driver error naming an internal index (idx_outcomes_one_open_draft) was
// returned verbatim to the MCP caller via mcp.NewToolResultError. The
// MCP-caller-visible message MUST be generic; the original error MUST still
// be recoverable server-side via slog (see tools_outcome.go's
// handleRecordOutcome store-error branch, mirroring tools_arch.go's
// sanitize-then-log pattern).
func TestHandleRecordOutcome_StoreError_SanitizesInternalDetails(t *testing.T) {
	rawErr := errors.New(
		`superseding: sqlite OutcomeStore.CreateOutcome: constraint failed: ` +
			`UNIQUE constraint failed: index 'idx_outcomes_one_open_draft' (2067)`,
	)
	store := &stubOutcomeStore{returnErr: rawErr}
	s := newOutcomeServer(store)

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	r := callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   uuid.New().String(),
		"result":      "failure",
	})

	if !r.IsError {
		t.Fatalf("expected IsError=true when store returns error")
	}
	msg := resultText(r)
	if strings.Contains(msg, "idx_outcomes_one_open_draft") {
		t.Errorf("MCP-caller-visible error leaked the internal index name: %q", msg)
	}
	if strings.Contains(msg, "constraint failed") {
		t.Errorf("MCP-caller-visible error leaked raw driver detail: %q", msg)
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "idx_outcomes_one_open_draft") {
		t.Errorf("expected server-side log to retain the original error detail, got: %q", logged)
	}
}

func TestHandleRecordOutcome_AllEntityTypes(t *testing.T) {
	for _, et := range []string{entityTypeTask, "decision", "sprint", "project"} {
		t.Run(et, func(t *testing.T) {
			store := &stubOutcomeStore{returnOutcome: outcome.Outcome{EntityType: et, Result: "success"}}
			s := newOutcomeServer(store)
			r := callRecordOutcome(t, s, map[string]any{
				"entity_type": et,
				"entity_id":   uuid.New().String(),
				"result":      "success",
			})
			if r.IsError {
				t.Errorf("entity_type %q: unexpected error: %s", et, resultText(r))
			}
		})
	}
}

// --- handleEvaluateOutcome tests ---

func TestHandleEvaluateOutcome_HappyPath(t *testing.T) {
	outcomeID := uuid.New()
	store := &stubOutcomeStore{
		returnOutcome: outcome.Outcome{ID: outcomeID},
		returnEval:    outcome.Evaluation{OutcomeID: outcomeID, Analysis: "root cause found"},
	}
	s := newOutcomeServer(store)

	r := callEvaluateOutcome(t, s, map[string]any{
		"outcome_id":       outcomeID.String(),
		"analysis":         "root cause found",
		"lessons_json":     `["always write tests first"]`,
		"suggestions_json": `["add integration tests"]`,
	})

	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if store.lastEvalParams.Analysis != "root cause found" {
		t.Errorf("Analysis = %q, want %q", store.lastEvalParams.Analysis, "root cause found")
	}
}

func TestHandleEvaluateOutcome_InvalidOutcomeIDUUID(t *testing.T) {
	s := newOutcomeServer(&stubOutcomeStore{})
	r := callEvaluateOutcome(t, s, map[string]any{
		"outcome_id": "bad-uuid",
		"analysis":   "some analysis",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid outcome_id UUID")
	}
}

func TestHandleEvaluateOutcome_EmptyAnalysis(t *testing.T) {
	s := newOutcomeServer(&stubOutcomeStore{})
	r := callEvaluateOutcome(t, s, map[string]any{
		"outcome_id": uuid.New().String(),
		"analysis":   "",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for empty analysis")
	}
}

func TestHandleEvaluateOutcome_OutcomeNotFound(t *testing.T) {
	store := &stubOutcomeStore{returnErr: outcome.ErrNotFound}
	s := newOutcomeServer(store)
	r := callEvaluateOutcome(t, s, map[string]any{
		"outcome_id": uuid.New().String(),
		"analysis":   "some analysis",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true when outcome not found")
	}
}

func TestHandleEvaluateOutcome_InvalidLessonsJSON(t *testing.T) {
	// GetOutcomeByID must succeed first (no error), then lessons_json is validated.
	store := &stubOutcomeStore{returnOutcome: outcome.Outcome{ID: uuid.New()}}
	s := newOutcomeServer(store)
	r := callEvaluateOutcome(t, s, map[string]any{
		"outcome_id":   store.returnOutcome.ID.String(),
		"analysis":     "some analysis",
		"lessons_json": `"not an array"`,
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for non-array lessons_json")
	}
}

func TestHandleEvaluateOutcome_InvalidSuggestionsJSON(t *testing.T) {
	store := &stubOutcomeStore{returnOutcome: outcome.Outcome{ID: uuid.New()}}
	s := newOutcomeServer(store)
	r := callEvaluateOutcome(t, s, map[string]any{
		"outcome_id":       store.returnOutcome.ID.String(),
		"analysis":         "some analysis",
		"suggestions_json": `{"not": "array"}`,
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for non-array suggestions_json")
	}
}

// --- handleListRecentOutcomes tests ---

func TestHandleListRecentOutcomes_HappyPath(t *testing.T) {
	store := &stubOutcomeStore{returnList: []outcome.Outcome{{Result: "success"}, {Result: "failure"}}}
	s := newOutcomeServer(store)

	r := callListRecentOutcomes(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"limit":       float64(5),
	})

	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if store.lastListEntityType != entityTypeTask {
		t.Errorf("lastListEntityType = %q, want %q", store.lastListEntityType, entityTypeTask)
	}
	if store.lastListLimit != 5 {
		t.Errorf("lastListLimit = %d, want 5", store.lastListLimit)
	}
}

func TestHandleListRecentOutcomes_DefaultLimit(t *testing.T) {
	store := &stubOutcomeStore{}
	s := newOutcomeServer(store)

	r := callListRecentOutcomes(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	// Default limit is 20 when not specified.
	if store.lastListLimit != 20 {
		t.Errorf("lastListLimit = %d, want 20 (default)", store.lastListLimit)
	}
}

func TestHandleListRecentOutcomes_LimitCapped(t *testing.T) {
	store := &stubOutcomeStore{}
	s := newOutcomeServer(store)

	r := callListRecentOutcomes(t, s, map[string]any{
		"limit": float64(999),
	})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if store.lastListLimit != maxOutcomeLimit {
		t.Errorf("lastListLimit = %d, want %d (capped at maxOutcomeLimit)", store.lastListLimit, maxOutcomeLimit)
	}
}

func TestHandleListRecentOutcomes_InvalidEntityType(t *testing.T) {
	s := newOutcomeServer(&stubOutcomeStore{})
	r := callListRecentOutcomes(t, s, map[string]any{
		"entity_type": "bogus",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid entity_type")
	}
}

func TestHandleListRecentOutcomes_EmptyResultNilSafe(t *testing.T) {
	// Store returns nil slice — handler must return [] not null.
	store := &stubOutcomeStore{returnList: nil}
	s := newOutcomeServer(store)
	r := callListRecentOutcomes(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	txt := resultText(r)
	// Must be valid JSON array, not "null".
	if txt == nullStr {
		t.Error("response must be [] not null when store returns nil slice")
	}
}

// --- handleFindFailedPatterns tests ---

func TestHandleFindFailedPatterns_HappyPath(t *testing.T) {
	failed := []outcome.Outcome{
		{ID: uuid.New(), Result: "failure"},
		{ID: uuid.New(), Result: "regressed"},
	}
	store := &stubOutcomeStore{
		returnList:  failed,
		returnEvals: []outcome.Evaluation{{Analysis: "root cause"}},
	}
	s := newOutcomeServer(store)

	r := callFindFailedPatterns(t, s, map[string]any{"limit": float64(5)})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if store.lastFailedLimit != 5 {
		t.Errorf("lastFailedLimit = %d, want 5", store.lastFailedLimit)
	}
}

func TestHandleFindFailedPatterns_DefaultLimit(t *testing.T) {
	store := &stubOutcomeStore{}
	s := newOutcomeServer(store)
	r := callFindFailedPatterns(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	// Default limit is 10.
	if store.lastFailedLimit != 10 {
		t.Errorf("lastFailedLimit = %d, want 10 (default)", store.lastFailedLimit)
	}
}

func TestHandleFindFailedPatterns_LimitCapped(t *testing.T) {
	store := &stubOutcomeStore{}
	s := newOutcomeServer(store)
	r := callFindFailedPatterns(t, s, map[string]any{"limit": float64(500)})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if store.lastFailedLimit != maxOutcomeLimit {
		t.Errorf("lastFailedLimit = %d, want %d (capped)", store.lastFailedLimit, maxOutcomeLimit)
	}
}

func TestHandleFindFailedPatterns_EvalFetchErrorDegradeGracefully(t *testing.T) {
	// ListFailedOutcomes succeeds but ListEvaluationsByOutcomeID returns error.
	// The handler must degrade gracefully: return outcomes with empty evals, not abort.
	failed := []outcome.Outcome{{ID: uuid.New(), Result: "failure"}}
	// Use a custom store that fails only on eval fetch (not on ListFailedOutcomes).
	degradeStore := &evalFailingStore{
		outcomes:  failed,
		evalError: errors.New("eval fetch failed"),
	}
	s := newOutcomeServer(degradeStore)

	r := callFindFailedPatterns(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("expected graceful degradation, got error: %s", resultText(r))
	}
}

// evalFailingStore is a specialised stub where ListFailedOutcomes succeeds
// but ListEvaluationsByOutcomeID always returns an error — used to test the
// graceful degradation path in handleFindFailedPatterns.
type evalFailingStore struct {
	outcomes  []outcome.Outcome
	evalError error
}

var _ outcome.StoreIface = (*evalFailingStore)(nil)

func (e *evalFailingStore) CreateOutcome(_ context.Context, _ outcome.CreateOutcomeParams) (outcome.Outcome, error) {
	return outcome.Outcome{}, nil
}

func (e *evalFailingStore) GetOutcomeByID(_ context.Context, _ uuid.UUID, _ *uuid.UUID) (outcome.Outcome, error) {
	return outcome.Outcome{}, nil
}

func (e *evalFailingStore) ListRecentOutcomes(_ context.Context, _ *uuid.UUID, _ string, _ int) ([]outcome.Outcome, error) {
	return nil, nil
}

func (e *evalFailingStore) CreateEvaluation(_ context.Context, _ outcome.CreateEvaluationParams) (outcome.Evaluation, error) {
	return outcome.Evaluation{}, nil
}

func (e *evalFailingStore) ListEvaluationsByOutcomeID(_ context.Context, _ uuid.UUID, _ *uuid.UUID) ([]outcome.Evaluation, error) {
	return nil, e.evalError
}

func (e *evalFailingStore) ListFailedOutcomes(_ context.Context, _ *uuid.UUID, _ int) ([]outcome.Outcome, error) {
	return e.outcomes, nil
}

func (e *evalFailingStore) PruneOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

func (e *evalFailingStore) ExistsForEntity(_ context.Context, _ *uuid.UUID, _ string, _ uuid.UUID) (bool, error) {
	return false, nil
}

func (e *evalFailingStore) GetLatestForEntity(_ context.Context, _ *uuid.UUID, _ string, _ uuid.UUID) (outcome.Outcome, error) {
	return outcome.Outcome{}, outcome.ErrNotFound
}

func (e *evalFailingStore) FinalizeDraft(_ context.Context, id uuid.UUID, _ outcome.CreateOutcomeParams) (outcome.Outcome, error) {
	return outcome.Outcome{ID: id}, nil
}

func (e *evalFailingStore) SeedDraft(_ context.Context, _ *uuid.UUID, _ string, _ uuid.UUID) (outcome.Outcome, bool, error) {
	return outcome.Outcome{ID: uuid.New()}, true, nil
}

// --- isJSONArray / validateJSONArrayArg unit tests ---

func TestIsJSONArray(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{`[]`, true},
		{`["a","b"]`, true},
		{`{}`, false},
		{`"string"`, false},
		{`123`, false},
		{`not json`, false},
		{`[1,2,3]`, true},
	}
	for _, tc := range cases {
		got := isJSONArray(tc.input)
		if got != tc.want {
			t.Errorf("isJSONArray(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestValidateJSONArrayArg_EmptyAllowed(t *testing.T) {
	b, err := validateJSONArrayArg(map[string]any{}, "lessons_json")
	if err != nil {
		t.Fatalf("unexpected error for empty arg: %v", err)
	}
	if b != nil {
		t.Errorf("expected nil for empty arg, got %s", b)
	}
}

func TestValidateJSONArrayArg_ValidArray(t *testing.T) {
	b, err := validateJSONArrayArg(map[string]any{"k": `["x","y"]`}, "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(b) != `["x","y"]` {
		t.Errorf("got %s, want [\"x\",\"y\"]", b)
	}
}

func TestValidateJSONArrayArg_ObjectRejected(t *testing.T) {
	_, err := validateJSONArrayArg(map[string]any{"k": `{"a":1}`}, "k")
	if err == nil {
		t.Fatal("expected error for JSON object, got nil")
	}
}

// --- parseRelatedRuleIDs unit tests (MAJOR-2: upper bound guard) ---

func TestParseRelatedRuleIDs_Empty(t *testing.T) {
	ids, err := parseRelatedRuleIDs("")
	if err != nil {
		t.Fatalf("unexpected error for empty input: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 IDs, got %d", len(ids))
	}
}

func TestParseRelatedRuleIDs_EmptyArray(t *testing.T) {
	ids, err := parseRelatedRuleIDs("[]")
	if err != nil {
		t.Fatalf("unexpected error for empty array: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 IDs, got %d", len(ids))
	}
}

func TestParseRelatedRuleIDs_ValidUUIDs(t *testing.T) {
	id1, id2 := uuid.New(), uuid.New()
	raw := `["` + id1.String() + `","` + id2.String() + `"]`
	ids, err := parseRelatedRuleIDs(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 IDs, got %d", len(ids))
	}
}

func TestParseRelatedRuleIDs_InvalidUUID(t *testing.T) {
	_, err := parseRelatedRuleIDs(`["not-a-uuid"]`)
	if err == nil {
		t.Fatal("expected error for invalid UUID, got nil")
	}
}

func TestParseRelatedRuleIDs_ExceedsMax(t *testing.T) {
	// Build an array with maxRelatedRuleIDs+1 UUIDs.
	ids := make([]string, maxRelatedRuleIDs+1)
	for i := range ids {
		ids[i] = `"` + uuid.New().String() + `"`
	}
	raw := "[" + strings.Join(ids, ",") + "]"
	_, err := parseRelatedRuleIDs(raw)
	if err == nil {
		t.Fatalf("expected error when exceeding maxRelatedRuleIDs=%d, got nil", maxRelatedRuleIDs)
	}
	if !strings.Contains(err.Error(), "maximum is") {
		t.Errorf("error should mention maximum, got: %v", err)
	}
}

func TestParseRelatedRuleIDs_ExactlyMax(t *testing.T) {
	// Exactly maxRelatedRuleIDs should be accepted.
	ids := make([]string, maxRelatedRuleIDs)
	for i := range ids {
		ids[i] = `"` + uuid.New().String() + `"`
	}
	raw := "[" + strings.Join(ids, ",") + "]"
	parsed, err := parseRelatedRuleIDs(raw)
	if err != nil {
		t.Fatalf("unexpected error at exactly max (%d): %v", maxRelatedRuleIDs, err)
	}
	if len(parsed) != maxRelatedRuleIDs {
		t.Errorf("expected %d IDs, got %d", maxRelatedRuleIDs, len(parsed))
	}
}

func TestHandleRecordOutcome_RelatedRuleIDsExceedsMax(t *testing.T) {
	// Ensure the MCP handler rejects related_rule_ids arrays > maxRelatedRuleIDs.
	s := newOutcomeServer(&stubOutcomeStore{})
	ids := make([]string, maxRelatedRuleIDs+1)
	for i := range ids {
		ids[i] = `"` + uuid.New().String() + `"`
	}
	r := callRecordOutcome(t, s, map[string]any{
		"entity_type":      entityTypeTask,
		"entity_id":        uuid.New().String(),
		"result":           "success",
		"related_rule_ids": "[" + strings.Join(ids, ",") + "]",
	})
	if !r.IsError {
		t.Fatal("expected IsError=true when related_rule_ids exceeds max, got false")
	}
	if !strings.Contains(resultText(r), "maximum is") {
		t.Errorf("error should mention maximum, got: %s", resultText(r))
	}
}
