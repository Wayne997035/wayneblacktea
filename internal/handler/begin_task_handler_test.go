package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
