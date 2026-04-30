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
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

// fakeLearningStore implements the learningStore interface for testing.
type fakeLearningStore struct {
	reviews []learning.DueReview
	concept *db.Concept
	err     error
}

// fakeSuggestionDecisionStore implements suggestionDecisionStore for testing.
type fakeSuggestionDecisionStore struct {
	decisions []db.Decision
	err       error
}

func (f *fakeSuggestionDecisionStore) All(_ context.Context, _ int32) ([]db.Decision, error) {
	return f.decisions, f.err
}

func (f *fakeLearningStore) DueReviews(_ context.Context, _ int) ([]learning.DueReview, error) {
	return f.reviews, f.err
}

func (f *fakeLearningStore) SubmitReview(
	_ context.Context, _ uuid.UUID, _ learning.CardState, _ learning.Rating,
) error {
	return f.err
}

func (f *fakeLearningStore) CreateConcept(_ context.Context, _, _ string, _ []string) (*db.Concept, error) {
	return f.concept, f.err
}

// ---- GetDueReviews tests ----

func TestLearningHandler_GetDueReviews(t *testing.T) {
	review := learning.DueReview{
		ConceptID:   uuid.New(),
		ScheduleID:  uuid.New(),
		Title:       "FSRS algorithm",
		ReviewCount: 0,
	}
	cases := []struct {
		name     string
		store    *fakeLearningStore
		wantCode int
	}{
		{
			name:     "returns due reviews",
			store:    &fakeLearningStore{reviews: []learning.DueReview{review}},
			wantCode: http.StatusOK,
		},
		{
			name:     "empty list → 200",
			store:    &fakeLearningStore{reviews: []learning.DueReview{}},
			wantCode: http.StatusOK,
		},
		{
			name:     "store error → 500",
			store:    &fakeLearningStore{err: errors.New("db error")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			h := handler.NewLearningHandler(tc.store)
			e.GET("/api/learning/reviews", h.GetDueReviews)
			rec := performRequest(e, http.MethodGet, "/api/learning/reviews", "")
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// ---- SubmitReview tests ----

func TestLearningHandler_SubmitReview(t *testing.T) {
	id := uuid.New()
	cases := []struct {
		name     string
		paramID  string
		body     string
		store    *fakeLearningStore
		wantCode int
	}{
		{
			name:     "submits review",
			paramID:  id.String(),
			body:     `{"rating":3,"stability":2.5,"difficulty":5.0,"review_count":3}`,
			store:    &fakeLearningStore{},
			wantCode: http.StatusOK,
		},
		{
			name:     "invalid UUID → 400",
			paramID:  "not-a-uuid",
			body:     `{"rating":3}`,
			store:    &fakeLearningStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "rating 0 → 400",
			paramID:  id.String(),
			body:     `{"rating":0}`,
			store:    &fakeLearningStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "rating 5 → 400",
			paramID:  id.String(),
			body:     `{"rating":5}`,
			store:    &fakeLearningStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "schedule not found → 404",
			paramID:  id.String(),
			body:     `{"rating":3}`,
			store:    &fakeLearningStore{err: learning.ErrNotFound},
			wantCode: http.StatusNotFound,
		},
		{
			name:     "store error → 500",
			paramID:  id.String(),
			body:     `{"rating":3}`,
			store:    &fakeLearningStore{err: errors.New("db error")},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:     "invalid JSON → 400",
			paramID:  id.String(),
			body:     `{bad`,
			store:    &fakeLearningStore{},
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			h := handler.NewLearningHandler(tc.store)
			e.POST("/api/learning/reviews/:id/submit", h.SubmitReview)
			rec := performRequest(e, http.MethodPost, "/api/learning/reviews/"+tc.paramID+"/submit", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// ---- CreateConcept tests ----

func TestLearningHandler_CreateConcept(t *testing.T) {
	concept := &db.Concept{ID: uuid.New(), Title: "Spaced Repetition", Content: "Review at optimal intervals"}
	cases := []struct {
		name     string
		body     string
		store    *fakeLearningStore
		wantCode int
	}{
		{
			name:     "creates concept",
			body:     `{"title":"Spaced Repetition","content":"Review at optimal intervals"}`,
			store:    &fakeLearningStore{concept: concept},
			wantCode: http.StatusCreated,
		},
		{
			name:     "creates concept with tags",
			body:     `{"title":"FSRS","content":"Free Spaced Repetition Scheduler","tags":["memory","learning"]}`,
			store:    &fakeLearningStore{concept: concept},
			wantCode: http.StatusCreated,
		},
		{
			name:     "missing title → 400",
			body:     `{"content":"some content"}`,
			store:    &fakeLearningStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "missing content → 400",
			body:     `{"title":"Some Concept"}`,
			store:    &fakeLearningStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid JSON → 400",
			body:     `{bad`,
			store:    &fakeLearningStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "store error → 500",
			body:     `{"title":"FSRS","content":"Algorithm"}`,
			store:    &fakeLearningStore{err: errors.New("db error")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			h := handler.NewLearningHandler(tc.store)
			e.POST("/api/learning/concepts", h.CreateConcept)
			rec := performRequest(e, http.MethodPost, "/api/learning/concepts", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// ---- GetSuggestions tests ----

func TestLearningHandler_GetSuggestions(t *testing.T) {
	highValueItem := db.KnowledgeItem{
		ID:            uuid.New(),
		Title:         "FSRS Algorithm",
		Content:       "Spaced repetition scheduling",
		Tags:          []string{"learning"},
		LearningValue: pgtype.Int4{Int32: 4, Valid: true},
	}
	lowValueItem := db.KnowledgeItem{
		ID:            uuid.New(),
		Title:         "Random note",
		Content:       "Something trivial",
		LearningValue: pgtype.Int4{Int32: 1, Valid: true},
	}
	noValueItem := db.KnowledgeItem{
		ID:            uuid.New(),
		Title:         "No value set",
		LearningValue: pgtype.Int4{Valid: false},
	}
	recentDecision := db.Decision{
		ID:      uuid.New(),
		Title:   "Use Echo framework",
		Context: "Need HTTP router for Go service",
	}

	cases := []struct {
		name           string
		knowledgeItems []db.KnowledgeItem
		decisions      []db.Decision
		knowledgeErr   error
		decisionErr    error
		wantCode       int
		checkBody      func(t *testing.T, body []byte)
	}{
		{
			name:           "returns filtered high-value items and decisions",
			knowledgeItems: []db.KnowledgeItem{highValueItem, lowValueItem, noValueItem},
			decisions:      []db.Decision{recentDecision},
			wantCode:       http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var resp map[string]json.RawMessage
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				var items []map[string]json.RawMessage
				if err := json.Unmarshal(resp["knowledge_items"], &items); err != nil {
					t.Fatalf("invalid knowledge_items: %v", err)
				}
				if len(items) != 1 {
					t.Errorf("want 1 knowledge item (learning_value>=2), got %d", len(items))
				}
				var decisions []map[string]json.RawMessage
				if err := json.Unmarshal(resp["decisions"], &decisions); err != nil {
					t.Fatalf("invalid decisions: %v", err)
				}
				if len(decisions) != 1 {
					t.Errorf("want 1 decision, got %d", len(decisions))
				}
			},
		},
		{
			name:           "empty knowledge → empty arrays not null",
			knowledgeItems: []db.KnowledgeItem{},
			decisions:      []db.Decision{},
			wantCode:       http.StatusOK,
			checkBody: func(t *testing.T, body []byte) {
				t.Helper()
				var resp struct {
					KnowledgeItems []json.RawMessage `json:"knowledge_items"`
					Decisions      []json.RawMessage `json:"decisions"`
				}
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				if resp.KnowledgeItems == nil {
					t.Error("knowledge_items must be [] not null")
				}
				if resp.Decisions == nil {
					t.Error("decisions must be [] not null")
				}
			},
		},
		{
			name:         "knowledge store error → 500",
			knowledgeErr: errors.New("db error"),
			wantCode:     http.StatusInternalServerError,
		},
		{
			name:           "decision store error → 500",
			knowledgeItems: []db.KnowledgeItem{},
			decisionErr:    errors.New("db error"),
			wantCode:       http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			kStore := &fakeKnowledgeStore{items: tc.knowledgeItems, err: tc.knowledgeErr}
			dStore := &fakeSuggestionDecisionStore{decisions: tc.decisions, err: tc.decisionErr}
			h := handler.NewLearningHandler(
				&fakeLearningStore{},
				handler.WithKnowledgeStore(kStore),
				handler.WithDecisionStore(dStore),
			)
			e.GET("/api/learning/suggestions", h.GetSuggestions)
			rec := performRequest(e, http.MethodGet, "/api/learning/suggestions", "")
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.checkBody != nil && rec.Code == http.StatusOK {
				tc.checkBody(t, rec.Body.Bytes())
			}
		})
	}
}

// ---- CreateConceptFromKnowledge tests ----

func TestLearningHandler_CreateConceptFromKnowledge(t *testing.T) {
	knowledgeID := uuid.New()
	concept := &db.Concept{ID: uuid.New(), Title: "FSRS Algorithm", Content: "Spaced repetition"}
	knowledgeItem := &db.KnowledgeItem{
		ID:      knowledgeID,
		Title:   "FSRS Algorithm",
		Content: "Spaced repetition scheduling",
		Tags:    []string{"learning"},
	}

	cases := []struct {
		name          string
		body          string
		knowledgeItem *db.KnowledgeItem
		concept       *db.Concept
		getErr        error
		createErr     error
		wantCode      int
	}{
		{
			name:          "creates concept from knowledge item",
			body:          `{"knowledge_id":"` + knowledgeID.String() + `"}`,
			knowledgeItem: knowledgeItem,
			concept:       concept,
			wantCode:      http.StatusCreated,
		},
		{
			name:     "invalid UUID → 400",
			body:     `{"knowledge_id":"not-a-uuid"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid JSON → 400",
			body:     `{bad`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "knowledge item not found → 404",
			body:     `{"knowledge_id":"` + knowledgeID.String() + `"}`,
			getErr:   knowledge.ErrNotFound,
			wantCode: http.StatusNotFound,
		},
		{
			name:          "store create error → 500",
			body:          `{"knowledge_id":"` + knowledgeID.String() + `"}`,
			knowledgeItem: knowledgeItem,
			createErr:     errors.New("db write fail"),
			wantCode:      http.StatusInternalServerError,
		},
		{
			name:     "knowledge GetByID error → 500",
			body:     `{"knowledge_id":"` + knowledgeID.String() + `"}`,
			getErr:   errors.New("db error"),
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			kStore := &fakeKnowledgeStore{item: tc.knowledgeItem, getErr: tc.getErr}
			lStore := &fakeLearningStore{concept: tc.concept, err: tc.createErr}
			h := handler.NewLearningHandler(lStore, handler.WithKnowledgeStore(kStore))
			e.POST("/api/learning/from-knowledge", h.CreateConceptFromKnowledge)
			rec := performRequest(e, http.MethodPost, "/api/learning/from-knowledge", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}
