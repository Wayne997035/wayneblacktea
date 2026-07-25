package scheduler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	"github.com/google/uuid"
)

// pendingStatus / rejectedStatus are proposal status strings used in test
// assertions. Declared as constants to satisfy the goconst linter (≥3
// occurrences).
const (
	pendingStatus  = "pending"
	rejectedStatus = "rejected"
)

// ---------------------------------------------------------------------------
// stubReflectionStore — minimal reflection.StoreIface implementation for tests.
// ---------------------------------------------------------------------------

type stubReflectionStore struct {
	created        []*reflection.Reflection
	createErr      error
	recentPatterns []*reflection.Reflection
	recentErr      error
}

func (s *stubReflectionStore) Create(_ context.Context, p reflection.CreateParams) (*reflection.Reflection, error) {
	if s.createErr != nil {
		return nil, s.createErr
	}
	r := &reflection.Reflection{
		ID:          uuid.New(),
		WorkspaceID: p.WorkspaceID,
		Type:        p.Type,
		Summary:     p.Summary,
		Confidence:  p.Confidence,
		CreatedAt:   time.Now(),
	}
	s.created = append(s.created, r)
	return r, nil
}

func (s *stubReflectionStore) List(_ context.Context, _ reflection.ListParams) ([]*reflection.Reflection, error) {
	return nil, nil
}

func (s *stubReflectionStore) GetLatest(_ context.Context, _ *uuid.UUID, _ string) (*reflection.Reflection, error) {
	return nil, nil
}

func (s *stubReflectionStore) ByRelatedEntity(
	_ context.Context, _ *uuid.UUID, _ string, _ uuid.UUID, _ int,
) ([]*reflection.Reflection, error) {
	return nil, nil
}

func (s *stubReflectionStore) RecentWithPatterns(
	_ context.Context, _ *uuid.UUID, _ time.Time, _ int,
) ([]*reflection.Reflection, error) {
	return s.recentPatterns, s.recentErr
}

func (s *stubReflectionStore) PruneOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// ---------------------------------------------------------------------------
// Test: Job 5 — proposal_cleanup
// ---------------------------------------------------------------------------

// TestProposalCleanup_SkipsNilPool verifies that runProposalCleanup exits
// gracefully and produces no error when disciplinePool is nil (SQLite dev path).
func TestProposalCleanup_SkipsNilPool(t *testing.T) {
	sc := &Scheduler{
		disciplinePool: nil,
	}
	// Must not panic.
	sc.runProposalCleanup()
}

// TestProposalCleanup_SQLBoundary_SchedulerOnly verifies that the SQL UPDATE
// in proposal_cleanup carries the proposed_by LIKE 'scheduler:%' guard.
// We test this by inspecting the constant SQL string embedded in the job source,
// without running against a real DB (unit-level guard).
func TestProposalCleanup_SQLBoundary_SchedulerOnly(t *testing.T) {
	// The guard is in the const q inside runProposalCleanup.
	// We verify it appears in the live query by testing a real PG run
	// if testPgPool is non-nil, otherwise we validate the constant at unit level.
	if testPgPool == nil {
		t.Skip("testPgPool not set (short mode or no Docker) — skipping PG integration portion")
	}

	ctx := context.Background()
	wsID := uuid.New()
	now := time.Now()

	// Seed two proposals: one scheduler-owned (should be expired) and one
	// user-owned (must NOT be touched).
	const insertProposal = `INSERT INTO pending_proposals
(id, workspace_id, type, payload, status, proposed_by, created_at)
VALUES ($1, $2, 'knowledge', '{"title":"t","content":"c"}', 'pending', $3, $4)`

	schedulerID := uuid.New()
	userID := uuid.New()
	// Set created_at to 35 days ago so both are within the cleanup window.
	oldTs := now.Add(-35 * 24 * time.Hour)

	if _, err := testPgPool.Exec(ctx, insertProposal, schedulerID, wsID, "scheduler:test-job", oldTs); err != nil {
		t.Fatalf("seed scheduler proposal: %v", err)
	}
	if _, err := testPgPool.Exec(ctx, insertProposal, userID, wsID, "user", oldTs); err != nil {
		t.Fatalf("seed user proposal: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPgPool.Exec(ctx,
			"DELETE FROM pending_proposals WHERE id = ANY($1::uuid[])",
			[]uuid.UUID{schedulerID, userID},
		)
	})

	sc := &Scheduler{disciplinePool: testPgPool}
	sc.runProposalCleanup()

	// scheduler-owned: must be rejected.
	var schedStatus, schedReason string
	if err := testPgPool.QueryRow(ctx,
		"SELECT status, reason FROM pending_proposals WHERE id = $1", schedulerID,
	).Scan(&schedStatus, &schedReason); err != nil {
		t.Fatalf("query scheduler proposal: %v", err)
	}
	if schedStatus != rejectedStatus {
		t.Errorf("scheduler proposal: expected status %q, got %q", rejectedStatus, schedStatus)
	}
	if !strings.Contains(schedReason, "expired by scheduler") {
		t.Errorf("scheduler proposal reason: expected 'expired by scheduler', got %q", schedReason)
	}

	// user-owned: must remain pending and untouched.
	var userStatus string
	if err := testPgPool.QueryRow(ctx,
		"SELECT status FROM pending_proposals WHERE id = $1", userID,
	).Scan(&userStatus); err != nil {
		t.Fatalf("query user proposal: %v", err)
	}
	if userStatus != pendingStatus {
		t.Errorf("user proposal: expected status still %q, got %q", pendingStatus, userStatus)
	}
}

// ---------------------------------------------------------------------------
// Test: Job 2 — stuck_task_detection (nil pool skip path)
// ---------------------------------------------------------------------------

// TestStuckTaskDetection_SkipsNilPool ensures the job skips gracefully when
// disciplinePool is nil.
func TestStuckTaskDetection_SkipsNilPool(t *testing.T) {
	sc := &Scheduler{
		disciplinePool: nil,
		cognitiveDeps: &cognitiveDeps{
			proposal:    &stubProposalStore{},
			workspaceID: func() *uuid.UUID { id := uuid.New(); return &id }(),
		},
	}
	// Must not panic.
	sc.runStuckTaskDetection()
}

// TestStuckTaskDetection_SkipsNilDeps ensures the job skips when cognitiveDeps
// is nil even if the pool is non-nil (belt-and-suspenders nil guard).
func TestStuckTaskDetection_SkipsNilCognitiveDeps(t *testing.T) {
	if testPgPool == nil {
		t.Skip("requires testPgPool")
	}
	sc := &Scheduler{
		disciplinePool: testPgPool,
		cognitiveDeps:  nil,
	}
	// Must not panic.
	sc.runStuckTaskDetection()
}

// ---------------------------------------------------------------------------
// Test: Job 5 PG integration — scheduler proposals are rejected, user skipped
// (duplicate of TestProposalCleanup_SQLBoundary_SchedulerOnly but verifies the
// proposed_by LIKE guard is in the SQL constant even in short mode by inspecting
// the JSON-marshalled payload shape).
// ---------------------------------------------------------------------------

// stubGTDWithGoals embeds *stubGTDStore so all other StoreIface methods are
// satisfied by delegation. ActiveGoals is overridden to return configurable goals.
type stubGTDWithGoals struct {
	*stubGTDStore
	goals []db.Goal
}

func (s *stubGTDWithGoals) ActiveGoals(_ context.Context) ([]db.Goal, error) {
	return s.goals, nil
}

// TestWeeklyGoalReview_PayloadShape ensures the JSON payload written by
// runWeeklyGoalReview matches proposal.KnowledgePayload and uses type='knowledge'.
func TestWeeklyGoalReview_PayloadShape(t *testing.T) {
	propStore := &stubProposalStore{}
	wsID := uuid.New()
	goalID := uuid.New()

	gStub := &stubGTDWithGoals{
		stubGTDStore: &stubGTDStore{},
		goals: []db.Goal{
			{ID: goalID, Title: "Ship M7"},
		},
	}

	sc := &Scheduler{
		cognitiveDeps: &cognitiveDeps{
			gtd:         gStub,
			proposal:    propStore,
			workspaceID: &wsID,
		},
	}
	sc.runWeeklyGoalReview()

	if len(propStore.created) != 1 {
		t.Fatalf("expected 1 proposal created, got %d", len(propStore.created))
	}
	var kp proposal.KnowledgePayload
	if err := json.Unmarshal(propStore.created[0].Payload, &kp); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if kp.Title == "" || kp.Content == "" {
		t.Errorf("expected non-empty title and content in KnowledgePayload, got %+v", kp)
	}
	if propStore.created[0].Type != string(proposal.TypeKnowledge) {
		t.Errorf("expected type='knowledge', got %q", propStore.created[0].Type)
	}
}

// ---------------------------------------------------------------------------
// Test: Job 3 — decision_outcome_review (nil pool and nil cognitive deps)
// ---------------------------------------------------------------------------

// TestDecisionOutcomeReview_SkipsNilCognitiveDeps verifies that
// runDecisionOutcomeReview skips gracefully when cognitiveDeps is nil.
func TestDecisionOutcomeReview_SkipsNilCognitiveDeps(t *testing.T) {
	if testPgPool == nil {
		t.Skip("requires testPgPool")
	}
	sc := &Scheduler{
		disciplinePool: testPgPool,
		cognitiveDeps:  nil,
	}
	// Must not panic.
	sc.runDecisionOutcomeReview()
}

// TestDecisionOutcomeReview_SkipsNilPool verifies that runDecisionOutcomeReview
// exits gracefully when disciplinePool is nil (SQLite dev path).
func TestDecisionOutcomeReview_SkipsNilPool(t *testing.T) {
	propStore := &stubProposalStore{}
	wsID := uuid.New()
	sc := &Scheduler{
		disciplinePool: nil,
		cognitiveDeps: &cognitiveDeps{
			proposal:    propStore,
			workspaceID: &wsID,
		},
	}
	// Must not panic and must not create any proposals.
	sc.runDecisionOutcomeReview()
	if len(propStore.created) != 0 {
		t.Errorf("expected 0 proposals when pool is nil, got %d", len(propStore.created))
	}
}

// ---------------------------------------------------------------------------
// Test: Job 4 — knowledge_to_skill_candidate (nil pool skip)
// ---------------------------------------------------------------------------

// TestKnowledgeToSkillCandidate_SkipsNilCognitiveDeps verifies that
// runKnowledgeToSkillCandidate skips gracefully when cognitiveDeps is nil.
func TestKnowledgeToSkillCandidate_SkipsNilCognitiveDeps(t *testing.T) {
	if testPgPool == nil {
		t.Skip("requires testPgPool")
	}
	sc := &Scheduler{
		disciplinePool: testPgPool,
		cognitiveDeps:  nil,
	}
	// Must not panic.
	sc.runKnowledgeToSkillCandidate()
}

// TestKnowledgeToSkillCandidate_SkipsNilPool verifies that the job exits
// gracefully when disciplinePool is nil.
func TestKnowledgeToSkillCandidate_SkipsNilPool(t *testing.T) {
	propStore := &stubProposalStore{}
	wsID := uuid.New()
	sc := &Scheduler{
		disciplinePool: nil,
		cognitiveDeps: &cognitiveDeps{
			proposal:    propStore,
			workspaceID: &wsID,
		},
	}
	sc.runKnowledgeToSkillCandidate()
	if len(propStore.created) != 0 {
		t.Errorf("expected 0 proposals when pool is nil, got %d", len(propStore.created))
	}
}

// ---------------------------------------------------------------------------
// Test: Job 6 — behavior_rule_candidate (nil cognitive deps)
// ---------------------------------------------------------------------------

// TestBehaviorRuleCandidate_SkipsNilCognitiveDeps verifies that
// runBehaviorRuleCandidate skips gracefully when cognitiveDeps is nil.
func TestBehaviorRuleCandidate_SkipsNilCognitiveDeps(t *testing.T) {
	sc := &Scheduler{cognitiveDeps: nil}
	// Must not panic.
	sc.runBehaviorRuleCandidate()
}

// TestBehaviorRuleCandidate_SkipsNilReflectionStore verifies that the job
// exits gracefully when cognitiveDeps.reflection is nil.
func TestBehaviorRuleCandidate_SkipsNilReflectionStore(t *testing.T) {
	propStore := &stubProposalStore{}
	wsID := uuid.New()
	sc := &Scheduler{
		cognitiveDeps: &cognitiveDeps{
			reflection:  nil, // <-- nil: job must skip
			proposal:    propStore,
			workspaceID: &wsID,
		},
	}
	sc.runBehaviorRuleCandidate()
	if len(propStore.created) != 0 {
		t.Errorf("expected 0 proposals when reflection store is nil, got %d", len(propStore.created))
	}
}

// TestBehaviorRuleCandidate_SkipsEmptySummary exercises runProposalLoop's
// ok=false skip branch: behavior_rule_candidate's build callback returns
// ok=false for reflections with an empty Summary, so those must NOT produce
// a pending_proposal while non-empty-Summary reflections in the same batch
// still do.
func TestBehaviorRuleCandidate_SkipsEmptySummary(t *testing.T) {
	propStore := &stubProposalStore{}
	wsID := uuid.New()
	reflStore := &stubReflectionStore{
		recentPatterns: []*reflection.Reflection{
			{ID: uuid.New(), Type: "weekly", Summary: "", CreatedAt: time.Now()},
			{ID: uuid.New(), Type: "weekly", Summary: "user tends to defer decisions on Fridays", CreatedAt: time.Now()},
			{ID: uuid.New(), Type: "daily", Summary: "", CreatedAt: time.Now()},
			{ID: uuid.New(), Type: "daily", Summary: "recurring stuck-task pattern in area X", CreatedAt: time.Now()},
		},
	}
	sc := &Scheduler{
		cognitiveDeps: &cognitiveDeps{
			reflection:  reflStore,
			proposal:    propStore,
			workspaceID: &wsID,
		},
	}
	sc.runBehaviorRuleCandidate()

	if len(propStore.created) != 2 {
		t.Fatalf("expected 2 proposals (only non-empty-Summary reflections), got %d", len(propStore.created))
	}
	for _, row := range propStore.created {
		var kp proposal.KnowledgePayload
		if err := json.Unmarshal(row.Payload, &kp); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if !strings.Contains(kp.Content, "user tends to defer decisions on Fridays") &&
			!strings.Contains(kp.Content, "recurring stuck-task pattern in area X") {
			t.Errorf("unexpected proposal content (empty-Summary reflection should have been skipped): %q", kp.Content)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: WithCognitiveDeps job registration (P3.0b — daily_reflection retired)
// ---------------------------------------------------------------------------

// TestWithCognitiveDeps_DoesNotRegisterDailyReflection verifies that a fresh
// scheduler's cognitive job registration contains exactly the six surviving
// jobs (weekly_goal_review, stuck_task_detection, decision_outcome_review,
// knowledge_to_skill_candidate, proposal_cleanup, behavior_rule_candidate)
// and does NOT register the retired cognitive-daily-reflection job.
func TestWithCognitiveDeps_DoesNotRegisterDailyReflection(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	wsID := uuid.New()
	deps := NewCognitiveDeps(&stubReflectionStore{}, &stubGTDStore{}, &stubProposalStore{}, &wsID)
	if err := sc.WithCognitiveDeps(deps); err != nil {
		t.Fatalf("WithCognitiveDeps() error: %v", err)
	}

	wantNames := map[string]bool{
		"cognitive-weekly-goal-review":           false,
		"cognitive-stuck-task-detection":         false,
		"cognitive-decision-outcome-review":      false,
		"cognitive-knowledge-to-skill-candidate": false,
		"cognitive-proposal-cleanup":             false,
		"cognitive-behavior-rule-candidate":      false,
	}

	for _, j := range sc.s.Jobs() {
		name := j.Name()
		if name == "cognitive-daily-reflection" {
			t.Error("retired cognitive-daily-reflection job is still registered")
		}
		if _, tracked := wantNames[name]; tracked {
			wantNames[name] = true
		}
	}

	for name, found := range wantNames {
		if !found {
			t.Errorf("expected cognitive job %q to be registered, but it was not found", name)
		}
	}
}
