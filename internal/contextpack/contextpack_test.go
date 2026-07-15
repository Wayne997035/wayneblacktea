package contextpack

import "testing"

// TestNewAssembler_ValidStoresSucceed is the happy path: every fake store is
// non-nil, so NewAssembler must return a usable *Assembler and no error.
func TestNewAssembler_ValidStoresSucceed(t *testing.T) {
	f := newFakes()
	a, err := NewAssembler(f.gtd, f.decision, f.knowledge, f.atom, f.procedural, f.skill,
		f.outcome, f.reflection, f.rule, f.session, f.workSession)
	if err != nil {
		t.Fatalf("NewAssembler returned unexpected error: %v", err)
	}
	if a == nil {
		t.Fatal("NewAssembler returned a nil *Assembler with no error")
	}
}

// TestNewAssembler_NilGuard asserts every store argument is validated up
// front — a nil store must produce an error from NewAssembler itself, not a
// *Assembler that panics on first use deep inside retrieve() (P1 review
// finding I).
func TestNewAssembler_NilGuard(t *testing.T) {
	f := newFakes()

	t.Run("nil gtdStore", func(t *testing.T) {
		_, err := NewAssembler(nil, f.decision, f.knowledge, f.atom, f.procedural, f.skill,
			f.outcome, f.reflection, f.rule, f.session, f.workSession)
		if err == nil {
			t.Error("expected an error for nil gtdStore")
		}
	})
	t.Run("nil decisionStore", func(t *testing.T) {
		_, err := NewAssembler(f.gtd, nil, f.knowledge, f.atom, f.procedural, f.skill,
			f.outcome, f.reflection, f.rule, f.session, f.workSession)
		if err == nil {
			t.Error("expected an error for nil decisionStore")
		}
	})
	t.Run("nil knowledgeStore", func(t *testing.T) {
		_, err := NewAssembler(f.gtd, f.decision, nil, f.atom, f.procedural, f.skill,
			f.outcome, f.reflection, f.rule, f.session, f.workSession)
		if err == nil {
			t.Error("expected an error for nil knowledgeStore")
		}
	})
	t.Run("nil atomStore", func(t *testing.T) {
		_, err := NewAssembler(f.gtd, f.decision, f.knowledge, nil, f.procedural, f.skill,
			f.outcome, f.reflection, f.rule, f.session, f.workSession)
		if err == nil {
			t.Error("expected an error for nil atomStore")
		}
	})
	t.Run("nil proceduralStore", func(t *testing.T) {
		_, err := NewAssembler(f.gtd, f.decision, f.knowledge, f.atom, nil, f.skill,
			f.outcome, f.reflection, f.rule, f.session, f.workSession)
		if err == nil {
			t.Error("expected an error for nil proceduralStore")
		}
	})
	t.Run("nil skillStore", func(t *testing.T) {
		_, err := NewAssembler(f.gtd, f.decision, f.knowledge, f.atom, f.procedural, nil,
			f.outcome, f.reflection, f.rule, f.session, f.workSession)
		if err == nil {
			t.Error("expected an error for nil skillStore")
		}
	})
	t.Run("nil outcomeStore", func(t *testing.T) {
		_, err := NewAssembler(f.gtd, f.decision, f.knowledge, f.atom, f.procedural, f.skill,
			nil, f.reflection, f.rule, f.session, f.workSession)
		if err == nil {
			t.Error("expected an error for nil outcomeStore")
		}
	})
	t.Run("nil reflectionStore", func(t *testing.T) {
		_, err := NewAssembler(f.gtd, f.decision, f.knowledge, f.atom, f.procedural, f.skill,
			f.outcome, nil, f.rule, f.session, f.workSession)
		if err == nil {
			t.Error("expected an error for nil reflectionStore")
		}
	})
	t.Run("nil behaviorRuleStore", func(t *testing.T) {
		_, err := NewAssembler(f.gtd, f.decision, f.knowledge, f.atom, f.procedural, f.skill,
			f.outcome, f.reflection, nil, f.session, f.workSession)
		if err == nil {
			t.Error("expected an error for nil behaviorRuleStore")
		}
	})
	t.Run("nil sessionStore", func(t *testing.T) {
		_, err := NewAssembler(f.gtd, f.decision, f.knowledge, f.atom, f.procedural, f.skill,
			f.outcome, f.reflection, f.rule, nil, f.workSession)
		if err == nil {
			t.Error("expected an error for nil sessionStore")
		}
	})
	t.Run("nil workSessionStore", func(t *testing.T) {
		_, err := NewAssembler(f.gtd, f.decision, f.knowledge, f.atom, f.procedural, f.skill,
			f.outcome, f.reflection, f.rule, f.session, nil)
		if err == nil {
			t.Error("expected an error for nil workSessionStore")
		}
	})
}
