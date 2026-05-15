package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---- fake proposal stores ----

// fakeProposalStore implements proposal.StoreIface for testing.
type fakeProposalStore struct {
	pending    []db.PendingProposal
	byID       map[uuid.UUID]*db.PendingProposal
	resolveErr error                // error returned by Resolve
	getErr     error                // error returned by Get
	listErr    error                // error returned by ListPending
	resolved   []uuid.UUID          // records which IDs were resolved
	resolvedAs proposal.Status      // last resolved status
	all        []db.PendingProposal // returned by ListAll; falls back to pending if nil
	listAllErr error                // error returned by ListAll
}

func newFakeProposalStore(rows ...db.PendingProposal) *fakeProposalStore {
	m := make(map[uuid.UUID]*db.PendingProposal, len(rows))
	for i := range rows {
		m[rows[i].ID] = &rows[i]
	}
	return &fakeProposalStore{
		pending: rows,
		byID:    m,
	}
}

func (f *fakeProposalStore) Create(_ context.Context, _ proposal.CreateParams) (*db.PendingProposal, error) {
	return nil, nil
}

func (f *fakeProposalStore) ListPending(_ context.Context) ([]db.PendingProposal, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.pending, nil
}

func (f *fakeProposalStore) ListAll(_ context.Context, _ string, _ int32) ([]db.PendingProposal, error) {
	if f.listAllErr != nil {
		return nil, f.listAllErr
	}
	if f.all != nil {
		return f.all, nil
	}
	return f.pending, nil
}

func (f *fakeProposalStore) Get(_ context.Context, id uuid.UUID) (*db.PendingProposal, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if p, ok := f.byID[id]; ok {
		return p, nil
	}
	return nil, proposal.ErrNotFound
}

func (f *fakeProposalStore) Resolve(_ context.Context, id uuid.UUID, status proposal.Status) (*db.PendingProposal, error) {
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	p, ok := f.byID[id]
	if !ok {
		return nil, proposal.ErrNotFound
	}
	f.resolved = append(f.resolved, id)
	f.resolvedAs = status
	p.Status = string(status)
	return p, nil
}

func (f *fakeProposalStore) AutoProposeConceptFromKnowledge(_ context.Context, _ *db.KnowledgeItem, _ string) (*db.PendingProposal, error) {
	return nil, nil
}

// fakeProposalLearningStore implements learning.StoreIface for testing.
// Only CreateConcept is exercised in proposal handler tests; others are no-ops.
type fakeProposalLearningStore struct {
	concept     *db.Concept
	err         error
	createCount int
}

func (f *fakeProposalLearningStore) CreateConcept(_ context.Context, _, _ string, _ []string) (*db.Concept, error) {
	f.createCount++
	return f.concept, f.err
}

func (f *fakeProposalLearningStore) DueReviews(_ context.Context, _ int) ([]learning.DueReview, error) {
	return nil, nil
}

func (f *fakeProposalLearningStore) SubmitReview(_ context.Context, _ uuid.UUID, _ learning.CardState, _ learning.Rating) error {
	return nil
}

func (f *fakeProposalLearningStore) CountDueReviews(_ context.Context) (int, error) {
	return 0, nil
}

func (f *fakeProposalLearningStore) ListForAIReview(_ context.Context, _ int) ([]learning.ConceptForReview, error) {
	return nil, nil
}

func (f *fakeProposalLearningStore) UpdateConceptStatus(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}

func (f *fakeProposalLearningStore) ReviewHistory(_ context.Context) ([]learning.ConceptHistoryRow, error) {
	return nil, nil
}

func (f *fakeProposalLearningStore) LearningStats(_ context.Context) (*learning.LearningStatsResult, error) {
	return &learning.LearningStatsResult{}, nil
}

// ---- helpers ----

func makeConceptProposal(id uuid.UUID, title, content string, tags []string) db.PendingProposal {
	payload, _ := json.Marshal(map[string]any{
		"title":   title,
		"content": content,
		"tags":    tags,
	})
	return db.PendingProposal{
		ID:      id,
		Type:    string(proposal.TypeConcept),
		Status:  string(proposal.StatusPending),
		Payload: payload,
	}
}

func makeGoalProposal(id uuid.UUID) db.PendingProposal {
	payload, _ := json.Marshal(map[string]any{
		"title": "Become CEO",
		"area":  "career",
	})
	return db.PendingProposal{
		ID:      id,
		Type:    string(proposal.TypeGoal),
		Status:  string(proposal.StatusPending),
		Payload: payload,
	}
}

// ---- ConfirmBatch tests ----

func TestProposalHandler_ConfirmBatch(t *testing.T) {
	concept1ID := uuid.New()
	concept2ID := uuid.New()
	goalID := uuid.New()
	unknownID := uuid.New()

	concept1 := makeConceptProposal(concept1ID, "Ebbinghaus", "Forgetting curve", []string{"memory"})
	concept2 := makeConceptProposal(concept2ID, "Zeigarnik", "Unfinished tasks", []string{"psychology"})
	goalProp := makeGoalProposal(goalID)

	cases := []struct {
		name               string
		body               string
		store              *fakeProposalStore
		learning           *fakeProposalLearningStore
		wantCode           int
		wantResultCount    int
		wantOKCount        int
		wantConceptCreated int
	}{
		{
			name:               "accept two concept proposals → two concepts created",
			body:               `{"ids":["` + concept1ID.String() + `","` + concept2ID.String() + `"],"action":"accept"}`,
			store:              newFakeProposalStore(concept1, concept2),
			learning:           &fakeProposalLearningStore{concept: &db.Concept{ID: uuid.New()}},
			wantCode:           http.StatusOK,
			wantResultCount:    2,
			wantOKCount:        2,
			wantConceptCreated: 2,
		},
		{
			name:               "accept mix: concept + goal → only one concept created",
			body:               `{"ids":["` + concept1ID.String() + `","` + goalID.String() + `"],"action":"accept"}`,
			store:              newFakeProposalStore(concept1, goalProp),
			learning:           &fakeProposalLearningStore{concept: &db.Concept{ID: uuid.New()}},
			wantCode:           http.StatusOK,
			wantResultCount:    2,
			wantOKCount:        2,
			wantConceptCreated: 1,
		},
		{
			name:               "reject concept proposals → no concept created",
			body:               `{"ids":["` + concept1ID.String() + `"],"action":"reject"}`,
			store:              newFakeProposalStore(concept1),
			learning:           &fakeProposalLearningStore{},
			wantCode:           http.StatusOK,
			wantResultCount:    1,
			wantOKCount:        1,
			wantConceptCreated: 0,
		},
		{
			name:               "unknown ID → skipped, batch still returns",
			body:               `{"ids":["` + unknownID.String() + `"],"action":"accept"}`,
			store:              newFakeProposalStore(), // empty store
			learning:           &fakeProposalLearningStore{},
			wantCode:           http.StatusOK,
			wantResultCount:    1,
			wantOKCount:        0, // skipped
			wantConceptCreated: 0,
		},
		{
			name:               "concept creation failure → batch entry error, proposal not resolved (materialise-first)",
			body:               `{"ids":["` + concept1ID.String() + `"],"action":"accept"}`,
			store:              newFakeProposalStore(concept1),
			learning:           &fakeProposalLearningStore{err: errors.New("concept store down")},
			wantCode:           http.StatusOK,
			wantResultCount:    1,
			wantOKCount:        0, // materialise failed → Resolve NOT called
			wantConceptCreated: 1, // attempted before Resolve
		},
		{
			name:     "invalid action → 400",
			body:     `{"ids":["` + concept1ID.String() + `"],"action":"noop"}`,
			store:    newFakeProposalStore(concept1),
			learning: &fakeProposalLearningStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "empty ids → 400",
			body:     `{"ids":[],"action":"accept"}`,
			store:    newFakeProposalStore(),
			learning: &fakeProposalLearningStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid UUID in ids → 400",
			body:     `{"ids":["not-a-uuid"],"action":"accept"}`,
			store:    newFakeProposalStore(),
			learning: &fakeProposalLearningStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid JSON → 400",
			body:     `{bad`,
			store:    newFakeProposalStore(),
			learning: &fakeProposalLearningStore{},
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewProposalHandler(tc.store, tc.learning)
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
					ID      string `json:"id"`
					OK      bool   `json:"ok"`
					Skipped bool   `json:"skipped"`
					Error   string `json:"error"`
				} `json:"results"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("response not valid JSON: %v (body: %s)", err, rec.Body.String())
			}
			if len(resp.Results) != tc.wantResultCount {
				t.Errorf("got %d results, want %d", len(resp.Results), tc.wantResultCount)
			}
			okCount := 0
			for _, r := range resp.Results {
				if r.OK {
					okCount++
				}
			}
			if okCount != tc.wantOKCount {
				t.Errorf("got %d OK results, want %d", okCount, tc.wantOKCount)
			}
			if tc.learning.createCount != tc.wantConceptCreated {
				t.Errorf("CreateConcept called %d times, want %d", tc.learning.createCount, tc.wantConceptCreated)
			}
		})
	}
}

// ---- ConfirmProposal tests (single-accept) ----

func TestProposalHandler_ConfirmProposal_Accept(t *testing.T) {
	id := uuid.New()
	pending := db.PendingProposal{
		ID:      id,
		Type:    string(proposal.TypeConcept),
		Status:  string(proposal.StatusPending),
		Payload: []byte(`{"title":"Test","content":"Body","tags":["a"]}`),
	}

	cases := []struct {
		name     string
		paramID  string
		body     string
		store    *fakeProposalStore
		learning *fakeProposalLearningStore
		wantCode int
	}{
		{
			name:     "accept concept → 200 with concept",
			paramID:  id.String(),
			body:     `{"action":"accept"}`,
			store:    newFakeProposalStore(pending),
			learning: &fakeProposalLearningStore{concept: &db.Concept{ID: uuid.New(), Title: "Test"}},
			wantCode: http.StatusOK,
		},
		{
			name:     "reject proposal → 200 (no concept created)",
			paramID:  id.String(),
			body:     `{"action":"reject"}`,
			store:    newFakeProposalStore(pending),
			learning: &fakeProposalLearningStore{},
			wantCode: http.StatusOK,
		},
		{
			name:     "invalid UUID → 400",
			paramID:  "not-a-uuid",
			body:     `{"action":"accept"}`,
			store:    newFakeProposalStore(),
			learning: &fakeProposalLearningStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "not found → 404",
			paramID:  uuid.New().String(),
			body:     `{"action":"accept"}`,
			store:    newFakeProposalStore(), // empty
			learning: &fakeProposalLearningStore{},
			wantCode: http.StatusNotFound,
		},
		{
			name:     "invalid action → 400",
			paramID:  id.String(),
			body:     `{"action":"noop"}`,
			store:    newFakeProposalStore(pending),
			learning: &fakeProposalLearningStore{},
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewProposalHandler(tc.store, tc.learning)
			e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
			rec := performRequest(e, http.MethodPost, "/api/proposals/"+tc.paramID+"/confirm", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// TestProposalHandler_ConfirmProposal_Accept_MaterialiseFirst verifies that
// materialisation happens BEFORE Resolve so a materialise failure cannot leave
// the proposal accepted with no backing entity.
func TestProposalHandler_ConfirmProposal_Accept_MaterialiseFirst(t *testing.T) {
	conceptProp := db.PendingProposal{
		ID:      uuid.New(),
		Type:    string(proposal.TypeConcept),
		Status:  string(proposal.StatusPending),
		Payload: []byte(`{"title":"Test","content":"Body","tags":[]}`),
	}

	t.Run("concept: materialise fails → 500, Resolve NOT called", func(t *testing.T) {
		store := newFakeProposalStore(conceptProp)
		learn := &fakeProposalLearningStore{err: errors.New("db down")}
		e := newEcho()
		h := handler.NewProposalHandler(store, learn)
		e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
		rec := performRequest(e, http.MethodPost, "/api/proposals/"+conceptProp.ID.String()+"/confirm", `{"action":"accept"}`)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("want 500, got %d: %s", rec.Code, rec.Body.String())
		}
		if len(store.resolved) != 0 {
			t.Errorf("Resolve must not be called when materialise fails; got %v", store.resolved)
		}
	})

	t.Run("concept: materialise succeeds → proposal resolved", func(t *testing.T) {
		store := newFakeProposalStore(conceptProp)
		learn := &fakeProposalLearningStore{concept: &db.Concept{ID: uuid.New(), Title: "Test"}}
		e := newEcho()
		h := handler.NewProposalHandler(store, learn)
		e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
		rec := performRequest(e, http.MethodPost, "/api/proposals/"+conceptProp.ID.String()+"/confirm", `{"action":"accept"}`)
		if rec.Code != http.StatusOK {
			t.Errorf("want 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if len(store.resolved) != 1 {
			t.Errorf("Resolve must be called exactly once; got %v", store.resolved)
		}
	})
}

// ---- ListProposals tests (UX-5: status filter) ----

func TestProposalHandler_ListProposals(t *testing.T) {
	pending1 := db.PendingProposal{
		ID:      uuid.New(),
		Type:    "concept",
		Status:  "pending",
		Payload: []byte(`{}`),
	}
	accepted1 := db.PendingProposal{
		ID:      uuid.New(),
		Type:    "concept",
		Status:  "accepted",
		Payload: []byte(`{}`),
	}
	rejected1 := db.PendingProposal{
		ID:      uuid.New(),
		Type:    "concept",
		Status:  "rejected",
		Payload: []byte(`{}`),
	}
	allRows := []db.PendingProposal{pending1, accepted1, rejected1}

	cases := []struct {
		name     string
		query    string
		store    *fakeProposalStore
		wantCode int
		wantLen  int
	}{
		{
			name:     "no status → defaults to pending",
			store:    &fakeProposalStore{byID: map[uuid.UUID]*db.PendingProposal{}, all: allRows},
			wantCode: http.StatusOK,
			wantLen:  1, // only pending1
		},
		{
			name:     "status=pending → only pending",
			query:    "?status=pending",
			store:    &fakeProposalStore{byID: map[uuid.UUID]*db.PendingProposal{}, all: allRows},
			wantCode: http.StatusOK,
			wantLen:  1,
		},
		{
			name:     "status=accepted → only accepted",
			query:    "?status=accepted",
			store:    &fakeProposalStore{byID: map[uuid.UUID]*db.PendingProposal{}, all: allRows},
			wantCode: http.StatusOK,
			wantLen:  1,
		},
		{
			name:     "status=rejected → only rejected",
			query:    "?status=rejected",
			store:    &fakeProposalStore{byID: map[uuid.UUID]*db.PendingProposal{}, all: allRows},
			wantCode: http.StatusOK,
			wantLen:  1,
		},
		{
			name:     "status=all → all proposals",
			query:    "?status=all",
			store:    &fakeProposalStore{byID: map[uuid.UUID]*db.PendingProposal{}, all: allRows},
			wantCode: http.StatusOK,
			wantLen:  3,
		},
		{
			name:     "invalid status → 400",
			query:    "?status=invalid",
			store:    &fakeProposalStore{byID: map[uuid.UUID]*db.PendingProposal{}, all: allRows},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "store error → 500",
			query:    "?status=all",
			store:    &fakeProposalStore{byID: map[uuid.UUID]*db.PendingProposal{}, listAllErr: errors.New("db error")},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:     "invalid type → 400",
			query:    "?type=invalid",
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
					t.Fatalf("response not JSON array: %v", err)
				}
				if len(items) != tc.wantLen {
					t.Errorf("got %d items, want %d", len(items), tc.wantLen)
				}
			}
		})
	}
}

// ---- ListPendingProposals tests ----

func TestProposalHandler_ListPendingProposals(t *testing.T) {
	p1 := db.PendingProposal{
		ID:        uuid.New(),
		Type:      "goal",
		Status:    "pending",
		Payload:   []byte(`{}`),
		CreatedAt: pgtype.Timestamptz{Valid: false},
	}
	p2 := db.PendingProposal{
		ID:        uuid.New(),
		Type:      "concept",
		Status:    "pending",
		Payload:   []byte(`{}`),
		CreatedAt: pgtype.Timestamptz{Valid: false},
	}

	cases := []struct {
		name     string
		query    string
		store    *fakeProposalStore
		wantCode int
		wantLen  int
	}{
		{
			name:     "returns all pending",
			store:    newFakeProposalStore(p1, p2),
			wantCode: http.StatusOK,
			wantLen:  2,
		},
		{
			name:     "type filter: concept only",
			query:    "?type=concept",
			store:    newFakeProposalStore(p1, p2),
			wantCode: http.StatusOK,
			wantLen:  1,
		},
		{
			name:     "empty list → 200",
			store:    newFakeProposalStore(),
			wantCode: http.StatusOK,
			wantLen:  0,
		},
		{
			name:     "store error → 500",
			store:    &fakeProposalStore{listErr: errors.New("db error")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewProposalHandler(tc.store, &fakeProposalLearningStore{})
			e.GET("/api/proposals/pending", h.ListPendingProposals)
			rec := performRequest(e, http.MethodGet, "/api/proposals/pending"+tc.query, "")
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
				return
			}
			if tc.wantCode == http.StatusOK {
				var items []json.RawMessage
				if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
					t.Fatalf("response not JSON array: %v", err)
				}
				if len(items) != tc.wantLen {
					t.Errorf("got %d items, want %d", len(items), tc.wantLen)
				}
			}
		})
	}
}
