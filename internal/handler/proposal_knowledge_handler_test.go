package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/google/uuid"
)

// TestProposalHandler_ConfirmProposal_AcceptKnowledge and
// TestProposalHandler_ConfirmBatch_AcceptKnowledge used to live here,
// asserting the TypeKnowledge accept path against a fakeKnowledgeStoreForProposal
// (implementing only the narrow AddItem method). [F184-03] TypeKnowledge now
// routes through the same proposal.AcceptOrchestration seam TypeGoal/TypeProject
// use (see proposal_handler.go's handleAccept switch / acceptGoalOrProject);
// PgAcceptDeps.Knowledge / AcceptDeps.Knowledge are typed as the CONCRETE
// *knowledge.Store / *sqlite.KnowledgeStore (accept_pg.go:33,
// accept_proposal.go:23), not an interface, so a fake can no longer
// substitute for either — the fake-based tests were structurally
// incompatible with the seam. Real-backend coverage (both directions, HTTP layer) now lives
// in proposal_handler_knowledge_pg_test.go and
// proposal_handler_knowledge_sqlite_test.go instead.
//
// TestProposalHandler_ListProposals_KnowledgeType below is unaffected (it
// never touches the accept path) and stays here unchanged.

// ---- ListProposals: ?type=knowledge (AC6) ----

func TestProposalHandler_ListProposals_KnowledgeType(t *testing.T) {
	knowledgeProp := db.PendingProposal{
		ID:      uuid.New(),
		Type:    "knowledge",
		Status:  "pending",
		Payload: []byte(`{"title":"T","content":"C"}`),
	}
	conceptProp := db.PendingProposal{
		ID:      uuid.New(),
		Type:    "concept",
		Status:  "pending",
		Payload: []byte(`{}`),
	}
	allRows := []db.PendingProposal{knowledgeProp, conceptProp}

	// Note: fakeProposalStore.ListAll ignores the proposalType param and returns
	// all rows in `all`. The handler does client-side status filtering but not
	// type filtering (that's the store's job in production). So each test case
	// pre-filters `all` to only contain rows of the requested type — matching
	// what the real store would return for that type query param.
	cases := []struct {
		name     string
		query    string
		store    *fakeProposalStore
		wantCode int
		wantLen  int
	}{
		{
			name:     "type=knowledge → 200 (AC6 — was 400 before fix)",
			query:    "?type=knowledge",
			store:    &fakeProposalStore{byID: map[uuid.UUID]*db.PendingProposal{}, all: []db.PendingProposal{knowledgeProp}},
			wantCode: http.StatusOK,
			wantLen:  1, // only pending knowledge proposal
		},
		{
			name:     "type=knowledge&status=all → all knowledge rows",
			query:    "?type=knowledge&status=all",
			store:    &fakeProposalStore{byID: map[uuid.UUID]*db.PendingProposal{}, all: []db.PendingProposal{knowledgeProp}},
			wantCode: http.StatusOK,
			wantLen:  1,
		},
		{
			name:     "type=concept still works after adding knowledge",
			query:    "?type=concept",
			store:    &fakeProposalStore{byID: map[uuid.UUID]*db.PendingProposal{}, all: []db.PendingProposal{conceptProp}},
			wantCode: http.StatusOK,
			wantLen:  1,
		},
		{
			name:     "type=invalid still rejected",
			query:    "?type=bogus",
			store:    &fakeProposalStore{byID: map[uuid.UUID]*db.PendingProposal{}, all: allRows},
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewProposalHandler(tc.store, &fakeProposalLearningStore{})
			e.GET("/api/proposals", h.ListProposals)
			rec := performRequest(e, http.MethodGet, "/api/proposals"+tc.query, "")
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
				return
			}
			if tc.wantCode == http.StatusOK {
				var items []json.RawMessage
				if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
					t.Fatalf("response not JSON array: %v (body: %s)", err, rec.Body.String())
				}
				if len(items) != tc.wantLen {
					t.Errorf("got %d items, want %d", len(items), tc.wantLen)
				}
			}
		})
	}
}
