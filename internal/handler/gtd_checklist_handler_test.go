package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// checklistTestHandler wraps fakeGTDStore with checklist-specific fields for assertions.
type checklistTestStore struct {
	fakeGTDStore
	addResult    []gtd.ChecklistItem
	updateResult []gtd.ChecklistItem
	deleteErr    error
}

func (f *checklistTestStore) AddChecklistItem(
	_ context.Context, _ uuid.UUID, _ uuid.UUID, _ gtd.ChecklistItem,
) ([]gtd.ChecklistItem, error) {
	return f.addResult, f.err
}

func (f *checklistTestStore) UpdateChecklistItem(
	_ context.Context, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID, _ gtd.UpdateChecklistItemParams,
) ([]gtd.ChecklistItem, error) {
	return f.updateResult, f.err
}

func (f *checklistTestStore) DeleteChecklistItem(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	return f.err
}

// nullByteJSONBody is valid JSON whose title contains the JSON unicode escape
// sequence   (backslash-u-0-0-0-0) which json.Decoder decodes to a null
// byte in Go. SanitiseChecklistText then strips it, producing "helloworld".
// The double-quoted Go string uses   to avoid any NUL byte in source.
var nullByteJSONBody = "{\"title\":\"hello\\u0000world\"}"

func TestGTDHandler_AddChecklistItem(t *testing.T) {
	taskID := uuid.New()
	itemID := uuid.New()
	sampleItems := []gtd.ChecklistItem{{ID: itemID, Title: "step 1"}}

	cases := []struct {
		name     string
		taskID   string
		body     string
		store    *checklistTestStore
		wantCode int
	}{
		{
			name:     "happy path item added",
			taskID:   taskID.String(),
			body:     `{"title":"step 1","notes":"some note"}`,
			store:    &checklistTestStore{addResult: sampleItems},
			wantCode: http.StatusOK,
		},
		{
			name:     "missing title returns 400",
			taskID:   taskID.String(),
			body:     `{"notes":"no title here"}`,
			store:    &checklistTestStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid task UUID returns 400",
			taskID:   "not-a-uuid",
			body:     `{"title":"step"}`,
			store:    &checklistTestStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "task not found returns 404",
			taskID:   taskID.String(),
			body:     `{"title":"step"}`,
			store:    &checklistTestStore{fakeGTDStore: fakeGTDStore{err: gtd.ErrNotFound}},
			wantCode: http.StatusNotFound,
		},
		{
			name:     "store error returns 500",
			taskID:   taskID.String(),
			body:     `{"title":"step"}`,
			store:    &checklistTestStore{fakeGTDStore: fakeGTDStore{err: errors.New("db down")}},
			wantCode: http.StatusInternalServerError,
		},
		{
			// Body exceeds the 32 KB limit: LimitReader truncates the JSON,
			// making it unparseable so the handler returns 400.
			name:     "body exceeds 32KB limit returns 400",
			taskID:   taskID.String(),
			body:     `{"title":"` + strings.Repeat("x", 33*1024) + `"}`,
			store:    &checklistTestStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			// nullByteJSONBody has the JSON unicode escape  in the title.
			// json.Decoder converts it to a null byte; SanitiseChecklistText
			// strips it, producing "helloworld". The request succeeds with 200.
			name:   "null byte in title is sanitised and returns 200",
			taskID: taskID.String(),
			body:   nullByteJSONBody,
			store: &checklistTestStore{
				addResult: []gtd.ChecklistItem{{ID: itemID, Title: "helloworld"}},
			},
			wantCode: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			h := handler.NewGTDHandler(tc.store)
			e.POST("/api/tasks/:id/checklist/items", h.AddChecklistItem)
			rec := performRequest(e, http.MethodPost, "/api/tasks/"+tc.taskID+"/checklist/items", tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode == http.StatusOK {
				var items []gtd.ChecklistItem
				if err := json.NewDecoder(rec.Body).Decode(&items); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if len(items) == 0 {
					t.Error("expected at least 1 item in response")
				}
			}
		})
	}
}

func TestGTDHandler_UpdateChecklistItem(t *testing.T) {
	taskID := uuid.New()
	itemID := uuid.New()
	done := true
	updatedItems := []gtd.ChecklistItem{{ID: itemID, Title: "step 1", Done: true}}

	cases := []struct {
		name     string
		taskID   string
		itemID   string
		body     string
		store    *checklistTestStore
		wantCode int
	}{
		{
			name:     "happy path done toggled",
			taskID:   taskID.String(),
			itemID:   itemID.String(),
			body:     `{"done":true}`,
			store:    &checklistTestStore{updateResult: updatedItems},
			wantCode: http.StatusOK,
		},
		{
			name:     "update title",
			taskID:   taskID.String(),
			itemID:   itemID.String(),
			body:     `{"title":"new title"}`,
			store:    &checklistTestStore{updateResult: []gtd.ChecklistItem{{ID: itemID, Title: "new title"}}},
			wantCode: http.StatusOK,
		},
		{
			name:     "invalid task UUID returns 400",
			taskID:   "bad",
			itemID:   itemID.String(),
			body:     `{"done":true}`,
			store:    &checklistTestStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid item UUID returns 400",
			taskID:   taskID.String(),
			itemID:   "bad",
			body:     `{"done":true}`,
			store:    &checklistTestStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "item not found returns 404",
			taskID:   taskID.String(),
			itemID:   itemID.String(),
			body:     `{"done":true}`,
			store:    &checklistTestStore{fakeGTDStore: fakeGTDStore{err: gtd.ErrNotFound}},
			wantCode: http.StatusNotFound,
		},
		{
			name:     "store error returns 500",
			taskID:   taskID.String(),
			itemID:   itemID.String(),
			body:     `{"done":true}`,
			store:    &checklistTestStore{fakeGTDStore: fakeGTDStore{err: errors.New("db down")}},
			wantCode: http.StatusInternalServerError,
		},
	}
	_ = done // suppress unused warning

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			h := handler.NewGTDHandler(tc.store)
			e.PATCH("/api/tasks/:id/checklist/items/:item_id", h.UpdateChecklistItem)
			rec := performRequest(e, http.MethodPatch,
				"/api/tasks/"+tc.taskID+"/checklist/items/"+tc.itemID, tc.body)
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
			// For the "happy path done toggled" case, decode the response and
			// verify that items[0].Done is true.
			if tc.wantCode == http.StatusOK && tc.name == "happy path done toggled" {
				var items []gtd.ChecklistItem
				if err := json.NewDecoder(rec.Body).Decode(&items); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if len(items) == 0 {
					t.Fatal("expected at least 1 item in response")
				}
				if !items[0].Done {
					t.Errorf("expected items[0].Done == true, got false")
				}
			}
		})
	}
}

func TestGTDHandler_DeleteChecklistItem(t *testing.T) {
	taskID := uuid.New()
	itemID := uuid.New()

	cases := []struct {
		name     string
		taskID   string
		itemID   string
		store    *checklistTestStore
		wantCode int
	}{
		{
			name:     "happy path item deleted",
			taskID:   taskID.String(),
			itemID:   itemID.String(),
			store:    &checklistTestStore{},
			wantCode: http.StatusNoContent,
		},
		{
			name:     "invalid task UUID returns 400",
			taskID:   "bad",
			itemID:   itemID.String(),
			store:    &checklistTestStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "invalid item UUID returns 400",
			taskID:   taskID.String(),
			itemID:   "bad",
			store:    &checklistTestStore{},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "item not found returns 404",
			taskID:   taskID.String(),
			itemID:   itemID.String(),
			store:    &checklistTestStore{deleteErr: gtd.ErrNotFound},
			wantCode: http.StatusNotFound,
		},
		{
			name:     "store error returns 500",
			taskID:   taskID.String(),
			itemID:   itemID.String(),
			store:    &checklistTestStore{deleteErr: errors.New("db down")},
			wantCode: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			h := handler.NewGTDHandler(tc.store)
			e.DELETE("/api/tasks/:id/checklist/items/:item_id", h.DeleteChecklistItem)
			rec := performRequest(e, http.MethodDelete,
				"/api/tasks/"+tc.taskID+"/checklist/items/"+tc.itemID, "")
			if rec.Code != tc.wantCode {
				t.Errorf("got status %d, want %d (body: %s)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}
