package proposal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
)

// These three tests guard pgAcceptAdapter.materializeGoal/materializeProject/
// materializeConcept against a nil store dependency. Without the guard,
// a.deps.GTD.WithTx(a.tx) (or a.deps.Learning.WithTx(a.tx)) on a nil *Store
// receiver panics inside gtd.Store.WithTx / learning.Store.WithTx (both
// `return &Store{q: s.q.WithTx(tx)}`, which dereferences s.q on a nil
// receiver). materializeGoal/materializeProject are reachable in production
// since PR #17 wired AcceptOrchestration to the HTTP accept-proposal
// endpoint (see dispatch note); materializeConcept has no production caller
// yet but is guarded defensively for the same reason. No real Postgres pool
// or open tx is needed here — the nil check fires before either is touched.
func TestPgAcceptAdapter_MaterializeGoal_NilGTDStore(t *testing.T) {
	a := &pgAcceptAdapter{deps: PgAcceptDeps{GTD: nil}}
	payload, err := json.Marshal(map[string]any{"title": "goal title", "area": "career"})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	prop := &db.PendingProposal{Payload: payload}

	_, err = a.materializeGoal(context.Background(), prop)
	if err == nil {
		t.Fatal("materializeGoal: want error for nil GTD store, got nil (would have panicked on WithTx)")
	}
	if !strings.Contains(err.Error(), "gtd store") {
		t.Errorf("error = %q, want substring %q", err.Error(), "gtd store")
	}
}

func TestPgAcceptAdapter_MaterializeProject_NilGTDStore(t *testing.T) {
	a := &pgAcceptAdapter{deps: PgAcceptDeps{GTD: nil}}
	payload, err := json.Marshal(map[string]any{"name": "proj", "title": "Project", "area": "projects"})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	prop := &db.PendingProposal{Payload: payload}

	_, err = a.materializeProject(context.Background(), prop)
	if err == nil {
		t.Fatal("materializeProject: want error for nil GTD store, got nil (would have panicked on WithTx)")
	}
	if !strings.Contains(err.Error(), "gtd store") {
		t.Errorf("error = %q, want substring %q", err.Error(), "gtd store")
	}
}

func TestPgAcceptAdapter_MaterializeConcept_NilLearningStore(t *testing.T) {
	a := &pgAcceptAdapter{deps: PgAcceptDeps{Learning: nil}}
	payload, err := json.Marshal(map[string]any{"title": "concept title", "content": "concept content"})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	prop := &db.PendingProposal{Payload: payload}

	_, err = a.materializeConcept(context.Background(), prop)
	if err == nil {
		t.Fatal("materializeConcept: want error for nil Learning store, got nil (would have panicked on WithTx)")
	}
	if !strings.Contains(err.Error(), "learning store") {
		t.Errorf("error = %q, want substring %q", err.Error(), "learning store")
	}
}
