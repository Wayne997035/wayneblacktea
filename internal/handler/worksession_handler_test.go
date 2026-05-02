package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
)

const (
	jsonFalse = "false"
	jsonTrue  = "true"
)

// fakeWorkSessionStore satisfies the unexported workSessionStore interface used
// by WorkSessionHandler.
type fakeWorkSessionStore struct {
	result *worksession.ActiveSessionResult
	err    error
	// capture call arguments for assertions
	calledWorkspaceID uuid.UUID
	calledRepoName    string
}

func (f *fakeWorkSessionStore) GetActive(
	_ context.Context,
	workspaceID uuid.UUID,
	repoName string,
) (*worksession.ActiveSessionResult, error) {
	f.calledWorkspaceID = workspaceID
	f.calledRepoName = repoName
	return f.result, f.err
}

// assertActiveField unmarshals body as a JSON object and checks the "active" field.
func assertActiveField(t *testing.T, body []byte, want string) {
	t.Helper()
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if string(resp["active"]) != want {
		t.Errorf("active = %s, want %s", resp["active"], want)
	}
}

// TestWorkSessionHandler_ActiveSession verifies the 200 response for an active session.
func TestWorkSessionHandler_ActiveSession(t *testing.T) {
	wsID := uuid.New()
	sessID := uuid.New()
	store := &fakeWorkSessionStore{
		result: &worksession.ActiveSessionResult{
			Active: true,
			Session: &worksession.Session{
				ID:       sessID,
				RepoName: "my-repo",
				Status:   "in_progress",
			},
			ImplementationAllowed: true,
		},
	}
	e := newEcho()
	h := handler.NewWorkSessionHandler(store, &wsID)
	e.GET("/api/work-sessions/active", h.GetActiveWorkSession)
	rec := performRequest(e, http.MethodGet, "/api/work-sessions/active?repo_name=my-repo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if string(resp["active"]) != jsonTrue {
		t.Errorf("active = %s, want true", resp["active"])
	}
	if resp["session"] == nil {
		t.Error("expected session field in response")
	}
	if string(resp["implementation_allowed"]) != jsonTrue {
		t.Errorf("implementation_allowed = %s, want true", resp["implementation_allowed"])
	}
}

// TestWorkSessionHandler_InactiveSession verifies 200 with active=false.
func TestWorkSessionHandler_InactiveSession(t *testing.T) {
	wsID := uuid.New()
	store := &fakeWorkSessionStore{
		result: &worksession.ActiveSessionResult{Active: false, ImplementationAllowed: false},
	}
	e := newEcho()
	h := handler.NewWorkSessionHandler(store, &wsID)
	e.GET("/api/work-sessions/active", h.GetActiveWorkSession)
	rec := performRequest(e, http.MethodGet, "/api/work-sessions/active?repo_name=empty-repo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	assertActiveField(t, rec.Body.Bytes(), jsonFalse)
}

// TestWorkSessionHandler_ErrNotFound verifies that ErrNotFound maps to 200 active=false.
func TestWorkSessionHandler_ErrNotFound(t *testing.T) {
	wsID := uuid.New()
	store := &fakeWorkSessionStore{err: worksession.ErrNotFound}
	e := newEcho()
	h := handler.NewWorkSessionHandler(store, &wsID)
	e.GET("/api/work-sessions/active", h.GetActiveWorkSession)
	rec := performRequest(e, http.MethodGet, "/api/work-sessions/active?repo_name=my-repo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	assertActiveField(t, rec.Body.Bytes(), jsonFalse)
}

// TestWorkSessionHandler_MissingRepoName verifies that missing repo_name returns 400.
func TestWorkSessionHandler_MissingRepoName(t *testing.T) {
	wsID := uuid.New()
	store := &fakeWorkSessionStore{}
	e := newEcho()
	h := handler.NewWorkSessionHandler(store, &wsID)
	e.GET("/api/work-sessions/active", h.GetActiveWorkSession)
	rec := performRequest(e, http.MethodGet, "/api/work-sessions/active", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", rec.Code)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["error"] == "" {
		t.Error("expected error field in 400 response")
	}
}

// TestWorkSessionHandler_NilWorkspaceID verifies that nil workspaceID returns 200 active=false.
func TestWorkSessionHandler_NilWorkspaceID(t *testing.T) {
	store := &fakeWorkSessionStore{}
	e := newEcho()
	h := handler.NewWorkSessionHandler(store, nil) // no workspace configured
	e.GET("/api/work-sessions/active", h.GetActiveWorkSession)
	rec := performRequest(e, http.MethodGet, "/api/work-sessions/active?repo_name=my-repo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	assertActiveField(t, rec.Body.Bytes(), jsonFalse)
}

// TestWorkSessionHandler_StoreError verifies that store errors return 500.
func TestWorkSessionHandler_StoreError(t *testing.T) {
	wsID := uuid.New()
	store := &fakeWorkSessionStore{err: errors.New("db connection reset")}
	e := newEcho()
	h := handler.NewWorkSessionHandler(store, &wsID)
	e.GET("/api/work-sessions/active", h.GetActiveWorkSession)
	rec := performRequest(e, http.MethodGet, "/api/work-sessions/active?repo_name=my-repo", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", rec.Code)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp["error"] == "" {
		t.Error("expected error field in 500 response")
	}
}

// TestWorkSessionHandler_WorkspaceIDNeverFromRequest verifies that the handler
// never reads workspace_id from the HTTP request — it must always use the
// server-level configured workspaceID.
func TestWorkSessionHandler_WorkspaceIDNeverFromRequest(t *testing.T) {
	wsID := uuid.New()
	store := &fakeWorkSessionStore{result: &worksession.ActiveSessionResult{Active: false}}

	e := newEcho()
	h := handler.NewWorkSessionHandler(store, &wsID)
	e.GET("/api/work-sessions/active", h.GetActiveWorkSession)

	// Request includes a workspace_id query param that differs from server wsID.
	// The handler MUST ignore it and use only the server-level wsID.
	differentWS := uuid.New().String()
	rec := performRequest(e, http.MethodGet,
		"/api/work-sessions/active?repo_name=test&workspace_id="+differentWS, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// The store must have been called with the SERVER-level wsID, not differentWS.
	if store.calledWorkspaceID != wsID {
		t.Errorf("store called with %s, want server wsID %s", store.calledWorkspaceID, wsID)
	}
}
