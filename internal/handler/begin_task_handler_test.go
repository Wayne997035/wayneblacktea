package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

func TestGTDHandler_BeginTask(t *testing.T) {
	taskID := uuid.New()
	baseTask := &db.Task{
		ID:     taskID,
		Title:  "implement feature X",
		Status: "in_progress",
	}

	cases := []struct {
		name     string
		paramID  string
		store    *fakeGTDStore
		wantCode int
		wantBody func(t *testing.T, body []byte)
	}{
		{
			name:    "happy path — pending task begins",
			paramID: taskID.String(),
			store: &fakeGTDStore{
				updatedTask: baseTask,
			},
			wantCode: http.StatusOK,
			wantBody: func(t *testing.T, body []byte) {
				t.Helper()
				var resp map[string]json.RawMessage
				if err := json.Unmarshal(body, &resp); err != nil {
					t.Fatalf("parse response body: %v", err)
				}
				if _, ok := resp["task"]; !ok {
					t.Error("response missing 'task' field")
				}
				if _, ok := resp["branch_name_suggestion"]; !ok {
					t.Error("response missing 'branch_name_suggestion' field")
				}
				if _, ok := resp["work_session_id"]; !ok {
					t.Error("response missing 'work_session_id' field")
				}
			},
		},
		{
			name:     "invalid UUID returns 400",
			paramID:  "not-a-uuid",
			store:    &fakeGTDStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:    "task not found returns 404",
			paramID: taskID.String(),
			store: &fakeGTDStore{
				err: gtd.ErrNotFound,
			},
			wantCode: http.StatusNotFound,
		},
		{
			name:    "internal store error returns 500",
			paramID: taskID.String(),
			store: &fakeGTDStore{
				err: errors.New("db connection lost"),
			},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost, "/api/tasks/"+tc.paramID+"/begin", nil,
			)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(tc.paramID)

			h := handler.NewGTDHandler(tc.store)
			// BeginTask uses echo.NewHTTPError, which Echo's DefaultHTTPErrorHandler
			// translates to the correct status code. Calling directly returns the
			// error; we need to check the code vs the error.
			err := h.BeginTask(c)
			if err != nil {
				// Echo HTTP errors carry the status; unwrap them.
				var he *echo.HTTPError
				if errors.As(err, &he) {
					if he.Code != tc.wantCode {
						t.Errorf("got HTTP %d, want %d (msg: %v)", he.Code, tc.wantCode, he.Message)
					}
					return
				}
				t.Fatalf("unexpected non-HTTP error: %v", err)
			}
			if rec.Code != tc.wantCode {
				t.Errorf("got HTTP %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantBody != nil {
				tc.wantBody(t, rec.Body.Bytes())
			}
		})
	}
}

func TestTaskTitleToBranchSlug(t *testing.T) {
	// taskTitleToBranchSlug is unexported from handler, so we exercise it
	// through the HTTP response body's branch_name_suggestion field.
	cases := []struct {
		title string
		want  string
	}{
		{
			title: "implement user auth",
			want:  "feature/implement-user-auth",
		},
		{
			title: "Fix null pointer dereference",
			want:  "fix/fix-null-pointer-dereference",
		},
		{
			title: "FEATURE: Add OAuth2 support",
			want:  "feature/feature-add-oauth2-support",
		},
		{
			title: "fix: crash on empty slice",
			want:  "fix/fix-crash-on-empty-slice",
		},
		{
			// Long title gets truncated at 60 chars in the slug portion.
			// slug of the title = "implement-the-very-long-feature-that-has-too-many-words-in-the-title-for-a-branch-name"
			// slug[:60] = "implement-the-very-long-feature-that-has-too-many-words-in-t"
			// TrimRight trailing dash: no trailing dash → unchanged
			title: "implement the very long feature that has too many words in the title for a branch name",
			want:  "feature/implement-the-very-long-feature-that-has-too-many-words-in-t",
		},
		{
			title: "  spaces   and---dashes  ",
			want:  "feature/spaces-and-dashes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			taskID := uuid.New()
			task := &db.Task{
				ID:     taskID,
				Title:  tc.title,
				Status: "in_progress",
			}
			store := &fakeGTDStore{updatedTask: task}
			h := handler.NewGTDHandler(store)

			e := echo.New()
			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost, "/api/tasks/"+taskID.String()+"/begin", nil,
			)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(taskID.String())

			if err := h.BeginTask(c); err != nil {
				t.Fatalf("BeginTask: %v", err)
			}

			var resp struct {
				BranchNameSuggestion string `json:"branch_name_suggestion"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.BranchNameSuggestion != tc.want {
				t.Errorf("branch_name_suggestion = %q, want %q", resp.BranchNameSuggestion, tc.want)
			}
		})
	}
}

// TestGTDHandler_BeginTask_PersistsBranchAndPR covers the M-8 sprint linkage:
// POST /api/tasks/:id/begin with body {branch_name, pr_url} MUST forward both
// values to UpdateTask after BeginTask succeeds. Closes the gap that today's
// 5 stale tasks demonstrated.
func TestGTDHandler_BeginTask_PersistsBranchAndPR(t *testing.T) {
	taskID := uuid.New()
	postBegin := &db.Task{
		ID:     taskID,
		Title:  "implement reconcile",
		Status: "in_progress",
	}
	store := &fakeGTDStore{
		beginTaskResult: postBegin,
		updatedTask:     postBegin,
	}
	h := handler.NewGTDHandler(store)

	body := `{"branch_name":"feature/foo","pr_url":"https://github.com/Wayne997035/wayneblacktea/pull/999"}`
	e := echo.New()
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost, "/api/tasks/"+taskID.String()+"/begin",
		strings.NewReader(body),
	)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(taskID.String())

	if err := h.BeginTask(c); err != nil {
		t.Fatalf("BeginTask: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Verify UpdateTask was called with the linkage params.
	if store.capturedUpdateTaskParams == nil {
		t.Fatal("UpdateTask was not called — branch/pr_url not persisted")
	}
	if store.capturedUpdateTaskParams.BranchName == nil ||
		*store.capturedUpdateTaskParams.BranchName != "feature/foo" {
		t.Errorf("BranchName = %+v, want feature/foo", store.capturedUpdateTaskParams.BranchName)
	}
	if store.capturedUpdateTaskParams.PRUrl == nil ||
		*store.capturedUpdateTaskParams.PRUrl != "https://github.com/Wayne997035/wayneblacktea/pull/999" {
		t.Errorf("PRUrl = %+v, want the PR URL", store.capturedUpdateTaskParams.PRUrl)
	}
	if store.beginTaskCalls != 1 {
		t.Errorf("beginTaskCalls = %d, want 1", store.beginTaskCalls)
	}
}

// TestGTDHandler_BeginTask_RejectsInvalidPRURL covers the validation path —
// malformed pr_url MUST return 400 without touching the store.
func TestGTDHandler_BeginTask_RejectsInvalidPRURL(t *testing.T) {
	taskID := uuid.New()
	cases := []struct {
		name string
		body string
	}{
		{
			name: "non-github host",
			body: `{"pr_url":"https://notgithub.com/foo/bar/pull/1"}`,
		},
		{
			name: "issues path not pull",
			body: `{"pr_url":"https://github.com/foo/bar/issues/1"}`,
		},
		{
			name: "javascript scheme",
			body: `{"pr_url":"javascript:alert(1)"}`,
		},
		{
			name: "branch name with newline",
			body: "{\"branch_name\":\"feature/bad\nname\"}",
		},
		{
			name: "branch_name too long",
			body: `{"branch_name":"` + strings.Repeat("a", 256) + `"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeGTDStore{}
			h := handler.NewGTDHandler(store)
			e := echo.New()
			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost, "/api/tasks/"+taskID.String()+"/begin",
				strings.NewReader(tc.body),
			)
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(taskID.String())

			err := h.BeginTask(c)
			var he *echo.HTTPError
			if !errors.As(err, &he) {
				t.Fatalf("expected echo.HTTPError, got %v", err)
			}
			if he.Code != http.StatusBadRequest {
				t.Errorf("got HTTP %d, want 400 (msg: %v)", he.Code, he.Message)
			}
			if store.beginTaskCalls != 0 {
				t.Errorf("beginTaskCalls = %d, want 0 (store must be untouched on validation fail)", store.beginTaskCalls)
			}
			if store.capturedUpdateTaskParams != nil {
				t.Errorf("UpdateTask was called: %+v — must not run on validation fail", store.capturedUpdateTaskParams)
			}
		})
	}
}

// TestGTDHandler_BeginTask_EmptyBodyStillWorks covers the legacy contract:
// callers that POST without a body must still get the status flip.
func TestGTDHandler_BeginTask_EmptyBodyStillWorks(t *testing.T) {
	taskID := uuid.New()
	store := &fakeGTDStore{
		beginTaskResult: &db.Task{ID: taskID, Title: "x", Status: "in_progress"},
	}
	h := handler.NewGTDHandler(store)
	e := echo.New()
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost, "/api/tasks/"+taskID.String()+"/begin", nil,
	)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(taskID.String())

	if err := h.BeginTask(c); err != nil {
		t.Fatalf("BeginTask: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if store.beginTaskCalls != 1 {
		t.Errorf("beginTaskCalls = %d, want 1", store.beginTaskCalls)
	}
	if store.capturedUpdateTaskParams != nil {
		t.Error("UpdateTask should NOT be called when no linkage was supplied")
	}
}

// TestGTDHandler_BeginTask_PartialLinkage covers only-branch and only-pr cases.
func TestGTDHandler_BeginTask_PartialLinkage(t *testing.T) {
	taskID := uuid.New()
	completed := &db.Task{ID: taskID, Title: "x", Status: "in_progress"}

	cases := []struct {
		name           string
		body           string
		wantBranch     string
		wantPR         string
		expectBranch   bool
		expectPRSet    bool
		expectUpdCall  bool
		expectStoreErr bool
	}{
		{
			name:          "only branch_name",
			body:          `{"branch_name":"feature/x"}`,
			wantBranch:    "feature/x",
			expectBranch:  true,
			expectUpdCall: true,
		},
		{
			name:          "only pr_url",
			body:          `{"pr_url":"https://github.com/owner/repo/pull/42"}`,
			wantPR:        "https://github.com/owner/repo/pull/42",
			expectPRSet:   true,
			expectUpdCall: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeGTDStore{
				beginTaskResult: completed,
				updatedTask:     completed,
			}
			h := handler.NewGTDHandler(store)
			e := echo.New()
			req := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodPost, "/api/tasks/"+taskID.String()+"/begin",
				strings.NewReader(tc.body),
			)
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(taskID.String())

			if err := h.BeginTask(c); err != nil {
				t.Fatalf("BeginTask: %v", err)
			}
			if tc.expectUpdCall && store.capturedUpdateTaskParams == nil {
				t.Fatal("UpdateTask not called")
			}
			if tc.expectBranch {
				if store.capturedUpdateTaskParams.BranchName == nil ||
					*store.capturedUpdateTaskParams.BranchName != tc.wantBranch {
					t.Errorf("BranchName mismatch: got %+v want %q",
						store.capturedUpdateTaskParams.BranchName, tc.wantBranch)
				}
				if store.capturedUpdateTaskParams.PRUrl != nil {
					t.Errorf("PRUrl set when only branch was supplied: %+v",
						store.capturedUpdateTaskParams.PRUrl)
				}
			}
			if tc.expectPRSet {
				if store.capturedUpdateTaskParams.PRUrl == nil ||
					*store.capturedUpdateTaskParams.PRUrl != tc.wantPR {
					t.Errorf("PRUrl mismatch: got %+v want %q",
						store.capturedUpdateTaskParams.PRUrl, tc.wantPR)
				}
				if store.capturedUpdateTaskParams.BranchName != nil {
					t.Errorf("BranchName set when only PR was supplied: %+v",
						store.capturedUpdateTaskParams.BranchName)
				}
			}
		})
	}
}
