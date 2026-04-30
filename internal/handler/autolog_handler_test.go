package handler_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
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
	list []db.Decision
	err  error
}

func (f *fakeAutologDecisionStore) All(_ context.Context, _ int32) ([]db.Decision, error) {
	return f.list, f.err
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
			h := handler.NewAutologHandler(tc.gtd, &fakeAutologSessionStore{}, &fakeAutologDecisionStore{})
			e.POST("/api/activity", h.LogActivity)
			rec := performRequest(e, http.MethodPost, "/api/activity", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}

// ---- POST /api/auto-handoff tests ----

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
			h := handler.NewAutologHandler(tc.gtd, tc.sess, tc.dec)
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
