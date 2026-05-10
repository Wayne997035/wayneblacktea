package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/discipline"
)

// stubDisciplineStore is an in-memory test double for discipline.Store. It
// supports the two read methods used by EvaluateDisciplineDrift; Insert is a
// no-op (drift evaluation never inserts).
type stubDisciplineStore struct {
	mutating  []discipline.Event
	decisions map[string][]time.Time

	// Optional: trigger errors to verify the swallow behaviour.
	mutatingErr  error
	decisionsErr error
}

func (s *stubDisciplineStore) Insert(_ context.Context, _ discipline.InsertParams) error {
	return nil
}

func (s *stubDisciplineStore) RecentMutating(_ context.Context, since time.Time, limit int) ([]discipline.Event, error) {
	if s.mutatingErr != nil {
		return nil, s.mutatingErr
	}
	var out []discipline.Event
	for _, ev := range s.mutating {
		if ev.ObservedAt.Before(since) {
			continue
		}
		out = append(out, ev)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *stubDisciplineStore) RecentDecisionTimes(_ context.Context, sessionID string, since time.Time) ([]time.Time, error) {
	if s.decisionsErr != nil {
		return nil, s.decisionsErr
	}
	var out []time.Time
	for _, t := range s.decisions[sessionID] {
		if t.Before(since) {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// Compile-time guarantee that the stub satisfies the interface.
var _ discipline.Store = (*stubDisciplineStore)(nil)

// TestEvaluateDisciplineDrift_MutatingWithoutDecision: a mutating call with
// no preceding decision in the same session is drift.
func TestEvaluateDisciplineDrift_MutatingWithoutDecision(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	mutAt := now.Add(-1 * time.Hour)

	store := &stubDisciplineStore{
		mutating: []discipline.Event{
			{SessionID: "s1", ToolName: "add_task", ObservedAt: mutAt, IsMutating: true},
		},
	}
	got := EvaluateDisciplineDrift(context.Background(), store, now)
	if got.DriftCount24h != 1 {
		t.Fatalf("DriftCount24h: want 1, got %d", got.DriftCount24h)
	}
	if len(got.RecentDrifts) != 1 || got.RecentDrifts[0].ToolName != "add_task" {
		t.Errorf("RecentDrifts: %+v", got.RecentDrifts)
	}
}

// TestEvaluateDisciplineDrift_MutatingWithRecentDecision: a mutating call
// preceded by a log_decision in the same session within driftWindow does
// NOT count as drift.
func TestEvaluateDisciplineDrift_MutatingWithRecentDecision(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	mutAt := now.Add(-1 * time.Hour)
	decAt := mutAt.Add(-5 * time.Minute) // 5 min before, inside 15-min window

	store := &stubDisciplineStore{
		mutating: []discipline.Event{
			{SessionID: "s1", ToolName: "complete_task", ObservedAt: mutAt, IsMutating: true},
		},
		decisions: map[string][]time.Time{
			"s1": {decAt},
		},
	}
	got := EvaluateDisciplineDrift(context.Background(), store, now)
	if got.DriftCount24h != 0 {
		t.Errorf("DriftCount24h: want 0 (covered by decision), got %d", got.DriftCount24h)
	}
}

// TestEvaluateDisciplineDrift_DecisionOutsideWindow: a decision more than
// driftWindow before the mutating call does NOT cover it.
func TestEvaluateDisciplineDrift_DecisionOutsideWindow(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	mutAt := now.Add(-1 * time.Hour)
	// 16 minutes before — just outside the 15-minute window
	decAt := mutAt.Add(-16 * time.Minute)

	store := &stubDisciplineStore{
		mutating: []discipline.Event{
			{SessionID: "s1", ToolName: "log_decision", ObservedAt: mutAt, IsMutating: true},
		},
		decisions: map[string][]time.Time{
			"s1": {decAt},
		},
	}
	got := EvaluateDisciplineDrift(context.Background(), store, now)
	if got.DriftCount24h != 1 {
		t.Errorf("DriftCount24h: want 1 (decision outside window), got %d", got.DriftCount24h)
	}
}

// TestEvaluateDisciplineDrift_DecisionAfterMutation: a decision logged AFTER
// the mutating call does NOT cover it (decisions must precede).
func TestEvaluateDisciplineDrift_DecisionAfterMutation(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	mutAt := now.Add(-1 * time.Hour)
	decAt := mutAt.Add(2 * time.Minute) // AFTER the mutation

	store := &stubDisciplineStore{
		mutating: []discipline.Event{
			{SessionID: "s1", ToolName: "add_task", ObservedAt: mutAt, IsMutating: true},
		},
		decisions: map[string][]time.Time{
			"s1": {decAt},
		},
	}
	got := EvaluateDisciplineDrift(context.Background(), store, now)
	if got.DriftCount24h != 1 {
		t.Errorf("DriftCount24h: want 1 (decision after mutation does not cover), got %d", got.DriftCount24h)
	}
}

// TestEvaluateDisciplineDrift_BackToBackMutationsSingleDecision: documents
// the policy choice — a single log_decision covers ALL mutating calls
// within the next 15 minutes, not just the next one. Calls beyond the
// window are drift.
func TestEvaluateDisciplineDrift_BackToBackMutationsSingleDecision(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	decAt := now.Add(-2 * time.Hour)
	// Three mutations: at +1m, +5m, +20m relative to the decision.
	mut1 := decAt.Add(1 * time.Minute)
	mut2 := decAt.Add(5 * time.Minute)
	mut3 := decAt.Add(20 * time.Minute) // outside window

	store := &stubDisciplineStore{
		mutating: []discipline.Event{
			{SessionID: "s1", ToolName: "add_task", ObservedAt: mut1, IsMutating: true},
			{SessionID: "s1", ToolName: "update_task", ObservedAt: mut2, IsMutating: true},
			{SessionID: "s1", ToolName: "complete_task", ObservedAt: mut3, IsMutating: true},
		},
		decisions: map[string][]time.Time{
			"s1": {decAt},
		},
	}
	got := EvaluateDisciplineDrift(context.Background(), store, now)
	if got.DriftCount24h != 1 {
		t.Errorf("DriftCount24h: want 1 (only mut3 outside window), got %d", got.DriftCount24h)
	}
	if len(got.RecentDrifts) != 1 || got.RecentDrifts[0].ToolName != "complete_task" {
		t.Errorf("RecentDrifts: want 1×complete_task, got %+v", got.RecentDrifts)
	}
}

// TestEvaluateDisciplineDrift_DecisionInDifferentSession: a decision in
// session A does NOT cover a mutation in session B (per-session scope).
func TestEvaluateDisciplineDrift_DecisionInDifferentSession(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	mutAt := now.Add(-1 * time.Hour)

	store := &stubDisciplineStore{
		mutating: []discipline.Event{
			{SessionID: "session-B", ToolName: "add_task", ObservedAt: mutAt, IsMutating: true},
		},
		decisions: map[string][]time.Time{
			"session-A": {mutAt.Add(-1 * time.Minute)},
		},
	}
	got := EvaluateDisciplineDrift(context.Background(), store, now)
	if got.DriftCount24h != 1 {
		t.Errorf("DriftCount24h: want 1 (decision in other session), got %d", got.DriftCount24h)
	}
}

// TestEvaluateDisciplineDrift_NilStore: a nil discipline store yields a
// zero-value health snapshot. Verifies the explicit nil-guard.
func TestEvaluateDisciplineDrift_NilStore(t *testing.T) {
	got := EvaluateDisciplineDrift(context.Background(), nil, time.Now())
	if got.DriftCount24h != 0 {
		t.Errorf("DriftCount24h: want 0 with nil store, got %d", got.DriftCount24h)
	}
	if len(got.RecentDrifts) != 0 {
		t.Errorf("RecentDrifts: want empty, got %+v", got.RecentDrifts)
	}
}

// TestEvaluateDisciplineDrift_SampleCap: only the first
// maxDisciplineSamples drifts populate RecentDrifts even when the count is
// higher.
func TestEvaluateDisciplineDrift_SampleCap(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	store := &stubDisciplineStore{}
	for i := 0; i < maxDisciplineSamples+3; i++ {
		store.mutating = append(store.mutating, discipline.Event{
			SessionID:  "s1",
			ToolName:   "add_task",
			ObservedAt: now.Add(-time.Duration(i) * time.Minute),
			IsMutating: true,
		})
	}
	got := EvaluateDisciplineDrift(context.Background(), store, now)
	if got.DriftCount24h != maxDisciplineSamples+3 {
		t.Errorf("DriftCount24h: want %d, got %d", maxDisciplineSamples+3, got.DriftCount24h)
	}
	if len(got.RecentDrifts) != maxDisciplineSamples {
		t.Errorf("RecentDrifts len: want %d, got %d", maxDisciplineSamples, len(got.RecentDrifts))
	}
}

// TestEvaluateDisciplineDrift_StoreErrorSwallowed: a failing store yields a
// zero-value snapshot rather than blowing up. The signal is missing rather
// than the whole system_health response failing.
func TestEvaluateDisciplineDrift_StoreErrorSwallowed(t *testing.T) {
	store := &stubDisciplineStore{
		mutatingErr: context.DeadlineExceeded,
	}
	got := EvaluateDisciplineDrift(context.Background(), store, time.Now())
	if got.DriftCount24h != 0 {
		t.Errorf("DriftCount24h: want 0 on store err, got %d", got.DriftCount24h)
	}
}

// TestHasDecisionInWindow exercises the window-membership helper directly.
func TestHasDecisionInWindow(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name      string
		mutAt     time.Time
		decisions []time.Time
		want      bool
	}{
		{
			name:      "no decisions",
			mutAt:     now,
			decisions: nil,
			want:      false,
		},
		{
			name:      "decision exactly at lower bound",
			mutAt:     now,
			decisions: []time.Time{now.Add(-driftWindow)},
			want:      true,
		},
		{
			name:      "decision one second before lower bound",
			mutAt:     now,
			decisions: []time.Time{now.Add(-driftWindow - time.Second)},
			want:      false,
		},
		{
			name:      "decision after the mutation",
			mutAt:     now,
			decisions: []time.Time{now.Add(time.Minute)},
			want:      false,
		},
		{
			name:      "decision exactly at observedAt counts",
			mutAt:     now,
			decisions: []time.Time{now},
			want:      true,
		},
		{
			name:      "multiple decisions, only one inside window",
			mutAt:     now,
			decisions: []time.Time{now.Add(-2 * time.Hour), now.Add(-1 * time.Minute)},
			want:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := hasDecisionInWindow(tc.mutAt, tc.decisions)
			if got != tc.want {
				t.Errorf("hasDecisionInWindow: want %v, got %v", tc.want, got)
			}
		})
	}
}

// TestMutatingTools_ContainsExpectedSet verifies the canonical mutating-tool
// allowlist matches the spec (Lead-frozen list).
func TestMutatingTools_ContainsExpectedSet(t *testing.T) {
	mustBeMutating := []string{
		"add_task", "update_task", "complete_task", "delete_task",
		"confirm_plan", "confirm_proposal", "confirm_proposals",
		"log_decision",
		"add_knowledge", "add_procedural", "add_vision_item",
		"update_vision_item", "promote_vision_to_task",
		"create_concept", "submit_review",
		"set_session_handoff", "resolve_handoff",
		"start_work", "finish_work", "checkpoint_work",
		"propose_goal", "propose_project", "create_goal", "create_project",
		"mark_procedural_used", "sync_repo",
		"upsert_project_arch", "update_project_status",
	}
	for _, tool := range mustBeMutating {
		if !discipline.IsMutating(tool) {
			t.Errorf("expected %q to be mutating", tool)
		}
	}

	// Read-only tools MUST NOT be in the set.
	mustBeReadOnly := []string{"list_tasks", "get_today_context", "search_knowledge", "system_health"}
	for _, tool := range mustBeReadOnly {
		if discipline.IsMutating(tool) {
			t.Errorf("expected %q to be read-only", tool)
		}
	}
}
