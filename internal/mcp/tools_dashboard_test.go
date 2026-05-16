package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
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
	dbPath := filepath.Join(t.TempDir(), "dashboard-test.db")
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
