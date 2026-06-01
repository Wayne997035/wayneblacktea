package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

func openBehaviorRuleDB(t *testing.T) *wbtsqlite.DB {
	t.Helper()
	db, err := wbtsqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSQLiteBehaviorRuleStore_Propose(t *testing.T) {
	db := openBehaviorRuleDB(t)
	store := wbtsqlite.NewBehaviorRuleStore(db)
	ctx := context.Background()

	wsID := uuid.New()
	srcID := uuid.New()

	r, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "when backlog exceeds 50 tasks",
		Action:      "schedule a review session",
		SourceType:  "manual",
		SourceID:    &srcID,
		Confidence:  0.75,
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if r.Status != "proposed" {
		t.Errorf("expected status 'proposed', got %q", r.Status)
	}
	if r.Condition != "when backlog exceeds 50 tasks" {
		t.Errorf("unexpected condition: %q", r.Condition)
	}
	if r.Confidence < 0.749 || r.Confidence > 0.751 {
		t.Errorf("expected confidence ~0.75, got %v", r.Confidence)
	}
	if r.SourceID == nil || *r.SourceID != srcID {
		t.Errorf("unexpected source_id: %v", r.SourceID)
	}
}

func TestSQLiteBehaviorRuleStore_ApplyOutcome_SuccessOnProposed(t *testing.T) {
	db := openBehaviorRuleDB(t)
	store := wbtsqlite.NewBehaviorRuleStore(db)
	ctx := context.Background()

	wsID := uuid.New()
	r, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "condition P",
		Action:      "action P",
		SourceType:  "reflection",
		Confidence:  0.50,
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	updated, err := store.ApplyOutcome(ctx, r.ID, "success")
	if err != nil {
		t.Fatalf("ApplyOutcome success: %v", err)
	}
	if updated.Status != statusActive {
		t.Errorf("expected status 'active' after success on proposed, got %q", updated.Status)
	}
	// 0.50 + 0.05 = 0.55
	if updated.Confidence < 0.549 || updated.Confidence > 0.551 {
		t.Errorf("expected confidence ~0.55, got %v", updated.Confidence)
	}
}

func TestSQLiteBehaviorRuleStore_ApplyOutcome_SuccessOnActive(t *testing.T) {
	db := openBehaviorRuleDB(t)
	store := wbtsqlite.NewBehaviorRuleStore(db)
	ctx := context.Background()

	wsID := uuid.New()
	r, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "condition Q",
		Action:      "action Q",
		SourceType:  "outcome",
		Confidence:  0.60,
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	// Promote to active
	active, err := store.ApplyOutcome(ctx, r.ID, "success")
	if err != nil {
		t.Fatalf("first ApplyOutcome: %v", err)
	}
	if active.Status != statusActive {
		t.Fatalf("expected active, got %q", active.Status)
	}

	// Second success: confidence only
	updated, err := store.ApplyOutcome(ctx, r.ID, "success")
	if err != nil {
		t.Fatalf("second ApplyOutcome: %v", err)
	}
	if updated.Status != statusActive {
		t.Errorf("status should remain 'active', got %q", updated.Status)
	}
	// 0.60 + 0.05 + 0.05 = 0.70
	if updated.Confidence < 0.699 || updated.Confidence > 0.701 {
		t.Errorf("expected confidence ~0.70, got %v", updated.Confidence)
	}
}

func TestSQLiteBehaviorRuleStore_ApplyOutcome_Failure(t *testing.T) {
	db := openBehaviorRuleDB(t)
	store := wbtsqlite.NewBehaviorRuleStore(db)
	ctx := context.Background()

	wsID := uuid.New()
	r, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "condition R",
		Action:      "action R",
		SourceType:  "manual",
		Confidence:  0.50,
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	updated, err := store.ApplyOutcome(ctx, r.ID, "failure")
	if err != nil {
		t.Fatalf("ApplyOutcome failure: %v", err)
	}
	if updated.Status != "proposed" {
		t.Errorf("status should not change on failure; got %q", updated.Status)
	}
	// 0.50 - 0.10 = 0.40
	if updated.Confidence < 0.399 || updated.Confidence > 0.401 {
		t.Errorf("expected confidence ~0.40, got %v", updated.Confidence)
	}
}

func TestSQLiteBehaviorRuleStore_ApplyOutcome_ConfidenceCap(t *testing.T) {
	db := openBehaviorRuleDB(t)
	store := wbtsqlite.NewBehaviorRuleStore(db)
	ctx := context.Background()

	wsID := uuid.New()
	r, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "cap condition",
		Action:      "cap action",
		SourceType:  "manual",
		Confidence:  0.98,
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	updated, err := store.ApplyOutcome(ctx, r.ID, "success")
	if err != nil {
		t.Fatalf("ApplyOutcome: %v", err)
	}
	if updated.Confidence > 1.00 {
		t.Errorf("confidence exceeded 1.00: got %v", updated.Confidence)
	}
}

func TestSQLiteBehaviorRuleStore_ApplyOutcome_ConfidenceFloor(t *testing.T) {
	db := openBehaviorRuleDB(t)
	store := wbtsqlite.NewBehaviorRuleStore(db)
	ctx := context.Background()

	wsID := uuid.New()
	r, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "floor condition",
		Action:      "floor action",
		SourceType:  "manual",
		Confidence:  0.05,
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	updated, err := store.ApplyOutcome(ctx, r.ID, "failure")
	if err != nil {
		t.Fatalf("ApplyOutcome: %v", err)
	}
	if updated.Confidence < 0.00 {
		t.Errorf("confidence went below 0.00: got %v", updated.Confidence)
	}
}

func TestSQLiteBehaviorRuleStore_Deprecate(t *testing.T) {
	db := openBehaviorRuleDB(t)
	store := wbtsqlite.NewBehaviorRuleStore(db)
	ctx := context.Background()

	wsID := uuid.New()
	r, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "to be deprecated",
		Action:      "old action",
		SourceType:  "manual",
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	updated, err := store.Deprecate(ctx, r.ID)
	if err != nil {
		t.Fatalf("Deprecate: %v", err)
	}
	if updated.Status != "deprecated" {
		t.Errorf("expected 'deprecated', got %q", updated.Status)
	}

	// Idempotent second Deprecate
	_, err = store.Deprecate(ctx, r.ID)
	if err != nil {
		t.Errorf("second Deprecate returned unexpected error: %v", err)
	}
}

func TestSQLiteBehaviorRuleStore_PruneOlderThan(t *testing.T) {
	db := openBehaviorRuleDB(t)
	store := wbtsqlite.NewBehaviorRuleStore(db)
	ctx := context.Background()

	wsID := uuid.New()

	// Create and deprecate one rule
	dep, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "to be pruned",
		Action:      "old",
		SourceType:  "manual",
	})
	if err != nil {
		t.Fatalf("Propose dep: %v", err)
	}
	if _, err := store.Deprecate(ctx, dep.ID); err != nil {
		t.Fatalf("Deprecate: %v", err)
	}

	// Create and promote one rule to active
	active, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "active rule",
		Action:      "live",
		SourceType:  "manual",
	})
	if err != nil {
		t.Fatalf("Propose active: %v", err)
	}
	if _, err := store.ApplyOutcome(ctx, active.ID, "success"); err != nil {
		t.Fatalf("promote to active: %v", err)
	}

	// Create one rule that stays proposed
	proposed, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "still proposed",
		Action:      "pending",
		SourceType:  "manual",
	})
	if err != nil {
		t.Fatalf("Propose proposed: %v", err)
	}

	// Prune with a future cutoff
	cutoff := time.Now().Add(1 * time.Minute)
	n, err := store.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if n < 1 {
		t.Errorf("expected at least 1 row deleted, got %d", n)
	}

	// deprecated row should be gone
	_, err = store.GetByID(ctx, dep.ID)
	if !errors.Is(err, behaviorrule.ErrNotFound) {
		t.Errorf("deprecated row should be pruned; got err=%v", err)
	}

	// active and proposed should survive
	if _, err := store.GetByID(ctx, active.ID); err != nil {
		t.Errorf("active row was unexpectedly pruned: %v", err)
	}
	if _, err := store.GetByID(ctx, proposed.ID); err != nil {
		t.Errorf("proposed row was unexpectedly pruned: %v", err)
	}
}
