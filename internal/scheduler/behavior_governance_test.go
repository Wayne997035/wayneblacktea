package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// stub outcome.StoreIface
// ---------------------------------------------------------------------------

type stubOutcomeStore struct {
	recentOutcomes []outcome.Outcome
	listRecentErr  error
}

var _ outcome.StoreIface = (*stubOutcomeStore)(nil)

func (s *stubOutcomeStore) CreateOutcome(_ context.Context, _ outcome.CreateOutcomeParams) (outcome.Outcome, error) {
	return outcome.Outcome{}, nil
}

func (s *stubOutcomeStore) GetOutcomeByID(_ context.Context, _ uuid.UUID, _ *uuid.UUID) (outcome.Outcome, error) {
	return outcome.Outcome{}, outcome.ErrNotFound
}

func (s *stubOutcomeStore) ListRecentOutcomes(_ context.Context, _ *uuid.UUID, _ string, _ int) ([]outcome.Outcome, error) {
	return s.recentOutcomes, s.listRecentErr
}

func (s *stubOutcomeStore) CreateEvaluation(_ context.Context, _ outcome.CreateEvaluationParams) (outcome.Evaluation, error) {
	return outcome.Evaluation{}, nil
}

func (s *stubOutcomeStore) ListEvaluationsByOutcomeID(_ context.Context, _ uuid.UUID, _ *uuid.UUID) ([]outcome.Evaluation, error) {
	return nil, nil
}

func (s *stubOutcomeStore) ListFailedOutcomes(_ context.Context, _ *uuid.UUID, _ int) ([]outcome.Outcome, error) {
	return nil, nil
}

func (s *stubOutcomeStore) PruneOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

func (s *stubOutcomeStore) ExistsForEntity(_ context.Context, _ *uuid.UUID, _ string, _ uuid.UUID) (bool, error) {
	return false, nil
}

// GetLatestForEntity / FinalizeDraft / SeedDraft: added mechanically to keep
// this stub satisfying outcome.StoreIface after migration 000074 / decision
// 80c1e8ae (arch-r2 A13, outcome lifecycle convergence) extended the
// interface. This package's own tests never exercise these three methods.
func (s *stubOutcomeStore) GetLatestForEntity(_ context.Context, _ *uuid.UUID, _ string, _ uuid.UUID) (outcome.Outcome, error) {
	return outcome.Outcome{}, outcome.ErrNotFound
}

func (s *stubOutcomeStore) FinalizeDraft(_ context.Context, id uuid.UUID, _ outcome.CreateOutcomeParams) (outcome.Outcome, error) {
	return outcome.Outcome{ID: id}, nil
}

func (s *stubOutcomeStore) SeedDraft(_ context.Context, _ *uuid.UUID, _ string, _ uuid.UUID) (outcome.Outcome, bool, error) {
	return outcome.Outcome{}, true, nil
}

// ---------------------------------------------------------------------------
// stub behaviorrule.StoreIface
// ---------------------------------------------------------------------------

type stubBehaviorRuleStore struct {
	applyOutcomeCalls []applyOutcomeCall
	applyOutcomeErr   error
	listRules         []*behaviorrule.BehaviorRule
	listErr           error
	deprecateCalls    []uuid.UUID
	deprecateErr      error
}

type applyOutcomeCall struct {
	ruleID  uuid.UUID
	outcome string
}

var _ behaviorrule.StoreIface = (*stubBehaviorRuleStore)(nil)

func (s *stubBehaviorRuleStore) Propose(_ context.Context, _ behaviorrule.CreateParams) (*behaviorrule.BehaviorRule, error) {
	return nil, nil
}

func (s *stubBehaviorRuleStore) List(_ context.Context, _ behaviorrule.ListParams) ([]*behaviorrule.BehaviorRule, error) {
	return s.listRules, s.listErr
}

func (s *stubBehaviorRuleStore) ApplyOutcome(_ context.Context, id uuid.UUID, out string) (*behaviorrule.BehaviorRule, error) {
	s.applyOutcomeCalls = append(s.applyOutcomeCalls, applyOutcomeCall{ruleID: id, outcome: out})
	if s.applyOutcomeErr != nil {
		return nil, s.applyOutcomeErr
	}
	return &behaviorrule.BehaviorRule{ID: id, Status: "active", Confidence: 0.55}, nil
}

func (s *stubBehaviorRuleStore) Deprecate(_ context.Context, id uuid.UUID) (*behaviorrule.BehaviorRule, error) {
	s.deprecateCalls = append(s.deprecateCalls, id)
	if s.deprecateErr != nil {
		return nil, s.deprecateErr
	}
	return &behaviorrule.BehaviorRule{ID: id, Status: "deprecated"}, nil
}

func (s *stubBehaviorRuleStore) PruneOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func recentOutcomeWithRules(result string, ruleIDs []uuid.UUID) outcome.Outcome {
	return outcome.Outcome{
		ID:             uuid.New(),
		EntityType:     "task",
		EntityID:       uuid.New(),
		Result:         result,
		RelatedRuleIDs: ruleIDs,
		CreatedAt:      time.Now().Add(-1 * time.Hour), // 1 hour ago = within 7-day window
	}
}

func activeRuleOlderThan(days int, confidence float64) *behaviorrule.BehaviorRule {
	return &behaviorrule.BehaviorRule{
		ID:         uuid.New(),
		Status:     "active",
		Confidence: confidence,
		CreatedAt:  time.Now().AddDate(0, 0, -days),
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestRunBehaviorGovernance_ResultMapping verifies that success/failure/regressed
// map correctly to ApplyOutcome calls, and partial/unknown are skipped.
func TestRunBehaviorGovernance_ResultMapping(t *testing.T) {
	ruleID := uuid.New()

	tests := []struct {
		name           string
		result         string
		wantApplyCalls int
		wantOutcome    string
	}{
		{"success maps to success", outcomeResultSuccess, 1, outcomeResultSuccess},
		{"failure maps to failure", "failure", 1, "failure"},
		{"regressed maps to failure", "regressed", 1, "failure"},
		{"partial is skipped", "partial", 0, ""},
		{"unknown is skipped", "unknown", 0, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := recentOutcomeWithRules(tc.result, []uuid.UUID{ruleID})
			brStore := &stubBehaviorRuleStore{}
			deps := governanceDeps{
				outcomeStore:      &stubOutcomeStore{recentOutcomes: []outcome.Outcome{o}},
				behaviorRuleStore: brStore,
				workspaceID:       nil,
			}
			runBehaviorGovernance(deps)

			if len(brStore.applyOutcomeCalls) != tc.wantApplyCalls {
				t.Errorf("expected %d ApplyOutcome calls, got %d", tc.wantApplyCalls, len(brStore.applyOutcomeCalls))
			}
			if tc.wantApplyCalls > 0 && brStore.applyOutcomeCalls[0].outcome != tc.wantOutcome {
				t.Errorf("expected outcome=%q, got %q", tc.wantOutcome, brStore.applyOutcomeCalls[0].outcome)
			}
		})
	}
}

// TestRunBehaviorGovernance_PerRuleCallCount verifies that an outcome with
// multiple linked rules triggers one ApplyOutcome call per rule.
func TestRunBehaviorGovernance_PerRuleCallCount(t *testing.T) {
	rule1, rule2, rule3 := uuid.New(), uuid.New(), uuid.New()
	o := recentOutcomeWithRules(outcomeResultSuccess, []uuid.UUID{rule1, rule2, rule3})

	brStore := &stubBehaviorRuleStore{}
	deps := governanceDeps{
		outcomeStore:      &stubOutcomeStore{recentOutcomes: []outcome.Outcome{o}},
		behaviorRuleStore: brStore,
		workspaceID:       nil,
	}
	runBehaviorGovernance(deps)

	if len(brStore.applyOutcomeCalls) != 3 {
		t.Errorf("expected 3 ApplyOutcome calls, got %d", len(brStore.applyOutcomeCalls))
	}
}

// TestRunBehaviorGovernance_ErrNotFoundTolerated verifies that ErrNotFound
// from ApplyOutcome is tolerated (stale link) without stopping the run.
func TestRunBehaviorGovernance_ErrNotFoundTolerated(t *testing.T) {
	o := recentOutcomeWithRules(outcomeResultSuccess, []uuid.UUID{uuid.New()})

	brStore := &stubBehaviorRuleStore{applyOutcomeErr: behaviorrule.ErrNotFound}
	deps := governanceDeps{
		outcomeStore:      &stubOutcomeStore{recentOutcomes: []outcome.Outcome{o}},
		behaviorRuleStore: brStore,
		workspaceID:       nil,
	}
	// Must not panic and should log a warn.
	runBehaviorGovernance(deps)
}

// TestRunBehaviorGovernance_AutoDeprecateLoConfidenceAndAge verifies that
// low-confidence active rules older than 30 days are deprecated.
func TestRunBehaviorGovernance_AutoDeprecateLoConfidenceAndAge(t *testing.T) {
	staleLow := activeRuleOlderThan(31, 0.05)  // eligible: age >= 30d AND confidence < 0.10
	staleHigh := activeRuleOlderThan(31, 0.50) // skip: confidence OK
	recentLow := activeRuleOlderThan(10, 0.05) // skip: too young

	brStore := &stubBehaviorRuleStore{
		listRules: []*behaviorrule.BehaviorRule{staleLow, staleHigh, recentLow},
	}
	deps := governanceDeps{
		outcomeStore:      &stubOutcomeStore{},
		behaviorRuleStore: brStore,
		workspaceID:       nil,
	}
	runBehaviorGovernance(deps)

	if len(brStore.deprecateCalls) != 1 {
		t.Errorf("expected 1 Deprecate call, got %d", len(brStore.deprecateCalls))
	}
	if brStore.deprecateCalls[0] != staleLow.ID {
		t.Errorf("deprecated wrong rule: got %v, want %v", brStore.deprecateCalls[0], staleLow.ID)
	}
}

// TestRunBehaviorGovernance_OldOutcomeSkipped verifies that outcomes older
// than 7 days are not used for confidence updates.
func TestRunBehaviorGovernance_OldOutcomeSkipped(t *testing.T) {
	ruleID := uuid.New()
	old := outcome.Outcome{
		ID:             uuid.New(),
		Result:         outcomeResultSuccess,
		RelatedRuleIDs: []uuid.UUID{ruleID},
		// 8 days ago — outside the 7-day lookback window.
		CreatedAt: time.Now().AddDate(0, 0, -8),
	}

	brStore := &stubBehaviorRuleStore{}
	deps := governanceDeps{
		outcomeStore:      &stubOutcomeStore{recentOutcomes: []outcome.Outcome{old}},
		behaviorRuleStore: brStore,
		workspaceID:       nil,
	}
	runBehaviorGovernance(deps)

	if len(brStore.applyOutcomeCalls) != 0 {
		t.Errorf("expected 0 ApplyOutcome calls for old outcome, got %d", len(brStore.applyOutcomeCalls))
	}
}

// TestRunBehaviorGovernance_DeprecateErrNotFoundTolerated verifies that
// ErrNotFound from Deprecate is tolerated (race or already deprecated).
func TestRunBehaviorGovernance_DeprecateErrNotFoundTolerated(t *testing.T) {
	staleLow := activeRuleOlderThan(31, 0.05)
	brStore := &stubBehaviorRuleStore{
		listRules:    []*behaviorrule.BehaviorRule{staleLow},
		deprecateErr: behaviorrule.ErrNotFound,
	}
	deps := governanceDeps{
		outcomeStore:      &stubOutcomeStore{},
		behaviorRuleStore: brStore,
		workspaceID:       nil,
	}
	// Must not panic.
	runBehaviorGovernance(deps)
}

// ---------------------------------------------------------------------------
// Supersession filter (GTD 80cf80b6, PR #152 2nd-army finding): a superseded
// outcome row must not double-vote alongside the row that supersedes it.
// ---------------------------------------------------------------------------

// TestRunBehaviorGovernance_SupersededOutcome_VotesOnce is the primary
// regression test: two outcome rows for the same entity — an older row
// superseded by a newer one, both inside the 7-day lookback window — must
// cast exactly ONE ApplyOutcome vote (from the newer, non-superseded row),
// not two.
func TestRunBehaviorGovernance_SupersededOutcome_VotesOnce(t *testing.T) {
	ruleID := uuid.New()
	oldID := uuid.New()

	old := outcome.Outcome{
		ID:             oldID,
		Result:         "failure",
		RelatedRuleIDs: []uuid.UUID{ruleID},
		CreatedAt:      time.Now().Add(-2 * time.Hour),
	}
	newer := outcome.Outcome{
		ID:             uuid.New(),
		Result:         outcomeResultSuccess,
		RelatedRuleIDs: []uuid.UUID{ruleID},
		SupersedesID:   &oldID,
		CreatedAt:      time.Now().Add(-1 * time.Hour),
	}

	brStore := &stubBehaviorRuleStore{}
	deps := governanceDeps{
		outcomeStore:      &stubOutcomeStore{recentOutcomes: []outcome.Outcome{old, newer}},
		behaviorRuleStore: brStore,
		workspaceID:       nil,
	}
	runBehaviorGovernance(deps)

	if len(brStore.applyOutcomeCalls) != 1 {
		t.Fatalf("expected exactly 1 ApplyOutcome call (superseded row must not vote), got %d: %+v",
			len(brStore.applyOutcomeCalls), brStore.applyOutcomeCalls)
	}
	if brStore.applyOutcomeCalls[0].outcome != outcomeResultSuccess {
		t.Errorf("expected the vote to come from the newer (non-superseded) row, got outcome=%q",
			brStore.applyOutcomeCalls[0].outcome)
	}
}

// TestRunBehaviorGovernance_NonSupersededOutcome_StillVotesOnce is the
// over-filtering guard the threat model calls out explicitly: a single,
// ordinary (non-superseded) outcome row must still cast exactly one vote.
// If the supersession filter were implemented wrong (e.g. treating every row
// with a nil SupersedesID as "superseded", or matching on the wrong field),
// this test would catch a fail-open regression where rules silently stop
// being scored.
func TestRunBehaviorGovernance_NonSupersededOutcome_StillVotesOnce(t *testing.T) {
	o := recentOutcomeWithRules(outcomeResultSuccess, []uuid.UUID{uuid.New()})
	// Explicitly confirm the fixture has no supersession relationship, since
	// this test's entire point is the "ordinary row" baseline.
	if o.SupersedesID != nil {
		t.Fatalf("test fixture bug: recentOutcomeWithRules must not set SupersedesID")
	}

	brStore := &stubBehaviorRuleStore{}
	deps := governanceDeps{
		outcomeStore:      &stubOutcomeStore{recentOutcomes: []outcome.Outcome{o}},
		behaviorRuleStore: brStore,
		workspaceID:       nil,
	}
	runBehaviorGovernance(deps)

	if len(brStore.applyOutcomeCalls) != 1 {
		t.Errorf("expected exactly 1 ApplyOutcome call for a single non-superseded row, got %d",
			len(brStore.applyOutcomeCalls))
	}
}

// TestRunBehaviorGovernance_SupersededOutsideWindow_SuccessorAloneVotes
// verifies chained supersession (A superseded by B superseded by C, all
// present in the same fetch): only C — the row nothing else supersedes —
// casts a vote.
func TestRunBehaviorGovernance_SupersededOutsideWindow_SuccessorAloneVotes(t *testing.T) {
	ruleID := uuid.New()
	aID, bID := uuid.New(), uuid.New()

	a := outcome.Outcome{
		ID: aID, Result: "failure", RelatedRuleIDs: []uuid.UUID{ruleID},
		CreatedAt: time.Now().Add(-3 * time.Hour),
	}
	b := outcome.Outcome{
		ID: bID, Result: "failure", RelatedRuleIDs: []uuid.UUID{ruleID}, SupersedesID: &aID,
		CreatedAt: time.Now().Add(-2 * time.Hour),
	}
	c := outcome.Outcome{
		ID: uuid.New(), Result: outcomeResultSuccess, RelatedRuleIDs: []uuid.UUID{ruleID}, SupersedesID: &bID,
		CreatedAt: time.Now().Add(-1 * time.Hour),
	}

	brStore := &stubBehaviorRuleStore{}
	deps := governanceDeps{
		outcomeStore:      &stubOutcomeStore{recentOutcomes: []outcome.Outcome{a, b, c}},
		behaviorRuleStore: brStore,
		workspaceID:       nil,
	}
	runBehaviorGovernance(deps)

	if len(brStore.applyOutcomeCalls) != 1 {
		t.Fatalf("expected exactly 1 ApplyOutcome call (only the un-superseded tail votes), got %d: %+v",
			len(brStore.applyOutcomeCalls), brStore.applyOutcomeCalls)
	}
	if brStore.applyOutcomeCalls[0].outcome != outcomeResultSuccess {
		t.Errorf("expected the vote to come from C (the final row in the chain), got outcome=%q",
			brStore.applyOutcomeCalls[0].outcome)
	}
}

// TestRunBehaviorGovernance_ListRecentError verifies graceful handling of
// ListRecentOutcomes error (logs warn, returns without panic).
func TestRunBehaviorGovernance_ListRecentError(t *testing.T) {
	brStore := &stubBehaviorRuleStore{}
	deps := governanceDeps{
		outcomeStore:      &stubOutcomeStore{listRecentErr: errors.New("db down")},
		behaviorRuleStore: brStore,
		workspaceID:       nil,
	}
	runBehaviorGovernance(deps)
	if len(brStore.applyOutcomeCalls) != 0 {
		t.Errorf("expected 0 ApplyOutcome calls when ListRecent errors, got %d", len(brStore.applyOutcomeCalls))
	}
}
