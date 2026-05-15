package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// ---- fake knowledge store ----

type fakeKnowledgeStoreForProposal struct {
	item      *db.KnowledgeItem
	err       error
	callCount int
}

func (f *fakeKnowledgeStoreForProposal) AddItem(_ context.Context, _ knowledge.AddItemParams) (*db.KnowledgeItem, error) {
	f.callCount++
	return f.item, f.err
}

// makeKnowledgeProposal builds a pending TypeKnowledge proposal row.
func makeKnowledgeProposal(id uuid.UUID, title, content string) db.PendingProposal {
	payload, _ := json.Marshal(map[string]any{
		"title":   title,
		"content": content,
		"tags":    []string{"learning"},
	})
	return db.PendingProposal{
		ID:      id,
		Type:    string(proposal.TypeKnowledge),
		Status:  string(proposal.StatusPending),
		Payload: payload,
	}
}

// ---- ConfirmProposal: TypeKnowledge accept (AC4) ----

func TestProposalHandler_ConfirmProposal_AcceptKnowledge(t *testing.T) {
	id := uuid.New()
	knowledgeItem := &db.KnowledgeItem{ID: uuid.New(), Title: "Ebbinghaus forgetting curve", Type: "til"}

	cases := []struct {
		name            string
		proposal        db.PendingProposal
		knowledgeStore  *fakeKnowledgeStoreForProposal
		wantCode        int
		wantItemInResp  bool
		wantItemCreated int
	}{
		{
			name:            "accept knowledge → 200 with knowledge_item",
			proposal:        makeKnowledgeProposal(id, "Ebbinghaus forgetting curve", "Memory decays without review."),
			knowledgeStore:  &fakeKnowledgeStoreForProposal{item: knowledgeItem},
			wantCode:        http.StatusOK,
			wantItemInResp:  true,
			wantItemCreated: 1,
		},
		{
			name:            "accept knowledge, store not wired → 200 but no knowledge_item field",
			proposal:        makeKnowledgeProposal(id, "Ebbinghaus forgetting curve", "Memory decays without review."),
			knowledgeStore:  nil,
			wantCode:        http.StatusOK,
			wantItemInResp:  false,
			wantItemCreated: 0,
		},
		{
			name:            "accept knowledge, AddItem fails → 200 (non-fatal)",
			proposal:        makeKnowledgeProposal(id, "Ebbinghaus forgetting curve", "Memory decays without review."),
			knowledgeStore:  &fakeKnowledgeStoreForProposal{err: errors.New("knowledge store error")},
			wantCode:        http.StatusOK,
			wantItemInResp:  false,
			wantItemCreated: 1, // attempted
		},
		{
			name: "malformed knowledge payload → 400",
			proposal: db.PendingProposal{
				ID:      id,
				Type:    string(proposal.TypeKnowledge),
				Status:  string(proposal.StatusPending),
				Payload: []byte(`{not json`),
			},
			knowledgeStore:  &fakeKnowledgeStoreForProposal{item: knowledgeItem},
			wantCode:        http.StatusBadRequest,
			wantItemCreated: 0,
		},
		{
			name: "knowledge payload missing title → 400",
			proposal: db.PendingProposal{
				ID:      id,
				Type:    string(proposal.TypeKnowledge),
				Status:  string(proposal.StatusPending),
				Payload: []byte(`{"title":"","content":"some content"}`),
			},
			knowledgeStore:  &fakeKnowledgeStoreForProposal{item: knowledgeItem},
			wantCode:        http.StatusBadRequest,
			wantItemCreated: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeProposalStore(tc.proposal)
			e := newEcho()
			h := handler.NewProposalHandler(store, &fakeProposalLearningStore{})
			if tc.knowledgeStore != nil {
				h = h.WithKnowledge(tc.knowledgeStore)
			}
			e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
			rec := performRequest(e, http.MethodPost, "/api/proposals/"+id.String()+"/confirm", `{"action":"accept"}`)
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
				return
			}
			if tc.wantCode == http.StatusOK {
				var resp map[string]json.RawMessage
				if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
					t.Fatalf("response not valid JSON: %v (body: %s)", err, rec.Body.String())
				}
				_, hasItem := resp["knowledge_item"]
				if tc.wantItemInResp && !hasItem {
					t.Errorf("expected knowledge_item in response, got: %s", rec.Body.String())
				}
				if !tc.wantItemInResp && hasItem {
					// knowledge_item null is acceptable (omitempty), but non-null is wrong
					if string(resp["knowledge_item"]) != "null" {
						t.Errorf("unexpected non-null knowledge_item: %s", rec.Body.String())
					}
				}
			}
			if tc.knowledgeStore != nil && tc.knowledgeStore.callCount != tc.wantItemCreated {
				t.Errorf("AddItem called %d times, want %d", tc.knowledgeStore.callCount, tc.wantItemCreated)
			}
		})
	}
}

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

// ---- ConfirmBatch: TypeKnowledge accept ----

func TestProposalHandler_ConfirmBatch_AcceptKnowledge(t *testing.T) {
	knowledgeID := uuid.New()
	conceptID := uuid.New()

	knowledge1 := makeKnowledgeProposal(knowledgeID, "TIL about goroutines", "Goroutines are lightweight threads.")
	concept1 := makeConceptProposal(conceptID, "Goroutines", "Lightweight threads", []string{"go"})

	cases := []struct {
		name               string
		body               string
		store              *fakeProposalStore
		knowledge          *fakeKnowledgeStoreForProposal
		learning           *fakeProposalLearningStore
		wantCode           int
		wantOKCount        int
		wantKnowledgeAdded int
		wantConceptCreated int
	}{
		{
			name:               "accept knowledge → AddItem called once",
			body:               `{"ids":["` + knowledgeID.String() + `"],"action":"accept"}`,
			store:              newFakeProposalStore(knowledge1),
			knowledge:          &fakeKnowledgeStoreForProposal{item: &db.KnowledgeItem{ID: uuid.New()}},
			learning:           &fakeProposalLearningStore{},
			wantCode:           http.StatusOK,
			wantOKCount:        1,
			wantKnowledgeAdded: 1,
			wantConceptCreated: 0,
		},
		{
			name:               "accept knowledge + concept → both materialised",
			body:               `{"ids":["` + knowledgeID.String() + `","` + conceptID.String() + `"],"action":"accept"}`,
			store:              newFakeProposalStore(knowledge1, concept1),
			knowledge:          &fakeKnowledgeStoreForProposal{item: &db.KnowledgeItem{ID: uuid.New()}},
			learning:           &fakeProposalLearningStore{concept: &db.Concept{ID: uuid.New()}},
			wantCode:           http.StatusOK,
			wantOKCount:        2,
			wantKnowledgeAdded: 1,
			wantConceptCreated: 1,
		},
		{
			name:               "reject knowledge → AddItem NOT called",
			body:               `{"ids":["` + knowledgeID.String() + `"],"action":"reject"}`,
			store:              newFakeProposalStore(knowledge1),
			knowledge:          &fakeKnowledgeStoreForProposal{},
			learning:           &fakeProposalLearningStore{},
			wantCode:           http.StatusOK,
			wantOKCount:        1,
			wantKnowledgeAdded: 0,
		},
		{
			name:               "knowledge store not wired → resolve still OK",
			body:               `{"ids":["` + knowledgeID.String() + `"],"action":"accept"}`,
			store:              newFakeProposalStore(knowledge1),
			knowledge:          nil,
			learning:           &fakeProposalLearningStore{},
			wantCode:           http.StatusOK,
			wantOKCount:        1,
			wantKnowledgeAdded: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewProposalHandler(tc.store, tc.learning)
			if tc.knowledge != nil {
				h = h.WithKnowledge(tc.knowledge)
			}
			e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
			rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
				return
			}
			if tc.wantCode != http.StatusOK {
				return
			}
			var resp struct {
				Results []struct {
					OK bool `json:"ok"`
				} `json:"results"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("response not JSON: %v (body: %s)", err, rec.Body.String())
			}
			okCount := 0
			for _, r := range resp.Results {
				if r.OK {
					okCount++
				}
			}
			if okCount != tc.wantOKCount {
				t.Errorf("got %d OK, want %d", okCount, tc.wantOKCount)
			}
			if tc.knowledge != nil && tc.knowledge.callCount != tc.wantKnowledgeAdded {
				t.Errorf("AddItem called %d times, want %d", tc.knowledge.callCount, tc.wantKnowledgeAdded)
			}
			if tc.learning.createCount != tc.wantConceptCreated {
				t.Errorf("CreateConcept called %d times, want %d", tc.learning.createCount, tc.wantConceptCreated)
			}
		})
	}
}
