package handler_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// [F205-01] Postgres twins of the SQLite cases in
// proposal_handler_decision_sqlite_test.go: the case bodies live there once
// (runDecision* / runBatch*) and run here against the shared testcontainers
// pool (openGoalProjectTestPgPool; skipped under -short). The container is
// shared across the package, so every row a test creates is removed by title /
// id in t.Cleanup and every title is unique.

// newPGDecisionBackend wires a ProposalHandler to the real PG accept adapter
// over the shared pool. Skips (via openGoalProjectTestPgPool) under -short.
func newPGDecisionBackend(t *testing.T) decisionBackend {
	t.Helper()
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()

	propStore := proposal.NewStore(pool, nil)
	learningStore := learning.NewStore(pool, nil)
	deps := proposal.PgAcceptDeps{
		Pool:     pool,
		Proposal: propStore,
		GTD:      gtd.NewStore(pool, nil),
		Learning: learningStore,
		Decision: decision.NewStore(pool, nil),
	}
	exec := func(t *testing.T, query string, arg any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, arg); err != nil {
			t.Logf("cleanup %q: %v", query, err)
		}
	}
	count := func(t *testing.T, query, title string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, query, title).Scan(&n); err != nil {
			t.Fatalf("count query: %v", err)
		}
		return n
	}
	return decisionBackend{
		name:           "pg",
		rawJSONPayload: false, // pending_proposals.payload is jsonb: broken JSON is refused at INSERT
		newHandler: func(wrap func(proposal.AcceptAdapter) proposal.AcceptAdapter) *handler.ProposalHandler {
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
		decisionCount: func(t *testing.T, title string) int {
			t.Helper()
			return count(t, "SELECT count(*) FROM decisions WHERE title = $1", title)
		},
		goalCount: func(t *testing.T, title string) int {
			t.Helper()
			return count(t, "SELECT count(*) FROM goals WHERE title = $1", title)
		},
		decisionAudit: func(t *testing.T, title string) decisionAuditRow {
			t.Helper()
			var row decisionAuditRow
			err := pool.QueryRow(ctx,
				"SELECT source, confirmed_by_human, actor_session_id IS NULL, decision, rationale FROM decisions WHERE title = $1", title).
				Scan(&row.Source, &row.Confirmed, &row.ActorIsNull, &row.Decision, &row.Rationale)
			if err != nil {
				t.Fatalf("decision audit query: %v", err)
			}
			return row
		},
		trackTitle: func(t *testing.T, title string) {
			t.Helper()
			t.Cleanup(func() {
				exec(t, "DELETE FROM decisions WHERE title = $1", title)
				exec(t, "DELETE FROM goals WHERE title = $1", title)
			})
		},
	}
}

func TestConfirmProposal_TypeDecision_PG_MaterialisesDecisionsRow(t *testing.T) {
	runDecisionMaterialises(t, newPGDecisionBackend(t))
}

func TestConfirmProposal_TypeDecision_PG_TagNoise_RollsBack(t *testing.T) {
	runDecisionTagNoise(t, newPGDecisionBackend(t))
}

func TestConfirmProposal_TypeDecision_PG_ResolveFails_RollsBackDecision(t *testing.T) {
	runDecisionResolveFails(t, newPGDecisionBackend(t))
}

func TestConfirmProposal_TypeDecision_PG_PayloadRejected_BadRequest(t *testing.T) {
	runDecisionPayloadRejected(t, newPGDecisionBackend(t))
}

func TestConfirmProposal_TypeDecision_PG_CapBoundaries_Accepted(t *testing.T) {
	runDecisionCapBoundaries(t, newPGDecisionBackend(t))
}

func TestConfirmProposal_TypeDecision_PG_ForgedSourceIgnored(t *testing.T) {
	runDecisionForgedSource(t, newPGDecisionBackend(t))
}

func TestConfirmProposal_TypeDecision_PG_DoubleAccept_Conflict(t *testing.T) {
	runDecisionDoubleAccept(t, newPGDecisionBackend(t))
}

func TestConfirmBatch_TypeDecision_PG_MaterialisesDecisionsRows(t *testing.T) {
	runBatchMaterialises(t, newPGDecisionBackend(t))
}

func TestConfirmBatch_TypeDecision_PG_MixedValidAndInvalid(t *testing.T) {
	runBatchMixedValidAndInvalid(t, newPGDecisionBackend(t))
}

func TestConfirmBatch_TypeDecision_PG_AlreadyAccepted_Skipped(t *testing.T) {
	runBatchAlreadyAccepted(t, newPGDecisionBackend(t))
}

func TestConfirmBatch_TypeDecision_PG_Reject_NoMaterialise(t *testing.T) {
	runBatchReject(t, newPGDecisionBackend(t))
}

func TestConfirmBatch_TypeDecision_PG_MixedWithGoal(t *testing.T) {
	runBatchMixedWithGoal(t, newPGDecisionBackend(t))
}

func TestConfirmBatch_TypeDecision_PG_ResolveFails_RollsBackDecision(t *testing.T) {
	runBatchResolveFails(t, newPGDecisionBackend(t))
}

// decisionShape is the backend-neutral projection of an accept response that
// AC-3 compares: HTTP status plus every decision field a consumer can see,
// minus the values that legitimately differ per run or per backend (id,
// created_at, workspace_id).
type decisionShape struct {
	Status                                      int
	Title, Context, Decision, Rationale, Source string
	Confirmed                                   bool
	ActorNull, RepoNull, ProjectNull, TaskNull  bool
}

// acceptShape accepts one AC-1 payload on be and projects the response.
func acceptShape(t *testing.T, be decisionBackend, title string) decisionShape {
	t.Helper()
	be.trackTitle(t, title)
	id := be.create(t, proposal.TypeDecision, decisionPayload(t, title))
	rec := postConfirm(be.newHandler(nil), id, acceptBody)
	return shapeOf(t, rec)
}

func shapeOf(t *testing.T, rec *httptest.ResponseRecorder) decisionShape {
	t.Helper()
	dec := requireAcceptedDecision(t, rec)
	return decisionShape{
		Status:      rec.Code,
		Title:       dec.Title,
		Context:     dec.Context,
		Decision:    dec.Decision,
		Rationale:   dec.Rationale,
		Source:      dec.Source,
		Confirmed:   dec.ConfirmedByHuman,
		ActorNull:   !dec.ActorSessionID.Valid,
		RepoNull:    !dec.RepoName.Valid,
		ProjectNull: !dec.ProjectID.Valid,
		TaskNull:    !dec.TaskID.Valid,
	}
}

// TestConfirmProposal_TypeDecision_BackendParity is AC-3 (decision cbbbf686:
// the two backends are functionally identical): the same payload accepted
// through the SQLite handler and the PG handler yields the same status and the
// same decision object shape. If either adapter returned a non-*db.Decision the
// `decision` key would vanish on that backend and the comparison would fail.
func TestConfirmProposal_TypeDecision_BackendParity(t *testing.T) {
	// [F205-02] SQLite and PG return the same decision object for the same accept.
	title := "parity-" + uuid.NewString()
	shapes := map[string]decisionShape{}
	t.Run("sqlite", func(t *testing.T) {
		shapes["sqlite"] = acceptShape(t, newSQLiteDecisionBackend(t), title)
	})
	t.Run("pg", func(t *testing.T) {
		shapes["pg"] = acceptShape(t, newPGDecisionBackend(t), title) // skips under -short
	})

	pg, ok := shapes["pg"]
	if !ok {
		t.Skip("PG shape unavailable (-short): parity needs both backends")
	}
	if sqlite := shapes["sqlite"]; sqlite != pg {
		t.Errorf("backend shapes differ:\n sqlite = %+v\n pg     = %+v", sqlite, pg)
	}
}
