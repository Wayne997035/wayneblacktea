package handler_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// fakeKnowledgeStore implements the knowledgeStore interface for testing.
type fakeKnowledgeStore struct {
	item   *db.KnowledgeItem
	items  []db.KnowledgeItem
	err    error
	getErr error // separate error for GetByID; if nil, falls back to err
}

func (f *fakeKnowledgeStore) AddItem(_ context.Context, _ knowledge.AddItemParams) (*db.KnowledgeItem, error) {
	return f.item, f.err
}

func (f *fakeKnowledgeStore) Search(_ context.Context, _ string, _ int) ([]db.KnowledgeItem, error) {
	return f.items, f.err
}

func (f *fakeKnowledgeStore) List(_ context.Context, _, _ int) ([]db.KnowledgeItem, error) {
	return f.items, f.err
}

func (f *fakeKnowledgeStore) GetByID(_ context.Context, _ uuid.UUID) (*db.KnowledgeItem, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.item, f.err
}

func (f *fakeKnowledgeStore) UpdateLearningValue(_ context.Context, _ uuid.UUID, _ int) error {
	return f.err
}

// buildTagsBody builds a JSON body for AddKnowledge with n tags named "tag0" … "tag(n-1)".
func buildTagsBody(n int) string {
	tags := make([]string, n)
	for i := range tags {
		tags[i] = `"tag` + strings.Repeat("x", i%10) + `"`
	}
	return `{"type":"til","title":"t","tags":[` + strings.Join(tags, ",") + `]}`
}

// ---- ListKnowledge tests ----

func TestKnowledgeHandler_ListKnowledge(t *testing.T) {
	item := db.KnowledgeItem{ID: uuid.New(), Type: "til", Title: "Go generics"}
	cases := []struct {
		name     string
		query    string
		store    *fakeKnowledgeStore
		wantCode int
	}{
		{
			name:     "returns items",
			query:    "",
			store:    &fakeKnowledgeStore{items: []db.KnowledgeItem{item}},
			wantCode: http.StatusOK,
		},
		{
			name:     "empty list → 200",
			query:    "",
			store:    &fakeKnowledgeStore{items: []db.KnowledgeItem{}},
			wantCode: http.StatusOK,
		},
		{
			name:     "store error → 500",
			query:    "",
			store:    &fakeKnowledgeStore{err: errors.New("db error")},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:     "custom limit → 200",
			query:    "?limit=5&offset=10",
			store:    &fakeKnowledgeStore{items: []db.KnowledgeItem{}},
			wantCode: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			h := handler.NewKnowledgeHandler(tc.store, nil)
			e.GET("/api/knowledge", h.ListKnowledge)
			rec := performRequest(e, http.MethodGet, "/api/knowledge"+tc.query, "")
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// ---- AddKnowledge tests ----

func TestKnowledgeHandler_AddKnowledge(t *testing.T) {
	item := &db.KnowledgeItem{ID: uuid.New(), Type: "til", Title: "Go generics"}
	cases := []struct {
		name     string
		body     string
		store    *fakeKnowledgeStore
		wantCode int
	}{
		{
			name:     "creates item",
			body:     `{"type":"til","title":"Go generics","content":"Type params since Go 1.18"}`,
			store:    &fakeKnowledgeStore{item: item},
			wantCode: http.StatusCreated,
		},
		{
			name:     "missing type → 400",
			body:     `{"title":"Go generics"}`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "missing title → 400",
			body:     `{"type":"til"}`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid JSON → 400",
			body:     `{invalid`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "store error → 500",
			body:     `{"type":"til","title":"Go generics"}`,
			store:    &fakeKnowledgeStore{err: errors.New("db write fail")},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:     "with tags and URL",
			body:     `{"type":"article","title":"Go 1.21","url":"https://go.dev","tags":["go","release"]}`,
			store:    &fakeKnowledgeStore{item: item},
			wantCode: http.StatusCreated,
		},
		{
			name:     "duplicate content → 409",
			body:     `{"type":"til","title":"Go generics"}`,
			store:    &fakeKnowledgeStore{err: knowledge.ErrDuplicate{ExistingTitle: "Go generics", Similarity: 0.95}},
			wantCode: http.StatusConflict,
		},
		// --- title length boundary ---
		{
			name:     "title exactly 200 chars → 201",
			body:     `{"type":"til","title":"` + strings.Repeat("a", 200) + `"}`,
			store:    &fakeKnowledgeStore{item: item},
			wantCode: http.StatusCreated,
		},
		{
			name:     "title 201 chars → 400",
			body:     `{"type":"til","title":"` + strings.Repeat("a", 201) + `"}`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
		// --- content size boundary (1 MB — matches global BodyLimit) ---
		{
			name:     "content exactly 1 MB → 201",
			body:     `{"type":"til","title":"big","content":"` + strings.Repeat("a", 1*1024*1024) + `"}`,
			store:    &fakeKnowledgeStore{item: item},
			wantCode: http.StatusCreated,
		},
		{
			name:     "content 1 MB + 1 byte → 400",
			body:     `{"type":"til","title":"big","content":"` + strings.Repeat("a", 1*1024*1024+1) + `"}`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
		// --- URL validation ---
		{
			name:     "valid https URL → 201",
			body:     `{"type":"bookmark","title":"Go","url":"https://example.com"}`,
			store:    &fakeKnowledgeStore{item: item},
			wantCode: http.StatusCreated,
		},
		{
			name:     "valid http URL → 201",
			body:     `{"type":"bookmark","title":"Go","url":"http://example.com"}`,
			store:    &fakeKnowledgeStore{item: item},
			wantCode: http.StatusCreated,
		},
		{
			name:     "ftp URL → 400",
			body:     `{"type":"bookmark","title":"Go","url":"ftp://example.com"}`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "relative URL → 400",
			body:     `{"type":"bookmark","title":"Go","url":"/relative/path"}`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
		// --- tags count boundary ---
		{
			name:     "20 tags → 201",
			body:     buildTagsBody(20),
			store:    &fakeKnowledgeStore{item: item},
			wantCode: http.StatusCreated,
		},
		{
			name:     "21 tags → 400",
			body:     buildTagsBody(21),
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
		// --- tag length boundary ---
		{
			name:     "tag exactly 50 chars → 201",
			body:     `{"type":"til","title":"t","tags":["` + strings.Repeat("a", 50) + `"]}`,
			store:    &fakeKnowledgeStore{item: item},
			wantCode: http.StatusCreated,
		},
		{
			name:     "tag 51 chars → 400",
			body:     `{"type":"til","title":"t","tags":["` + strings.Repeat("a", 51) + `"]}`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			h := handler.NewKnowledgeHandler(tc.store, nil)
			e.POST("/api/knowledge", h.AddKnowledge)
			rec := performRequest(e, http.MethodPost, "/api/knowledge", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// ---- SearchKnowledge tests ----

func TestKnowledgeHandler_SearchKnowledge(t *testing.T) {
	item := db.KnowledgeItem{ID: uuid.New(), Type: "article", Title: "Go performance"}
	cases := []struct {
		name     string
		query    string
		store    *fakeKnowledgeStore
		wantCode int
	}{
		{
			name:     "returns results",
			query:    "?q=go+performance",
			store:    &fakeKnowledgeStore{items: []db.KnowledgeItem{item}},
			wantCode: http.StatusOK,
		},
		{
			name:     "missing q → 400",
			query:    "",
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "empty q → 400",
			query:    "?q=",
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "store error → 500",
			query:    "?q=go",
			store:    &fakeKnowledgeStore{err: errors.New("fts error")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			h := handler.NewKnowledgeHandler(tc.store, nil)
			e.GET("/api/knowledge/search", h.SearchKnowledge)
			rec := performRequest(e, http.MethodGet, "/api/knowledge/search"+tc.query, "")
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// ---- UpdateLearningValue tests ----

func TestKnowledgeHandler_UpdateLearningValue(t *testing.T) {
	validID := uuid.New().String()
	cases := []struct {
		name     string
		paramID  string
		body     string
		store    *fakeKnowledgeStore
		wantCode int
	}{
		{
			name:     "valid value 1 → 200",
			paramID:  validID,
			body:     `{"learning_value":1}`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusOK,
		},
		{
			name:     "valid value 5 → 200",
			paramID:  validID,
			body:     `{"learning_value":5}`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusOK,
		},
		{
			name:     "valid value 3 → 200",
			paramID:  validID,
			body:     `{"learning_value":3}`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusOK,
		},
		{
			name:     "value 0 (below range) → 400",
			paramID:  validID,
			body:     `{"learning_value":0}`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "value 6 (above range) → 400",
			paramID:  validID,
			body:     `{"learning_value":6}`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "negative value → 400",
			paramID:  validID,
			body:     `{"learning_value":-1}`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid UUID → 400",
			paramID:  "not-a-uuid",
			body:     `{"learning_value":3}`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "not found → 404",
			paramID:  validID,
			body:     `{"learning_value":3}`,
			store:    &fakeKnowledgeStore{err: knowledge.ErrNotFound},
			wantCode: http.StatusNotFound,
		},
		{
			name:     "store error → 500",
			paramID:  validID,
			body:     `{"learning_value":3}`,
			store:    &fakeKnowledgeStore{err: errors.New("db down")},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:     "invalid JSON → 400",
			paramID:  validID,
			body:     `{bad`,
			store:    &fakeKnowledgeStore{},
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewKnowledgeHandler(tc.store, nil)
			e.PATCH("/api/knowledge/:id", h.UpdateLearningValue)
			rec := performRequest(e, http.MethodPatch, "/api/knowledge/"+tc.paramID, tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}
