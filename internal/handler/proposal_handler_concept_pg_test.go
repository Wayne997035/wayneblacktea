package handler_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// Postgres twins of the conceptBackend cases (bodies live in
// proposal_handler_concept_helpers_test.go), run against the shared
// testcontainers pool (openGoalProjectTestPgPool; skipped under -short). The
// container is shared across the package, so every row a test creates is
// removed by title / id in t.Cleanup and every title is unique. review_schedule
// rows are deleted BEFORE their concepts (no FK, so nothing enforces the order;
// it just avoids leaving schedule rows pointing at a deleted concept).

// newPGConceptBackend wires a ProposalHandler to the real PG accept adapter
// over the shared pool. Skips (via openGoalProjectTestPgPool) under -short.
func newPGConceptBackend(t *testing.T) conceptBackend {
	t.Helper()
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()

	propStore := proposal.NewStore(pool, nil)
	deps := proposal.PgAcceptDeps{
		Pool:     pool,
		Proposal: propStore,
		GTD:      gtd.NewStore(pool, nil),
		Learning: learning.NewStore(pool, nil),
		Decision: decision.NewStore(pool, nil),
	}
	exec := func(t *testing.T, query string, arg any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, arg); err != nil {
			t.Logf("cleanup %q: %v", query, err)
		}
	}
	return conceptBackend{
		name: "pg",
		newHandler: func(t *testing.T, wrap func(proposal.AcceptAdapter) proposal.AcceptAdapter) *handler.ProposalHandler {
			t.Helper()
			return handler.NewProposalHandler(propStore).
				WithGoalProjectAccept(func(id uuid.UUID) proposal.AcceptAdapter {
					a := proposal.NewPgAcceptAdapter(id, deps)
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
			t.Cleanup(func() { exec(t, "DELETE FROM pending_proposals WHERE id = $1", p.ID) })
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
			return scanOneInt(t, pool.QueryRow(ctx, "SELECT count(*) FROM concepts WHERE title = $1", title).Scan)
		},
		decisionCount: func(t *testing.T, title string) int {
			t.Helper()
			return scanOneInt(t, pool.QueryRow(ctx, "SELECT count(*) FROM decisions WHERE title = $1", title).Scan)
		},
		conceptID: func(t *testing.T, title string) string {
			t.Helper()
			return scanOneString(t, pool.QueryRow(ctx, "SELECT id::text FROM concepts WHERE title = $1", title).Scan)
		},
		scheduleCount: func(t *testing.T, conceptID string) int {
			t.Helper()
			return scanOneInt(t, pool.QueryRow(ctx, "SELECT count(*) FROM review_schedule WHERE concept_id = $1::uuid", conceptID).Scan)
		},
		trackTitle: func(t *testing.T, title string) {
			t.Helper()
			t.Cleanup(func() {
				exec(t, "DELETE FROM review_schedule WHERE concept_id IN (SELECT id FROM concepts WHERE title = $1)", title)
				exec(t, "DELETE FROM concepts WHERE title = $1", title)
				exec(t, "DELETE FROM decisions WHERE title = $1", title)
			})
		},
	}
}

func TestConfirmProposal_TypeConcept_PG_MaterialisesConceptAndSchedule(t *testing.T) {
	runConceptMaterialises(t, newPGConceptBackend(t))
}

func TestConfirmProposal_TypeConcept_PG_ResolveFails_RollsBackConcept(t *testing.T) {
	runConceptResolveFails(t, newPGConceptBackend(t))
}

func TestConfirmProposal_TypeConcept_PG_OversizePayload_BadRequest(t *testing.T) {
	runConceptOversize(t, newPGConceptBackend(t))
}

func TestConfirmProposal_TypeConcept_PG_AlreadyResolved_Conflict(t *testing.T) {
	runConceptAlreadyResolved(t, newPGConceptBackend(t))
}

func TestConfirmBatch_TypeConcept_PG_MaterialisesConcepts(t *testing.T) {
	runBatchConceptMaterialises(t, newPGConceptBackend(t))
}

func TestConfirmBatch_TypeConcept_PG_MixedValidAndOversize(t *testing.T) {
	runBatchConceptMixedValidAndOversize(t, newPGConceptBackend(t))
}

func TestConfirmBatch_TypeConcept_PG_ResolveFails_RollsBackConcept(t *testing.T) {
	runBatchConceptResolveFails(t, newPGConceptBackend(t))
}

func TestConfirmBatch_TypeConcept_PG_MixedWithDecision(t *testing.T) {
	runBatchConceptMixedWithDecision(t, newPGConceptBackend(t))
}
