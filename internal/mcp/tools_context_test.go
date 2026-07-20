package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/arch"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// pullForwardTestArchStore is a minimal arch.StoreIface fake that always
// reports "no snapshot" so handleGetTodayContext's best-effort arch lookup
// doesn't panic on a nil interface (Server.arch is a plain interface field —
// an unset one is a true nil, and calling a method on it panics) and doesn't
// add noise to the assertions below.
type pullForwardTestArchStore struct{}

func (pullForwardTestArchStore) UpsertSnapshot(context.Context, arch.UpsertParams) (*arch.Snapshot, error) {
	return nil, nil
}

func (pullForwardTestArchStore) GetSnapshot(context.Context, string) (*arch.Snapshot, error) {
	return nil, arch.ErrNotFound
}

// pullForwardTestGTDStore embeds noopGTDStore (tools_contextpack_fakes_test.go)
// and overrides PullForwardTasks to return a configurable fixture, so
// get_today_context tests can assert on the pulled_forward field without a
// real database.
type pullForwardTestGTDStore struct {
	noopGTDStore
	tasks []db.Task
	err   error
}

func (s pullForwardTestGTDStore) PullForwardTasks(context.Context, time.Time) ([]db.Task, error) {
	return s.tasks, s.err
}

// getTodayContextText calls handleGetTodayContext and returns the raw JSON
// text of a successful response, failing the test on any error or non-text
// result shape.
func getTodayContextText(t *testing.T, s *Server) string {
	t.Helper()
	result, err := s.handleGetTodayContext(context.Background(), mcpmsg.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleGetTodayContext: %v", err)
	}
	if result.IsError {
		t.Fatalf("handleGetTodayContext returned a tool error result: %+v", result.Content)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected at least one content item")
	}
	textContent, ok := result.Content[0].(mcpmsg.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	return textContent.Text
}

// TestHandleGetTodayContext_PulledForwardAlwaysPresent verifies the
// pulled_forward field is always a JSON array (never null/omitted) in
// get_today_context, both when the store returns tasks and when it returns
// an empty/nil slice — the wire-contract guarantee callers rely on so they
// never need a presence check.
func TestHandleGetTodayContext_PulledForwardAlwaysPresent(t *testing.T) {
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
			s := &Server{
				gtd:     pullForwardTestGTDStore{tasks: tc.tasks},
				session: noopSessionStore{},
				arch:    pullForwardTestArchStore{},
			}

			raw := getTodayContextText(t, s)

			var parsed map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
				t.Fatalf("unmarshal response: %v\nraw: %s", err, raw)
			}
			pfRaw, ok := parsed["pulled_forward"]
			if !ok {
				t.Fatalf("pulled_forward key missing from response: %s", raw)
			}
			if string(pfRaw) == nullStr {
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

// TestHandleGetTodayContext_PullForwardStoreError verifies a PullForwardTasks
// error surfaces as a tool error result (matching the sibling goals/projects/
// weekly-progress error-handling convention in handleGetTodayContext) instead
// of silently dropping the field or panicking.
func TestHandleGetTodayContext_PullForwardStoreError(t *testing.T) {
	s := &Server{
		gtd:     pullForwardTestGTDStore{err: context.DeadlineExceeded},
		session: noopSessionStore{},
		arch:    pullForwardTestArchStore{},
	}

	result, err := s.handleGetTodayContext(context.Background(), mcpmsg.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleGetTodayContext: unexpected transport error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error result when PullForwardTasks fails, got success")
	}
}
