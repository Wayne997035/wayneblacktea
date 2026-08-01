package sqlite

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
)

// These three tests mirror
// internal/proposal/accept_pg_nil_guard_test.go's PG-side coverage for the
// SQLite half of the accept adapters: sqliteAcceptAdapter.materializeGoal/
// materializeProject/materializeConcept must return a recoverable error
// instead of panicking when the corresponding store dependency is nil.
// materializeGoal/materializeProject call a.deps.GTD.CreateGoalTx /
// CreateProjectTx directly (not through a WithTx wrapper like the PG side),
// so a nil *GTDStore receiver would panic on the first field access inside
// those methods; materializeConcept panics the same way on a nil
// *LearningStore. No real SQLite connection or open tx is needed — the nil
// check fires before either is touched.
func TestSqliteAcceptAdapter_MaterializeGoal_NilGTDStore(t *testing.T) {
	a := &sqliteAcceptAdapter{deps: AcceptDeps{GTD: nil}}
	payload, err := json.Marshal(map[string]any{"title": "goal title", "area": "career"})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	prop := &db.PendingProposal{Payload: payload}

	_, err = a.materializeGoal(context.Background(), prop)
	if err == nil {
		t.Fatal("materializeGoal: want error for nil GTD store, got nil (would have panicked on CreateGoalTx)")
	}
	if !strings.Contains(err.Error(), "gtd store") {
		t.Errorf("error = %q, want substring %q", err.Error(), "gtd store")
	}
}

func TestSqliteAcceptAdapter_MaterializeProject_NilGTDStore(t *testing.T) {
	a := &sqliteAcceptAdapter{deps: AcceptDeps{GTD: nil}}
	payload, err := json.Marshal(map[string]any{"name": "proj", "title": "Project", "area": "projects"})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	prop := &db.PendingProposal{Payload: payload}

	_, err = a.materializeProject(context.Background(), prop)
	if err == nil {
		t.Fatal("materializeProject: want error for nil GTD store, got nil (would have panicked on CreateProjectTx)")
	}
	if !strings.Contains(err.Error(), "gtd store") {
		t.Errorf("error = %q, want substring %q", err.Error(), "gtd store")
	}
}

func TestSqliteAcceptAdapter_MaterializeConcept_NilLearningStore(t *testing.T) {
	a := &sqliteAcceptAdapter{deps: AcceptDeps{Learning: nil}}
	payload, err := json.Marshal(map[string]any{"title": "concept title", "content": "concept content"})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	prop := &db.PendingProposal{Payload: payload}

	_, err = a.materializeConcept(context.Background(), prop)
	if err == nil {
		t.Fatal("materializeConcept: want error for nil Learning store, got nil (would have panicked on CreateConceptTx)")
	}
	if !strings.Contains(err.Error(), "learning store") {
		t.Errorf("error = %q, want substring %q", err.Error(), "learning store")
	}
}
