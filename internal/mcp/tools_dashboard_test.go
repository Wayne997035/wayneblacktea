package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// fakeDashCandidateStore is a stub completionCandidateStore for dashboard tool tests.
type fakeDashCandidateStore struct {
	detected []completioncandidate.Candidate
	pending  []completioncandidate.Candidate
}

func (f *fakeDashCandidateStore) DetectAndUpsert(
	_ context.Context, _ completioncandidate.DetectParams,
) ([]completioncandidate.Candidate, error) {
	return f.detected, nil
}

func (f *fakeDashCandidateStore) ListPendingCandidates(
	_ context.Context, _ *uuid.UUID,
) ([]completioncandidate.Candidate, error) {
	return f.pending, nil
}

// Compile-time check: fakeDashCandidateStore satisfies completionCandidateStore.
var _ completionCandidateStore = (*fakeDashCandidateStore)(nil)

// newTestServerWithCandidates constructs a minimal Server for dashboard tests.
func newTestServerWithCandidates(t *testing.T, store completionCandidateStore) *Server {
	t.Helper()
	dbPath := newMigratedSQLitePath(t, "dashboard-test.db")
	stores, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{
		Backend:    storage.BackendSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("NewServerStores: %v", err)
	}
	t.Cleanup(func() { _ = stores.Close() })

	srv, err := New(stores)
	if err != nil {
		t.Fatalf("mcp.New: %v", err)
	}
	srv.WithCompletionCandidates(store)
	return srv
}

// TestDetectCompletionCandidates_DefaultParams verifies that the tool returns
// valid JSON with candidates key when the store returns 0 candidates.
func TestDetectCompletionCandidates_DefaultParams(t *testing.T) {
	t.Parallel()
	store := &fakeDashCandidateStore{
		detected: []completioncandidate.Candidate{},
		pending:  []completioncandidate.Candidate{},
	}
	s := newTestServerWithCandidates(t, store)

	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := s.handleDetectCompletionCandidates(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDetectCompletionCandidates: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	text := resultText(result)
	var body map[string]any
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatalf("unmarshal result: %v\ntext: %s", err, text)
	}
	if _, ok := body["candidates"]; !ok {
		t.Error("result JSON missing 'candidates' key")
	}
	if _, ok := body["count"]; !ok {
		t.Error("result JSON missing 'count' key")
	}
}

// TestReconcileDashboard_Summary verifies that the reconcile_dashboard tool
// returns a summary string that contains the candidate count.
func TestReconcileDashboard_Summary(t *testing.T) {
	t.Parallel()
	taskID1 := uuid.New()
	taskID2 := uuid.New()
	candidates := []completioncandidate.Candidate{
		{
			ID:           uuid.New(),
			TaskID:       taskID1,
			Reason:       completioncandidate.ReasonStaleInProgress,
			Confidence:   completioncandidate.ConfidenceMedium,
			Status:       completioncandidate.StatusPending,
			DetectedAt:   time.Now().UTC(),
			EvidenceRefs: []string{"stale since yesterday"},
		},
		{
			ID:           uuid.New(),
			TaskID:       taskID2,
			Reason:       completioncandidate.ReasonFinishWorkGap,
			Confidence:   completioncandidate.ConfidenceHigh,
			Status:       completioncandidate.StatusPending,
			DetectedAt:   time.Now().UTC(),
			EvidenceRefs: []string{"work_session completed"},
		},
	}

	store := &fakeDashCandidateStore{
		detected: candidates,
		pending:  candidates,
	}
	s := newTestServerWithCandidates(t, store)

	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := s.handleReconcileDashboard(context.Background(), req)
	if err != nil {
		t.Fatalf("handleReconcileDashboard: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	text := resultText(result)
	var body map[string]any
	if err := json.Unmarshal([]byte(text), &body); err != nil {
		t.Fatalf("unmarshal result: %v\ntext: %s", err, text)
	}

	summary, _ := body["summary"].(string)
	if summary == "" {
		t.Error("expected non-empty 'summary' field")
	}

	detectedCount, _ := body["detected_count"].(float64)
	if int(detectedCount) != 2 {
		t.Errorf("detected_count: got %v, want 2", detectedCount)
	}
}

// TestDetectCompletionCandidates_CustomParams verifies custom params are accepted.
func TestDetectCompletionCandidates_CustomParams(t *testing.T) {
	t.Parallel()
	store := &fakeDashCandidateStore{
		detected: []completioncandidate.Candidate{},
		pending:  []completioncandidate.Candidate{},
	}
	s := newTestServerWithCandidates(t, store)

	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"stale_threshold_hours": float64(48),
		"lookback_days":         float64(14),
	}

	result, err := s.handleDetectCompletionCandidates(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDetectCompletionCandidates: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Errorf("expected success, got error result: %s", resultText(result))
	}
}

// ---- F0929-62 real-store effect tests (H3: DeliberatelyExcludedTools -> MutatingTools) ----
//
// The effect tests below use a fake candidate store; the real SQLite
// detection rules are tested in internal/completioncandidate.

// toolReconcileDashboard / actionDashboardReconciled name the tool and the
// activity_log action it writes on success (middleware_autolog.go). Local
// constants (goconst) since both effect tests below reference each twice.
const (
	toolReconcileDashboard    = "reconcile_dashboard"
	actionDashboardReconciled = "dashboard:reconciled"
)

// TestDetectCompletionCandidates_NilStore_NoWrite is F0929-62's negative
// acceptance row: s.completionCandidates == nil must error with
// candidateStoreUnconfigured and attempt no write.
func TestDetectCompletionCandidates_NilStore_NoWrite(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t) // no WithCompletionCandidates call
	req := mcpmsg.CallToolRequest{}
	result, err := s.handleDetectCompletionCandidates(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDetectCompletionCandidates: %v", err)
	}
	if !result.IsError || resultText(result) != candidateStoreUnconfigured {
		t.Errorf("got %q (IsError=%v), want %q", resultText(result), result.IsError, candidateStoreUnconfigured)
	}
}

// TestReconcileDashboard_WritesExactlyOneActivityLogRow is F0929-62's
// reconcile_dashboard effect test: after reclassification to MutatingTools,
// the tool's activity_log write behavior must be provably unchanged —
// exactly one new row, action="dashboard:reconciled", via autoLogEntry
// (async, so this goes through the real autoLogMiddleware — the same "sync
// test seam" other autoLogEntry tests use, see middleware_autolog_test.go —
// and polls the real GTD store, not raw SQL). Uses fakeDashCandidateStore
// (no real completion_candidates SQL).
func TestReconcileDashboard_WritesExactlyOneActivityLogRow(t *testing.T) {
	t.Parallel()
	store := &fakeDashCandidateStore{
		detected: []completioncandidate.Candidate{}, pending: []completioncandidate.Candidate{},
	}
	s := newTestServerWithCandidates(t, store)
	since := time.Now().Add(-time.Minute)

	mw := s.autoLogMiddleware()
	wrapped := mw(s.handleReconcileDashboard)
	req := mcpmsg.CallToolRequest{}
	req.Params.Name = toolReconcileDashboard
	res, err := wrapped(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile_dashboard: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", resultText(res))
	}

	// autoLogEntry fires asynchronously — poll via the real GTD store.
	deadline := time.Now().Add(2 * time.Second)
	var reconciled int
	for time.Now().Before(deadline) {
		logs, lerr := s.gtd.ListActivityLogsSince(context.Background(), since, 10)
		if lerr != nil {
			t.Fatalf("ListActivityLogsSince: %v", lerr)
		}
		reconciled = 0
		for _, l := range logs {
			if l.Action == actionDashboardReconciled {
				reconciled++
			}
		}
		if reconciled >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if reconciled != 1 {
		t.Errorf("activity_log rows with action=dashboard:reconciled = %d, want exactly 1", reconciled)
	}
}

// TestReconcileDashboard_NilStore_NoActivityLogWrite is F0929-62's negative
// acceptance row: s.completionCandidates == nil must error with
// candidateStoreUnconfigured, and — because autoLogEntry only fires on
// !res.IsError — zero new activity_log rows in this failure case.
func TestReconcileDashboard_NilStore_NoActivityLogWrite(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t) // no WithCompletionCandidates call
	since := time.Now().Add(-time.Minute)

	mw := s.autoLogMiddleware()
	wrapped := mw(s.handleReconcileDashboard)

	req := mcpmsg.CallToolRequest{}
	req.Params.Name = toolReconcileDashboard
	res, err := wrapped(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile_dashboard: %v", err)
	}
	if !res.IsError || resultText(res) != candidateStoreUnconfigured {
		t.Errorf("got %q (IsError=%v), want %q", resultText(res), res.IsError, candidateStoreUnconfigured)
	}
	time.Sleep(50 * time.Millisecond) // give any errant goroutine a moment
	logs, err := s.gtd.ListActivityLogsSince(context.Background(), since, 10)
	if err != nil {
		t.Fatalf("ListActivityLogsSince: %v", err)
	}
	for _, l := range logs {
		if l.Action == actionDashboardReconciled {
			t.Errorf("unexpected activity_log row on failure: %+v", l)
		}
	}
}

// TestReconcileDashboardRoundTrip_CandidateTaskIDFeedsCompleteTask is
// F0929-62's D4 round-trip #2 (Risk flags, HIGH must-test): the workflow
// prompt (prompts.go:132-149) instructs a client to take
// candidateSummary.TaskID from detect_completion_candidates' response and
// pass it straight into complete_task's task_id argument. This task didn't
// rename any field, but the round-trip itself must stay provably intact.
// Uses fakeDashCandidateStore (not the real completioncandidate.SQLiteStore)
// so the round-trip's WIRING (field name / value threading through to a
// real task completion) is proven independently of the real store's
// detection SQL, which is tested in internal/completioncandidate.
func TestReconcileDashboardRoundTrip_CandidateTaskIDFeedsCompleteTask(t *testing.T) {
	t.Parallel()
	store := &fakeDashCandidateStore{}
	s := newTestServerWithCandidates(t, store)
	// complete_task is a seam-wrapped handler (tools_gtd.go) — its toolSpec
	// must be registered (via MCPServer()'s addTool calls) before callTool's
	// seam() can validate/decode args; newTestServerWithCandidates doesn't do
	// this by default since most tests in this file call handlers directly.
	s.MCPServer()

	seeded, err := s.gtd.CreateTask(context.Background(), gtd.CreateTaskParams{
		Title: "round-trip task " + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	store.detected = []completioncandidate.Candidate{{
		ID: uuid.New(), TaskID: seeded.ID, Reason: completioncandidate.ReasonStaleInProgress,
		Confidence: completioncandidate.ConfidenceMedium, Status: completioncandidate.StatusPending,
		DetectedAt: time.Now().UTC(), EvidenceRefs: []string{"stale"},
	}}

	req := mcpmsg.CallToolRequest{}
	result, err := s.handleDetectCompletionCandidates(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDetectCompletionCandidates: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(result))
	}
	var body struct {
		Candidates []struct {
			TaskID string `json:"task_id"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(resultText(result)), &body); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, resultText(result))
	}
	if len(body.Candidates) != 1 || body.Candidates[0].TaskID != seeded.ID.String() {
		t.Fatalf("expected exactly 1 candidate for the seeded task, got: %+v", body.Candidates)
	}

	// The round-trip: candidate's task_id fed straight into complete_task,
	// exactly as prompts.go's handlePromptReconcileDashboard instructs.
	completeRes := callTool(t, s, "complete_task", map[string]any{
		"task_id": body.Candidates[0].TaskID, "artifact": "https://github.com/o/r/pull/1",
	}, s.handleCompleteTask)
	if completeRes.IsError {
		t.Fatalf("complete_task must succeed on a round-tripped candidate task_id, got: %s", resultText(completeRes))
	}
	got, err := s.gtd.GetTaskByID(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("status = %q, want completed", got.Status)
	}

	// Negative case: a task_id that does not exist must resolve through
	// complete_task's existing not-found error, unchanged.
	missingRes := callTool(t, s, "complete_task", map[string]any{
		"task_id": uuid.New().String(), "artifact": "https://github.com/o/r/pull/2",
	}, s.handleCompleteTask)
	if !missingRes.IsError {
		t.Fatalf("complete_task on a nonexistent task_id must error, got: %s", resultText(missingRes))
	}
}
