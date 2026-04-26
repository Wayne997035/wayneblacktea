package handler_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/waynechen/wayneblacktea/internal/db"
	"github.com/waynechen/wayneblacktea/internal/handler"
	"github.com/waynechen/wayneblacktea/internal/learning"
)

// fakeLearningStore implements the learningStore interface for testing.
type fakeLearningStore struct {
	reviews []learning.DueReview
	concept *db.Concept
	err     error
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
