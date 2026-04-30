package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---- fake stores for AutologHandler ----

type fakeAutologGTDStore struct {
	tasks   []db.Task
	taskErr error
	logErr  error
}

func (f *fakeAutologGTDStore) LogActivity(_ context.Context, _, _ string, _ *uuid.UUID, _ string) error {
	return f.logErr
}

func (f *fakeAutologGTDStore) Tasks(_ context.Context, _ *uuid.UUID) ([]db.Task, error) {
	return f.tasks, f.taskErr
}

type fakeAutologSessionStore struct {
	result     *db.SessionHandoff
	err        error
	latest     *db.SessionHandoff
	latestErr  error
	resolveErr error
}

func (f *fakeAutologSessionStore) LatestHandoff(_ context.Context) (*db.SessionHandoff, error) {
	return f.latest, f.latestErr
}

func (f *fakeAutologSessionStore) SetHandoff(_ context.Context, _ session.HandoffParams) (*db.SessionHandoff, error) {
	return f.result, f.err
}

func (f *fakeAutologSessionStore) Resolve(_ context.Context, _ uuid.UUID) error {
	return f.resolveErr
}

type fakeAutologDecisionStore struct {
	list   []db.Decision
	err    error
	logErr error
	logged []decision.LogParams
}

func (f *fakeAutologDecisionStore) All(_ context.Context, _ int32) ([]db.Decision, error) {
	return f.list, f.err
}

func (f *fakeAutologDecisionStore) Log(_ context.Context, p decision.LogParams) (*db.Decision, error) {
	if f.logErr != nil {
		return nil, f.logErr
	}
	f.logged = append(f.logged, p)
	return &db.Decision{ID: uuid.New(), Title: p.Title}, nil
}

// ---- stub summarizers ----

type stubSummarizer struct {
	result ai.SummaryResult
	called bool
}

func (s *stubSummarizer) Summarize(_ context.Context, _ []ai.Message) ai.SummaryResult {
	s.called = true
	return s.result
}

type stubCapturingSummarizer struct {
	onSummarize func([]ai.Message) ai.SummaryResult
}

func (s *stubCapturingSummarizer) Summarize(_ context.Context, msgs []ai.Message) ai.SummaryResult {
	return s.onSummarize(msgs)
}

// ---- POST /api/activity tests ----

func TestAutologHandler_LogActivity(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		gtd      *fakeAutologGTDStore
		wantCode int
	}{
		{
			name:     "success",
			body:     `{"actor":"bash-hook","action":"deploy:bash","notes":"gh pr merge 42"}`,
			gtd:      &fakeAutologGTDStore{},
			wantCode: http.StatusOK,
		},
		{
			name:     "missing actor → 400",
			body:     `{"action":"deploy:bash"}`,
			gtd:      &fakeAutologGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "missing action → 400",
			body:     `{"actor":"bash-hook"}`,
			gtd:      &fakeAutologGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "empty body → 400",
			body:     `{}`,
			gtd:      &fakeAutologGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid JSON → 400",
			body:     `{bad`,
			gtd:      &fakeAutologGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "store error → 500",
			body:     `{"actor":"bash-hook","action":"deploy:bash"}`,
			gtd:      &fakeAutologGTDStore{logErr: errors.New("db write fail")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewAutologHandler(tc.gtd, &fakeAutologSessionStore{}, &fakeAutologDecisionStore{}, nil)
			e.POST("/api/activity", h.LogActivity)
			rec := performRequest(e, http.MethodPost, "/api/activity", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// ---- POST /api/auto-handoff tests (mechanical path) ----

func TestAutologHandler_AutoHandoff(t *testing.T) {
	handoffID := uuid.New()
	inProgressTask := db.Task{ID: uuid.New(), Title: "Implement autolog", Status: "in_progress"}
	doneTask := db.Task{ID: uuid.New(), Title: "Setup CI", Status: "done"}
	decision1 := db.Decision{ID: uuid.New(), Title: "Use Echo"}
	decision2 := db.Decision{ID: uuid.New(), Title: "Use PostgreSQL"}

	okHandoff := &db.SessionHandoff{
		ID:     handoffID,
		Intent: "Auto-handoff: in_progress=[Implement autolog] recent_decisions=[Use Echo, Use PostgreSQL]",
		ContextSummary: pgtype.Text{
			String: "Auto-handoff: in_progress=[Implement autolog] recent_decisions=[Use Echo, Use PostgreSQL]",
			Valid:  true,
		},
	}

	cases := []struct {
		name        string
		gtd         *fakeAutologGTDStore
		sess        *fakeAutologSessionStore
		dec         *fakeAutologDecisionStore
		wantCode    int
		wantIDField bool
	}{
		{
			name:        "success with in-progress tasks and decisions",
			gtd:         &fakeAutologGTDStore{tasks: []db.Task{inProgressTask, doneTask}},
			sess:        &fakeAutologSessionStore{result: okHandoff},
			dec:         &fakeAutologDecisionStore{list: []db.Decision{decision1, decision2}},
			wantCode:    http.StatusOK,
			wantIDField: true,
		},
		{
			name: "no in-progress tasks or decisions → still succeeds",
			gtd:  &fakeAutologGTDStore{tasks: []db.Task{}},
			sess: &fakeAutologSessionStore{result: &db.SessionHandoff{
				ID:     uuid.New(),
				Intent: "Auto-handoff: in_progress=[] recent_decisions=[]",
			}},
			dec:         &fakeAutologDecisionStore{list: []db.Decision{}},
			wantCode:    http.StatusOK,
			wantIDField: true,
		},
		{
			name:     "tasks store error → 500",
			gtd:      &fakeAutologGTDStore{taskErr: errors.New("db down")},
			sess:     &fakeAutologSessionStore{},
			dec:      &fakeAutologDecisionStore{},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:     "decisions store error → 500",
			gtd:      &fakeAutologGTDStore{tasks: []db.Task{}},
			sess:     &fakeAutologSessionStore{},
			dec:      &fakeAutologDecisionStore{err: errors.New("db down")},
			wantCode: http.StatusInternalServerError,
		},
		{
			name:     "session store error → 500",
			gtd:      &fakeAutologGTDStore{tasks: []db.Task{}},
			sess:     &fakeAutologSessionStore{err: errors.New("write fail")},
			dec:      &fakeAutologDecisionStore{list: []db.Decision{}},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEcho()
			h := handler.NewAutologHandler(tc.gtd, tc.sess, tc.dec, nil)
			e.POST("/api/auto-handoff", h.AutoHandoff)
			rec := performRequest(e, http.MethodPost, "/api/auto-handoff", "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantIDField {
				body := rec.Body.String()
				if !strings.Contains(body, "handoff_id") {
					t.Errorf("expected handoff_id in response, got: %s", body)
				}
			}
		})
	}
}

// ---- AI enrichment path tests ----

func TestAutoHandoff_SummarizerNil(t *testing.T) {
	// nil summarizer → mechanical fallback, must not panic.
	handoffID := uuid.New()
	sess := &fakeAutologSessionStore{result: &db.SessionHandoff{
		ID:     handoffID,
		Intent: "Auto-handoff: in_progress=[] recent_decisions=[]",
	}}
	e := newEcho()
	h := handler.NewAutologHandler(&fakeAutologGTDStore{}, sess, &fakeAutologDecisionStore{}, nil)
	e.POST("/api/auto-handoff", h.AutoHandoff)
	rec := performRequest(e, http.MethodPost, "/api/auto-handoff", `{"transcript":[{"role":"user","content":"hello"}]}`)
	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "handoff_id") {
		t.Errorf("expected handoff_id in response, got: %s", rec.Body.String())
	}
}

func TestAutoHandoff_WithTranscript(t *testing.T) {
	// Summarizer returns non-empty result → summary used as context_summary, decisions logged.
	handoffID := uuid.New()
	sess := &fakeAutologSessionStore{result: &db.SessionHandoff{
		ID:     handoffID,
		Intent: "Auto-handoff: in_progress=[] recent_decisions=[]",
		ContextSummary: pgtype.Text{
			String: "Implemented OAuth with PKCE flow.",
			Valid:  true,
		},
	}}
	dec := &fakeAutologDecisionStore{}

	stub := &stubSummarizer{
		result: ai.SummaryResult{
			Summary:   "Implemented OAuth with PKCE flow.",
			Decisions: []string{"Use PKCE over implicit grant"},
		},
	}

	e := newEcho()
	h := handler.NewAutologHandlerForTest(&fakeAutologGTDStore{}, sess, dec, stub)
	e.POST("/api/auto-handoff", h.AutoHandoff)

	body := `{"transcript":[{"role":"user","content":"Let's implement OAuth."},{"role":"assistant","content":"I'll use PKCE."}]}`
	rec := performRequest(e, http.MethodPost, "/api/auto-handoff", body)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !stub.called {
		t.Error("expected summarizer to be called, but it was not")
	}
	if len(dec.logged) == 0 {
		t.Error("expected implicit decision to be logged, but dec.logged is empty")
	}
	if dec.logged[0].Title != "Use PKCE over implicit grant" {
		t.Errorf("logged decision title = %q, want %q", dec.logged[0].Title, "Use PKCE over implicit grant")
	}
}

func TestAutoHandoff_SummarizerError(t *testing.T) {
	// When summarizer returns empty result, handler falls back to mechanical summary.
	handoffID := uuid.New()
	sess := &fakeAutologSessionStore{result: &db.SessionHandoff{
		ID:     handoffID,
		Intent: "Auto-handoff: in_progress=[] recent_decisions=[]",
	}}

	stub := &stubSummarizer{
		result: ai.SummaryResult{}, // empty = summarizer "failed"
	}

	e := newEcho()
	h := handler.NewAutologHandlerForTest(&fakeAutologGTDStore{}, sess, &fakeAutologDecisionStore{}, stub)
	e.POST("/api/auto-handoff", h.AutoHandoff)

	body := `{"transcript":[{"role":"user","content":"hello"}]}`
	rec := performRequest(e, http.MethodPost, "/api/auto-handoff", body)

	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if !stub.called {
		t.Error("expected summarizer to be called")
	}
	if !strings.Contains(rec.Body.String(), "handoff_id") {
		t.Errorf("expected handoff_id in response, got: %s", rec.Body.String())
	}
}

func TestAutoHandoff_TranscriptCapAt100Messages(t *testing.T) {
	// Build 150 messages — handler should cap to last 100.
	msgs := make([]ai.Message, 150)
	for i := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = ai.Message{Role: role, Content: "msg"}
	}

	var capturedLen int
	stub := &stubCapturingSummarizer{
		onSummarize: func(t []ai.Message) ai.SummaryResult {
			capturedLen = len(t)
			return ai.SummaryResult{}
		},
	}

	handoffID := uuid.New()
	sess := &fakeAutologSessionStore{result: &db.SessionHandoff{ID: handoffID}}

	reqBody, err := json.Marshal(map[string]any{"transcript": msgs})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	e := newEcho()
	h := handler.NewAutologHandlerForTest(&fakeAutologGTDStore{}, sess, &fakeAutologDecisionStore{}, stub)
	e.POST("/api/auto-handoff", h.AutoHandoff)

	rec := performRequest(e, http.MethodPost, "/api/auto-handoff", string(reqBody))
	if rec.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", rec.Code)
	}
	if capturedLen > 100 {
		t.Errorf("summarizer received %d messages, want ≤ 100", capturedLen)
	}
}
