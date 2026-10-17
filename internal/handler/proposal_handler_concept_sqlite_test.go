package handler_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// SQLite twins of the conceptBackend cases (bodies live in
// proposal_handler_concept_helpers_test.go).

// newSQLiteConceptBackend opens a fresh :memory: SQLite DB (migrations applied
// by wbtsqlite.Open) wired to the real sqlite accept adapter.
func newSQLiteConceptBackend(t *testing.T) conceptBackend {
	t.Helper()
	ctx := context.Background()
	sdb, err := wbtsqlite.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("wbtsqlite.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := sdb.Close(); err != nil {
			t.Logf("close sqlite: %v", err)
		}
	})

	propStore := wbtsqlite.NewProposalStore(sdb)
	deps := wbtsqlite.AcceptDeps{
		Proposal: propStore,
		GTD:      wbtsqlite.NewGTDStore(sdb),
		Learning: wbtsqlite.NewLearningStore(sdb),
		Decision: wbtsqlite.NewDecisionStore(sdb),
	}
	return conceptBackend{
		name: "sqlite",
		newHandler: func(t *testing.T, wrap func(proposal.AcceptAdapter) proposal.AcceptAdapter) *handler.ProposalHandler {
			t.Helper()
			return handler.NewProposalHandler(propStore).
				WithGoalProjectAccept(func(id uuid.UUID) proposal.AcceptAdapter {
					a := wbtsqlite.NewAcceptAdapter(id, deps)
					if wrap != nil {
						return wrap(a)
					}
					return a
				})
		},
		create: func(t *testing.T, typ proposal.Type, payload []byte) uuid.UUID {
			t.Helper()
			p, err := propStore.Create(ctx, proposal.CreateParams{Type: typ, Payload: payload})
			if err != nil {
				t.Fatalf("Create proposal: %v", err)
			}
			return p.ID
		},
		status: func(t *testing.T, id uuid.UUID) string {
			t.Helper()
			p, err := propStore.Get(ctx, id)
			if err != nil {
				t.Fatalf("Get proposal: %v", err)
			}
			return p.Status
		},
		conceptCount: func(t *testing.T, title string) int {
			t.Helper()
			return scanOneInt(t, sdb.QueryRowContext(ctx, "SELECT count(*) FROM concepts WHERE title = ?", title).Scan)
		},
		decisionCount: func(t *testing.T, title string) int {
			t.Helper()
			return scanOneInt(t, sdb.QueryRowContext(ctx, "SELECT count(*) FROM decisions WHERE title = ?", title).Scan)
		},
		conceptID: func(t *testing.T, title string) string {
			t.Helper()
			return scanOneString(t, sdb.QueryRowContext(ctx, "SELECT id FROM concepts WHERE title = ?", title).Scan)
		},
		scheduleCount: func(t *testing.T, conceptID string) int {
			t.Helper()
			return scanOneInt(t, sdb.QueryRowContext(ctx, "SELECT count(*) FROM review_schedule WHERE concept_id = ?", conceptID).Scan)
		},
		trackTitle: func(*testing.T, string) {},
	}
}

func TestConfirmProposal_TypeConcept_SQLite_MaterialisesConceptAndSchedule(t *testing.T) {
	// [F205-05] the accept response carries the created concept and one concepts row plus its schedule row is written.
	// [F205-06] the SQLite adapter reads the concept back through the open tx, so the response matches PG's.
	runConceptMaterialises(t, newSQLiteConceptBackend(t))
}

func TestConfirmProposal_TypeConcept_SQLite_ResolveFails_RollsBackConcept(t *testing.T) {
	runConceptResolveFails(t, newSQLiteConceptBackend(t))
}

func TestConfirmProposal_TypeConcept_SQLite_OversizePayload_BadRequest(t *testing.T) {
	runConceptOversize(t, newSQLiteConceptBackend(t))
}

func TestConfirmProposal_TypeConcept_SQLite_AlreadyResolved_Conflict(t *testing.T) {
	runConceptAlreadyResolved(t, newSQLiteConceptBackend(t))
}

func TestConfirmBatch_TypeConcept_SQLite_MaterialisesConcepts(t *testing.T) {
	runBatchConceptMaterialises(t, newSQLiteConceptBackend(t))
}

func TestConfirmBatch_TypeConcept_SQLite_MixedValidAndOversize(t *testing.T) {
	runBatchConceptMixedValidAndOversize(t, newSQLiteConceptBackend(t))
}

func TestConfirmBatch_TypeConcept_SQLite_ResolveFails_RollsBackConcept(t *testing.T) {
	runBatchConceptResolveFails(t, newSQLiteConceptBackend(t))
}

func TestConfirmBatch_TypeConcept_SQLite_MixedWithDecision(t *testing.T) {
	runBatchConceptMixedWithDecision(t, newSQLiteConceptBackend(t))
}
