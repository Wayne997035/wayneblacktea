package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
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

// ---- TASK 3: TypeTask materialise + validator tests ----

// fakeProposalTaskStore is the narrow fake for proposalTaskStore.
type fakeProposalTaskStore struct {
	mu        sync.Mutex
	created   []gtd.CreateTaskParams
	createErr error
}

func (f *fakeProposalTaskStore) CreateTask(_ context.Context, p gtd.CreateTaskParams) (*db.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.created = append(f.created, p)
	return &db.Task{ID: uuid.New(), Title: p.Title}, nil
}

func (f *fakeProposalTaskStore) snapshot() []gtd.CreateTaskParams {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]gtd.CreateTaskParams, len(f.created))
	copy(out, f.created)
	return out
}

// makeTaskProposal builds a pending TypeTask proposal for tests.
func makeTaskProposal(id uuid.UUID, title, description, kind string) db.PendingProposal {
	payload, _ := json.Marshal(proposal.TaskPayload{
		Title:         title,
		Description:   description,
		SuggestedKind: kind,
		SourceTool:    "test",
	})
	return db.PendingProposal{
		ID:      id,
		Type:    string(proposal.TypeTask),
		Status:  string(proposal.StatusPending),
		Payload: payload,
	}
}

// vagueDescriptionFixture is the canonical vague description used across the
// TypeTask validator tests — "TBD" hits validator.vaguenessMarkers.
const vagueDescriptionFixture = "TBD"

// confirmTaskProposal runs a single confirm_proposal request against a fresh
// handler wired with the supplied stores; returns the recorder + parsed body.
func confirmTaskProposal(
	t *testing.T, store *fakeProposalStore, taskStore *fakeProposalTaskStore, id uuid.UUID,
) (code int, body map[string]any, raw string) {
	t.Helper()
	e := newEcho()
	h := handler.NewProposalHandler(store, &fakeProposalLearningStore{}).WithTask(taskStore)
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+id.String()+"/confirm", `{"action":"accept"}`)
	raw = rec.Body.String()
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body, raw
}

// TestConfirmProposal_TypeTask_RunsVagueness_WarnMode: warn mode (default env)
// returns 200 with warnings in body AND task is materialised.
func TestConfirmProposal_TypeTask_RunsVagueness_WarnMode(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", "")
	id := uuid.New()
	prop := makeTaskProposal(id, "Add audit log", vagueDescriptionFixture, "general")
	store := newFakeProposalStore(prop)
	taskStore := &fakeProposalTaskStore{}

	code, body, raw := confirmTaskProposal(t, store, taskStore, id)
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", code, raw)
	}
	warnings, ok := body["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Errorf("expected non-empty warnings, body=%s", raw)
	}
	if len(taskStore.snapshot()) != 1 {
		t.Errorf("expected 1 task created in warn mode, got %d", len(taskStore.snapshot()))
	}
}

// TestConfirmProposal_TypeTask_RunsVagueness_StrictMode: strict mode returns
// 400 with warnings, NO task created, proposal stays pending.
func TestConfirmProposal_TypeTask_RunsVagueness_StrictMode(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", "true")
	id := uuid.New()
	prop := makeTaskProposal(id, "Add audit log", vagueDescriptionFixture, "general")
	store := newFakeProposalStore(prop)
	taskStore := &fakeProposalTaskStore{}

	code, body, raw := confirmTaskProposal(t, store, taskStore, id)
	if code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", code, raw)
	}
	warnings, ok := body["warnings"].([]any)
	if !ok || len(warnings) == 0 {
		t.Errorf("expected non-empty warnings, body=%s", raw)
	}
	if len(taskStore.snapshot()) != 0 {
		t.Errorf("expected 0 tasks in strict mode, got %d", len(taskStore.snapshot()))
	}
	if len(store.resolved) != 0 {
		t.Errorf("expected proposal to stay pending, got resolved=%v", store.resolved)
	}
}

// TestConfirmProposal_TypeTask_RunsVagueness_NoWarnings: non-vague description
// passes validator → 200, no warnings array, task is created with full
// description preserved.
func TestConfirmProposal_TypeTask_RunsVagueness_NoWarnings(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", "")
	id := uuid.New()
	const goodDescription = "Add slog.Info audit row to confirm_proposal at internal/handler/proposal_handler.go:240 with proposal_id"
	prop := makeTaskProposal(id, "Audit log proposal", goodDescription, "general")
	store := newFakeProposalStore(prop)
	taskStore := &fakeProposalTaskStore{}

	code, body, raw := confirmTaskProposal(t, store, taskStore, id)
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", code, raw)
	}
	if warnings, ok := body["warnings"].([]any); ok && len(warnings) > 0 {
		t.Errorf("expected no warnings, got %v", warnings)
	}
	creates := taskStore.snapshot()
	if len(creates) != 1 {
		t.Fatalf("expected 1 task created, got %d", len(creates))
	}
	if creates[0].Title != "Audit log proposal" {
		t.Errorf("title = %q", creates[0].Title)
	}
	if creates[0].Description != goodDescription {
		t.Errorf("description = %q", creates[0].Description)
	}
}

// TestConfirmProposal_TypeTask_MalformedPayload: malformed JSON → 400, no task.
func TestConfirmProposal_TypeTask_MalformedPayload(t *testing.T) {
	id := uuid.New()
	prop := db.PendingProposal{
		ID:      id,
		Type:    string(proposal.TypeTask),
		Status:  string(proposal.StatusPending),
		Payload: []byte(`{bad json`),
	}
	store := newFakeProposalStore(prop)
	taskStore := &fakeProposalTaskStore{}

	code, _, raw := confirmTaskProposal(t, store, taskStore, id)
	if code != http.StatusBadRequest {
		t.Errorf("want 400, got %d: %s", code, raw)
	}
	if len(taskStore.snapshot()) != 0 {
		t.Errorf("expected 0 tasks on malformed payload, got %d", len(taskStore.snapshot()))
	}
}

// batchConfirmTypeTaskResp is the narrow JSON shape exercised by the
// batch-confirm TypeTask round-1 follow-up tests.
type batchConfirmTypeTaskResp struct {
	Results []struct {
		ID      string `json:"id"`
		OK      bool   `json:"ok"`
		Skipped bool   `json:"skipped"`
		Error   string `json:"error"`
	} `json:"results"`
}

// runBatchConfirmTypeTask wires a fresh handler + store, POSTs a single-ID
// confirm-batch accept request, decodes the response, and returns it
// alongside the seed store/taskStore for further assertions. Keeps the
// per-case TestProposalHandler_ConfirmBatch_TypeTask_* tests below short
// enough to stay under gocyclo:15 (combined version tripped 22).
func runBatchConfirmTypeTask(
	t *testing.T, prop db.PendingProposal, id uuid.UUID,
) (*fakeProposalStore, *fakeProposalTaskStore, batchConfirmTypeTaskResp) {
	t.Helper()
	store := newFakeProposalStore(prop)
	taskStore := &fakeProposalTaskStore{}

	e := newEcho()
	h := handler.NewProposalHandler(store, &fakeProposalLearningStore{}).WithTask(taskStore)
	e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
	body := `{"ids":["` + id.String() + `"],"action":"accept"}`
	rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp batchConfirmTypeTaskResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response not valid JSON: %v (body: %s)", err, rec.Body.String())
	}
	if len(resp.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(resp.Results))
	}
	return store, taskStore, resp
}

// TestProposalHandler_ConfirmBatch_TypeTask_Materialises (round-1 follow-up
// GTD 95bd2a09): a clean TypeTask payload accepted via the batch endpoint
// inserts a task row and resolves the proposal as accepted.
func TestProposalHandler_ConfirmBatch_TypeTask_Materialises(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", "")
	id := uuid.New()
	const goodDescription = "Add slog.Info audit row to confirm_proposal at internal/handler/proposal_handler.go:240 with proposal_id"
	prop := makeTaskProposal(id, "Audit log proposal", goodDescription, "general")

	store, taskStore, resp := runBatchConfirmTypeTask(t, prop, id)
	if !resp.Results[0].OK {
		t.Errorf("result.OK = false, want true (error=%q)", resp.Results[0].Error)
	}
	if resp.Results[0].Error != "" {
		t.Errorf("unexpected entry.Error = %q", resp.Results[0].Error)
	}
	if creates := taskStore.snapshot(); len(creates) != 1 {
		t.Errorf("expected 1 task created, got %d", len(creates))
	}
	if store.resolvedAs != proposal.StatusAccepted {
		t.Errorf("resolvedAs = %q, want accepted", store.resolvedAs)
	}
}

// TestProposalHandler_ConfirmBatch_TypeTask_MalformedPayload (round-1
// follow-up GTD 95bd2a09): garbage JSON in payload surfaces "task proposal
// payload is malformed" as entry.Error, leaves the proposal pending, and
// inserts no task row.
func TestProposalHandler_ConfirmBatch_TypeTask_MalformedPayload(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", "")
	id := uuid.New()
	prop := db.PendingProposal{
		ID:      id,
		Type:    string(proposal.TypeTask),
		Status:  string(proposal.StatusPending),
		Payload: []byte(`{not valid json`),
	}

	store, taskStore, resp := runBatchConfirmTypeTask(t, prop, id)
	if resp.Results[0].OK {
		t.Errorf("result.OK = true, want false on malformed payload")
	}
	const wantSubstr = "task proposal payload is malformed"
	if !strings.Contains(resp.Results[0].Error, wantSubstr) {
		t.Errorf("entry.Error = %q, want substring %q", resp.Results[0].Error, wantSubstr)
	}
	if len(taskStore.snapshot()) != 0 {
		t.Errorf("expected 0 tasks on malformed payload, got %d", len(taskStore.snapshot()))
	}
	if len(store.resolved) != 0 {
		t.Errorf("expected proposal to stay pending, got resolved=%v", store.resolved)
	}
}

// TestProposalHandler_ConfirmBatch_TypeTask_StrictVague (round-1 follow-up
// GTD 95bd2a09): WBT_STRICT_VAGUENESS=1 + a vague description blocks task
// materialisation, surfaces "vagueness check failed" in entry.Error, and
// keeps the proposal pending.
func TestProposalHandler_ConfirmBatch_TypeTask_StrictVague(t *testing.T) {
	t.Setenv("WBT_STRICT_VAGUENESS", "1")
	id := uuid.New()
	prop := makeTaskProposal(id, "Audit log proposal", vagueDescriptionFixture, "general")

	store, taskStore, resp := runBatchConfirmTypeTask(t, prop, id)
	if resp.Results[0].OK {
		t.Errorf("result.OK = true, want false on strict-mode vagueness")
	}
	const wantSubstr = "vagueness check failed"
	if !strings.Contains(resp.Results[0].Error, wantSubstr) {
		t.Errorf("entry.Error = %q, want substring %q", resp.Results[0].Error, wantSubstr)
	}
	if len(taskStore.snapshot()) != 0 {
		t.Errorf("expected 0 tasks in strict mode, got %d", len(taskStore.snapshot()))
	}
	if len(store.resolved) != 0 {
		t.Errorf("expected proposal to stay pending, got resolved=%v", store.resolved)
	}
}

// TestPendingProposalResponse_ReasonField (round-2 follow-up N-1) verifies
// that pending_proposals.reason (migration 000051) round-trips through the
// API response when present, and is omitted entirely when NULL.
//
// Drives GET /api/proposals?status=all (the response path uses toResponse()
// just like /api/proposals/pending and /api/proposals/:id/confirm), then
// asserts:
//   - row with reason="ttl-expired-30d" → JSON includes `"reason":"ttl-expired-30d"`
//   - row with NULL reason → JSON object has no `reason` key at all
func TestPendingProposalResponse_ReasonField(t *testing.T) {
	rowWithReason := db.PendingProposal{
		ID:      uuid.New(),
		Type:    "concept",
		Status:  "rejected",
		Payload: []byte(`{}`),
		Reason:  pgtype.Text{String: "ttl-expired-30d", Valid: true},
	}
	rowWithoutReason := db.PendingProposal{
		ID:      uuid.New(),
		Type:    "concept",
		Status:  "rejected",
		Payload: []byte(`{}`),
		Reason:  pgtype.Text{Valid: false},
	}

	cases := []struct {
		name      string
		row       db.PendingProposal
		wantKey   bool
		wantValue string
	}{
		{
			name:      "non-null reason → field present in JSON",
			row:       rowWithReason,
			wantKey:   true,
			wantValue: "ttl-expired-30d",
		},
		{
			name:    "null reason → field omitted (omitempty)",
			row:     rowWithoutReason,
			wantKey: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeProposalStore{
				byID: map[uuid.UUID]*db.PendingProposal{tc.row.ID: &tc.row},
				all:  []db.PendingProposal{tc.row},
			}
			e := newEcho()
			h := handler.NewProposalHandler(store, &fakeProposalLearningStore{})
			e.GET("/api/proposals", h.ListProposals)
			rec := performRequest(e, http.MethodGet, "/api/proposals?status=all", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("got status %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			// Decode into []map[string]any so we can check key presence directly
			// (this is the spec — Reason is `*string` with `omitempty`, so a nil
			// Reason MUST omit the key entirely, not emit `"reason":null`).
			var items []map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
				t.Fatalf("response not valid JSON array: %v (body: %s)", err, rec.Body.String())
			}
			if len(items) != 1 {
				t.Fatalf("got %d items, want 1", len(items))
			}
			val, hasKey := items[0]["reason"]
			if hasKey != tc.wantKey {
				t.Errorf("reason key present = %v, want %v (body: %s)", hasKey, tc.wantKey, rec.Body.String())
			}
			if tc.wantKey {
				got, ok := val.(string)
				if !ok {
					t.Errorf("reason value type = %T, want string", val)
				} else if got != tc.wantValue {
					t.Errorf("reason value = %q, want %q", got, tc.wantValue)
				}
			}
		})
	}
}

// TestConfirmProposal_TypeTask_MissingTitle: payload without title → 400.
func TestConfirmProposal_TypeTask_MissingTitle(t *testing.T) {
	id := uuid.New()
	payload, _ := json.Marshal(proposal.TaskPayload{Description: "desc"})
	prop := db.PendingProposal{
		ID:      id,
		Type:    string(proposal.TypeTask),
		Status:  string(proposal.StatusPending),
		Payload: payload,
	}
	store := newFakeProposalStore(prop)
	taskStore := &fakeProposalTaskStore{}

	code, _, raw := confirmTaskProposal(t, store, taskStore, id)
	if code != http.StatusBadRequest {
		t.Errorf("want 400, got %d: %s", code, raw)
	}
}
