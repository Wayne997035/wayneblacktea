package handler_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/google/uuid"
)

// fakeDecisionHandlerStore records the LogParams passed to Log so tests can
// assert provenance binding without a real DB.
type fakeDecisionHandlerStore struct {
	logged  []decision.LogParams
	logErr  error
	all     []db.Decision
	allErr  error
	byRepo  []db.Decision
	byProj  []db.Decision
	listErr error
}

func (f *fakeDecisionHandlerStore) All(_ context.Context, _ int32) ([]db.Decision, error) {
	return f.all, f.allErr
}

func (f *fakeDecisionHandlerStore) ByRepo(_ context.Context, _ string, _ int32) ([]db.Decision, error) {
	return f.byRepo, f.listErr
}

func (f *fakeDecisionHandlerStore) ByProject(_ context.Context, _ uuid.UUID, _ int32) ([]db.Decision, error) {
	return f.byProj, f.listErr
}

func (f *fakeDecisionHandlerStore) Log(_ context.Context, p decision.LogParams) (*db.Decision, error) {
	if f.logErr != nil {
		return nil, f.logErr
	}
	f.logged = append(f.logged, p)
	return &db.Decision{ID: uuid.New(), Title: p.Title}, nil
}

// TestLogDecision_HTTP is a table-driven pass over POST /api/decisions
// covering the happy path, a required-field error, and the P3.0a
// forged-payload regression: an extra "source" key in the request body must
// never override the server-owned decision.SourceManual constant that
// LogDecision always binds (producer #4 — no `source` request field exists
// on logDecisionRequest, so c.Bind silently drops it).
func TestLogDecision_HTTP(t *testing.T) {
	cases := []struct {
		name         string
		body         string
		wantCode     int
		wantLogCount int
		wantSource   decision.Source
	}{
		{
			name: "happy path binds SourceManual",
			body: `{"title":"ADR: use Echo","context":"need HTTP framework",` +
				`"decision":"use echo/v4","rationale":"minimal, fast"}`,
			wantCode:     http.StatusCreated,
			wantLogCount: 1,
			wantSource:   decision.SourceManual,
		},
		{
			name:         "missing required field → 400, no log",
			body:         `{"title":"","context":"c","decision":"d","rationale":"r"}`,
			wantCode:     http.StatusBadRequest,
			wantLogCount: 0,
		},
		{
			name: "forged source field is ignored, still SourceManual",
			body: `{"title":"ADR: forged source","context":"ctx","decision":"dec",` +
				`"rationale":"rat","source":"auto"}`,
			wantCode:     http.StatusCreated,
			wantLogCount: 1,
			wantSource:   decision.SourceManual,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeDecisionHandlerStore{}
			e := newEcho()
			h := handler.NewDecisionHandler(store)
			e.POST("/api/decisions", h.LogDecision)
			rec := performRequest(e, http.MethodPost, "/api/decisions", tc.body)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if len(store.logged) != tc.wantLogCount {
				t.Fatalf("Log calls = %d, want %d", len(store.logged), tc.wantLogCount)
			}
			if tc.wantLogCount > 0 && store.logged[0].Source != tc.wantSource {
				t.Errorf("Source = %q, want %q", store.logged[0].Source, tc.wantSource)
			}
		})
	}
}
