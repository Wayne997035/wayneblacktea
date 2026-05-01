package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

const (
	entityKnowledge = "knowledge"
	entityDecision  = "decision"
	entityTask      = "task"
)

// ---- fake stores for SearchHandler ----

type fakeSearchKnowledgeStore struct {
	items []db.KnowledgeItem
	err   error
}

func (f *fakeSearchKnowledgeStore) Search(_ context.Context, _ string, _ int) ([]db.KnowledgeItem, error) {
	return f.items, f.err
}

type fakeSearchDecisionStore struct {
	list []db.Decision
	err  error
}

func (f *fakeSearchDecisionStore) All(_ context.Context, _ int32) ([]db.Decision, error) {
	return f.list, f.err
}

type fakeSearchGTDStore struct {
	tasks []db.Task
	err   error
}

func (f *fakeSearchGTDStore) Tasks(_ context.Context, _ *uuid.UUID) ([]db.Task, error) {
	return f.tasks, f.err
}

// ---- helpers ----

func newSearchHandler(k *fakeSearchKnowledgeStore, d *fakeSearchDecisionStore, g *fakeSearchGTDStore) *handler.SearchHandler {
	return handler.NewSearchHandler(k, d, g)
}

// ---- tests ----

func TestSearchHandler_EmptyQuery(t *testing.T) {
	e := echo.New()
	h := newSearchHandler(
		&fakeSearchKnowledgeStore{},
		&fakeSearchDecisionStore{},
		&fakeSearchGTDStore{},
	)
	e.GET("/api/search", h.Search)

	rec := performRequest(e, http.MethodGet, "/api/search", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestSearchHandler_QueryTooLong(t *testing.T) {
	e := echo.New()
	h := newSearchHandler(
		&fakeSearchKnowledgeStore{},
		&fakeSearchDecisionStore{},
		&fakeSearchGTDStore{},
	)
	e.GET("/api/search", h.Search)

	longQ := strings.Repeat("a", 501)
	rec := performRequest(e, http.MethodGet, "/api/search?q="+longQ, "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestSearchHandler_ReturnsKnowledge(t *testing.T) {
	id := uuid.New()
	kStore := &fakeSearchKnowledgeStore{
		items: []db.KnowledgeItem{
			{ID: id, Type: "til", Title: "Go generics", Content: "Type params since 1.18"},
		},
	}
	e := echo.New()
	h := newSearchHandler(kStore, &fakeSearchDecisionStore{}, &fakeSearchGTDStore{})
	e.GET("/api/search", h.Search)

	rec := performRequest(e, http.MethodGet, "/api/search?q=generics", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Results []struct {
			Type  string    `json:"type"`
			ID    uuid.UUID `json:"id"`
			Title string    `json:"title"`
		} `json:"results"`
		Query string `json:"query"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Query != "generics" {
		t.Errorf("query field: got %q, want %q", resp.Query, "generics")
	}
	if len(resp.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(resp.Results))
	}
	if resp.Results[0].Type != entityKnowledge {
		t.Errorf("type: got %q, want %q", resp.Results[0].Type, entityKnowledge)
	}
	if resp.Results[0].ID != id {
		t.Errorf("id mismatch: got %s, want %s", resp.Results[0].ID, id)
	}
}

func TestSearchHandler_ReturnsDecision(t *testing.T) {
	id := uuid.New()
	dStore := &fakeSearchDecisionStore{
		list: []db.Decision{
			{ID: id, Title: "Use PostgreSQL for persistence", Decision: "PostgreSQL chosen over MySQL"},
		},
	}
	e := echo.New()
	h := newSearchHandler(&fakeSearchKnowledgeStore{}, dStore, &fakeSearchGTDStore{})
	e.GET("/api/search", h.Search)

	rec := performRequest(e, http.MethodGet, "/api/search?q=PostgreSQL", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Results []struct {
			Type  string    `json:"type"`
			ID    uuid.UUID `json:"id"`
			Title string    `json:"title"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	found := false
	for _, r := range resp.Results {
		if r.Type == entityDecision && r.ID == id {
			found = true
		}
	}
	if !found {
		t.Errorf("expected decision result with id %s, results: %+v", id, resp.Results)
	}
}

func TestSearchHandler_DecisionNoMatch(t *testing.T) {
	dStore := &fakeSearchDecisionStore{
		list: []db.Decision{
			{ID: uuid.New(), Title: "Use PostgreSQL", Decision: "PostgreSQL over MySQL"},
		},
	}
	e := echo.New()
	h := newSearchHandler(&fakeSearchKnowledgeStore{}, dStore, &fakeSearchGTDStore{})
	e.GET("/api/search", h.Search)

	rec := performRequest(e, http.MethodGet, "/api/search?q=redis", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Results []struct {
			Type string `json:"type"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	for _, r := range resp.Results {
		if r.Type == entityDecision {
			t.Errorf("did not expect decision result for non-matching query, got: %+v", resp.Results)
		}
	}
}

func TestSearchHandler_ReturnsTask(t *testing.T) {
	id := uuid.New()
	gStore := &fakeSearchGTDStore{
		tasks: []db.Task{
			{
				ID:    id,
				Title: "Implement semantic search",
				Description: pgtype.Text{
					String: "Add pgvector search across entities",
					Valid:  true,
				},
				Status: "active",
			},
		},
	}
	e := echo.New()
	h := newSearchHandler(&fakeSearchKnowledgeStore{}, &fakeSearchDecisionStore{}, gStore)
	e.GET("/api/search", h.Search)

	rec := performRequest(e, http.MethodGet, "/api/search?q=semantic", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Results []struct {
			Type  string    `json:"type"`
			ID    uuid.UUID `json:"id"`
			Title string    `json:"title"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	found := false
	for _, r := range resp.Results {
		if r.Type == entityTask && r.ID == id {
			found = true
		}
	}
	if !found {
		t.Errorf("expected task result with id %s, results: %+v", id, resp.Results)
	}
}

func TestSearchHandler_KnowledgeStoreError(t *testing.T) {
	e := echo.New()
	h := newSearchHandler(
		&fakeSearchKnowledgeStore{err: errors.New("embedding service down")},
		&fakeSearchDecisionStore{},
		&fakeSearchGTDStore{},
	)
	e.GET("/api/search", h.Search)

	rec := performRequest(e, http.MethodGet, "/api/search?q=test", "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestSearchHandler_DecisionStoreError(t *testing.T) {
	e := echo.New()
	h := newSearchHandler(
		&fakeSearchKnowledgeStore{},
		&fakeSearchDecisionStore{err: errors.New("db timeout")},
		&fakeSearchGTDStore{},
	)
	e.GET("/api/search", h.Search)

	rec := performRequest(e, http.MethodGet, "/api/search?q=test", "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestSearchHandler_GTDStoreError(t *testing.T) {
	e := echo.New()
	h := newSearchHandler(
		&fakeSearchKnowledgeStore{},
		&fakeSearchDecisionStore{},
		&fakeSearchGTDStore{err: errors.New("gtd store unavailable")},
	)
	e.GET("/api/search", h.Search)

	rec := performRequest(e, http.MethodGet, "/api/search?q=test", "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestSearchHandler_WhitespaceOnlyQuery(t *testing.T) {
	e := echo.New()
	h := newSearchHandler(
		&fakeSearchKnowledgeStore{},
		&fakeSearchDecisionStore{},
		&fakeSearchGTDStore{},
	)
	e.GET("/api/search", h.Search)

	rec := performRequest(e, http.MethodGet, "/api/search?q=+++", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestSearchHandler_DecisionsCappedAtFive(t *testing.T) {
	// Build 10 matching decisions; expect only 5 in results.
	decisions := make([]db.Decision, 10)
	for i := range decisions {
		decisions[i] = db.Decision{ID: uuid.New(), Title: "Use PostgreSQL", Decision: "PostgreSQL chosen"}
	}
	e := echo.New()
	h := newSearchHandler(
		&fakeSearchKnowledgeStore{},
		&fakeSearchDecisionStore{list: decisions},
		&fakeSearchGTDStore{},
	)
	e.GET("/api/search", h.Search)

	rec := performRequest(e, http.MethodGet, "/api/search?q=PostgreSQL", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var resp struct {
		Results []struct {
			Type string `json:"type"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	decisionCount := 0
	for _, r := range resp.Results {
		if r.Type == entityDecision {
			decisionCount++
		}
	}
	if decisionCount > 5 {
		t.Errorf("expected at most 5 decision results, got %d", decisionCount)
	}
}
