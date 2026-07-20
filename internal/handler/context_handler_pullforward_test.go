package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// runGetTodayContext posts a GET /api/context/today through h and returns the
// response recorder, mirroring the runReconcileRequest helper pattern in
// reconcile_handler_test.go.
func runGetTodayContext(t *testing.T, h *handler.ContextHandler) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/context/today", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	if err := h.GetTodayContext(c); err != nil {
		t.Fatalf("GetTodayContext: %v", err)
	}
	return rec
}

// TestGetTodayContext_PulledForwardAlwaysPresent verifies GET
// /api/context/today always includes a pulled_forward JSON array (never
// null/omitted), both when the store returns tasks and when it returns an
// empty/nil slice — the wire-contract guarantee
// web/src/hooks/useContextToday.ts relies on.
func TestGetTodayContext_PulledForwardAlwaysPresent(t *testing.T) {
	tests := []struct {
		name  string
		tasks []db.Task
		want  int
	}{
		{
			name:  "store returns nil → pulled_forward is [] not null",
			tasks: nil,
			want:  0,
		},
		{
			name: "store returns tasks → pulled_forward carries them through",
			tasks: []db.Task{
				{ID: uuid.New(), Title: "important task", Status: "pending"},
			},
			want: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gtdStore := &fakeGTDStore{pullForwardTasks: tc.tasks}
			sessStore := &fakeSessionStore{}
			h := handler.NewContextHandler(gtdStore, sessStore)

			rec := runGetTodayContext(t, h)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}

			var parsed map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
				t.Fatalf("unmarshal response: %v\nbody: %s", err, rec.Body.String())
			}
			pfRaw, ok := parsed["pulled_forward"]
			if !ok {
				t.Fatalf("pulled_forward key missing from response: %s", rec.Body.String())
			}
			if string(pfRaw) == jsonNull {
				t.Fatalf("pulled_forward must never be null, got: %s", pfRaw)
			}

			var pf []db.Task
			if err := json.Unmarshal(pfRaw, &pf); err != nil {
				t.Fatalf("pulled_forward is not a JSON array: %s (%v)", pfRaw, err)
			}
			if len(pf) != tc.want {
				t.Errorf("pulled_forward length = %d, want %d", len(pf), tc.want)
			}
		})
	}
}

// TestGetTodayContext_PullForwardStoreError verifies a PullForwardTasks error
// surfaces as HTTP 500 (matching the sibling goals/projects/weekly-progress
// error-handling convention in GetTodayContext) instead of silently
// succeeding with a missing field.
func TestGetTodayContext_PullForwardStoreError(t *testing.T) {
	gtdStore := &fakeGTDStore{pullForwardErr: errors.New("db unavailable")}
	sessStore := &fakeSessionStore{}
	h := handler.NewContextHandler(gtdStore, sessStore)

	rec := runGetTodayContext(t, h)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
}
