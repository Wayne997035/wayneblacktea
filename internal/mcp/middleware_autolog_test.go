package mcp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// errMockNotImpl is returned by mockGTDStore stub methods that are never
// expected to be called during middleware tests.
var errMockNotImpl = errors.New("mock: method not expected to be called")

// logCall captures a single LogActivity invocation for assertion.
type logCall struct {
	actor     string
	action    string
	projectID *uuid.UUID
	notes     string
}

// mockGTDStore is a minimal gtd.StoreIface that records LogActivity calls.
// All other methods return errMockNotImpl — only LogActivity is exercised
// by the middleware tests, so any unexpected call is surfaced immediately.
type mockGTDStore struct {
	mu   sync.Mutex
	logs []logCall
}

func (m *mockGTDStore) recordedLogs() []logCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]logCall, len(m.logs))
	copy(out, m.logs)
	return out
}

func (m *mockGTDStore) LogActivity(_ context.Context, actor, action string, projectID *uuid.UUID, notes string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, logCall{actor: actor, action: action, projectID: projectID, notes: notes})
	return nil
}

// Satisfy the remaining StoreIface methods — these are unreachable during
// middleware tests, so they return a sentinel error rather than nil,nil.
func (m *mockGTDStore) ListActiveProjects(_ context.Context) ([]db.Project, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) GetProjectByID(_ context.Context, _ uuid.UUID) (*db.Project, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) ProjectByName(_ context.Context, _ string) (*db.Project, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) CreateProject(_ context.Context, _ gtd.CreateProjectParams) (*db.Project, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) Tasks(_ context.Context, _ *uuid.UUID) ([]db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) CreateTask(_ context.Context, _ gtd.CreateTaskParams) (*db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) CompleteTask(_ context.Context, _ uuid.UUID, _ *string) (*db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) ActiveGoals(_ context.Context) ([]db.Goal, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) CreateGoal(_ context.Context, _ gtd.CreateGoalParams) (*db.Goal, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) UpdateTaskStatus(_ context.Context, _ uuid.UUID, _ gtd.TaskStatus) (*db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) UpdateProjectStatus(_ context.Context, _ uuid.UUID, _ gtd.ProjectStatus) (*db.Project, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) DeleteTask(_ context.Context, _ uuid.UUID) error { return errMockNotImpl }

func (m *mockGTDStore) WeeklyProgress(_ context.Context) (int64, int64, error) {
	return 0, 0, errMockNotImpl
}

func (m *mockGTDStore) WorkspaceID() pgtype.UUID { return pgtype.UUID{} }

// successHandler returns a fixed success result — simulates a tool that completed OK.
func successHandler(_ context.Context, _ mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
	return mcpmsg.NewToolResultText("ok"), nil
}

// errorResultHandler returns a tool-error result (IsError=true).
func errorResultHandler(_ context.Context, _ mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
	return mcpmsg.NewToolResultError("something failed"), nil
}

// waitForLogs polls until at least n activity_log entries are recorded or the
// deadline passes. Needed because autoLogMiddleware fires asynchronously.
func waitForLogs(t *testing.T, store *mockGTDStore, n int, deadline time.Duration) []logCall {
	t.Helper()
	timeout := time.After(deadline)
	for {
		logs := store.recordedLogs()
		if len(logs) >= n {
			return logs
		}
		select {
		case <-timeout:
			t.Fatalf("timed out waiting for %d log entries; got %d", n, len(logs))
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestAutoLogMiddleware(t *testing.T) {
	type testCase struct {
		name         string
		tool         string
		args         map[string]any
		handler      func(context.Context, mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error)
		wantLogged   bool
		wantAction   string
		wantNotesHas string // substring that must appear in notes
	}

	cases := []testCase{
		{
			name:         "complete_task fires task:completed",
			tool:         "complete_task",
			args:         map[string]any{"task_id": "abc-123", "artifact": "https://github.com/pr/1"},
			handler:      successHandler,
			wantLogged:   true,
			wantAction:   "task:completed",
			wantNotesHas: "abc-123",
		},
		{
			name:         "add_task fires task:added",
			tool:         "add_task",
			args:         map[string]any{"title": "write integration tests"},
			handler:      successHandler,
			wantLogged:   true,
			wantAction:   "task:added",
			wantNotesHas: "write integration tests",
		},
		{
			name:         "log_decision fires decision:logged",
			tool:         "log_decision",
			args:         map[string]any{"title": "use SQLite for tests"},
			handler:      successHandler,
			wantLogged:   true,
			wantAction:   "decision:logged",
			wantNotesHas: "use SQLite for tests",
		},
		{
			name:         "confirm_plan fires plan:confirmed with counts",
			tool:         "confirm_plan",
			args:         map[string]any{"phases": `[{"title":"A"},{"title":"B"}]`, "decisions": `[{"title":"D1"}]`},
			handler:      successHandler,
			wantLogged:   true,
			wantAction:   "plan:confirmed",
			wantNotesHas: "phases=2 decisions=1",
		},
		{
			name:         "set_session_handoff fires session:handoff",
			tool:         "set_session_handoff",
			args:         map[string]any{"intent": "continue autolog PR"},
			handler:      successHandler,
			wantLogged:   true,
			wantAction:   "session:handoff",
			wantNotesHas: "continue autolog PR",
		},
		{
			name:       "list_tasks (non-high-signal) does not fire",
			tool:       "list_tasks",
			args:       map[string]any{},
			handler:    successHandler,
			wantLogged: false,
		},
		{
			name:       "complete_task with error result does not fire",
			tool:       "complete_task",
			args:       map[string]any{"task_id": "bad-id"},
			handler:    errorResultHandler,
			wantLogged: false,
		},
		{
			name:         "confirm_plan with empty phases JSON does not panic",
			tool:         "confirm_plan",
			args:         map[string]any{"phases": `[]`, "decisions": ``},
			handler:      successHandler,
			wantLogged:   true,
			wantAction:   "plan:confirmed",
			wantNotesHas: "phases=0 decisions=0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockGTDStore{}
			srv := &Server{gtd: mock}
			mw := srv.autoLogMiddleware()
			wrapped := mw(tc.handler)

			req := mcpmsg.CallToolRequest{}
			req.Params.Name = tc.tool
			req.Params.Arguments = tc.args

			res, err := wrapped(context.Background(), req)
			if err != nil {
				t.Fatalf("wrapped handler returned unexpected error: %v", err)
			}
			if res == nil {
				t.Fatal("wrapped handler returned nil result")
			}

			if !tc.wantLogged {
				// Give the goroutine a moment, then assert nothing was logged.
				time.Sleep(50 * time.Millisecond)
				logs := mock.recordedLogs()
				if len(logs) != 0 {
					t.Errorf("expected 0 log entries, got %d", len(logs))
				}
				return
			}

			logs := waitForLogs(t, mock, 1, 2*time.Second)
			if len(logs) != 1 {
				t.Fatalf("expected exactly 1 log entry, got %d", len(logs))
			}
			got := logs[0]

			if got.actor != "wayneblacktea-auto" {
				t.Errorf("actor = %q, want %q", got.actor, "wayneblacktea-auto")
			}
			if got.action != tc.wantAction {
				t.Errorf("action = %q, want %q", got.action, tc.wantAction)
			}
			if got.projectID != nil {
				t.Errorf("projectID = %v, want nil", got.projectID)
			}
			if tc.wantNotesHas != "" && !strings.Contains(got.notes, tc.wantNotesHas) {
				t.Errorf("notes = %q, want to contain %q", got.notes, tc.wantNotesHas)
			}
		})
	}
}

// TestAutoLogEntry_KnownTools verifies the pure mapping function directly,
// covering all five high-signal tools and the non-signal default.
func TestAutoLogEntry_KnownTools(t *testing.T) {
	cases := []struct {
		tool       string
		args       map[string]any
		wantAction string
		wantOK     bool
	}{
		{"complete_task", map[string]any{"task_id": "t1", "artifact": "link"}, "task:completed", true},
		{"add_task", map[string]any{"title": "fix bug"}, "task:added", true},
		{"log_decision", map[string]any{"title": "go with echo"}, "decision:logged", true},
		{"confirm_plan", map[string]any{"phases": `[{}]`, "decisions": `[{},{}]`}, "plan:confirmed", true},
		{"set_session_handoff", map[string]any{"intent": "next: finish PR"}, "session:handoff", true},
		{"list_tasks", map[string]any{}, "", false},
		{"system_health", map[string]any{}, "", false},
		{"get_today_context", map[string]any{}, "", false},
	}

	for _, tc := range cases {
		action, _, ok := autoLogEntry(tc.tool, tc.args)
		if ok != tc.wantOK {
			t.Errorf("[%s] ok = %v, want %v", tc.tool, ok, tc.wantOK)
		}
		if tc.wantOK && action != tc.wantAction {
			t.Errorf("[%s] action = %q, want %q", tc.tool, action, tc.wantAction)
		}
	}
}

// TestJsonArrayLen verifies edge cases for the JSON array length helper.
func TestJsonArrayLen(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", 0},
		{"invalid", 0},
		{`[]`, 0},
		{`[{}]`, 1},
		{`[{},{}]`, 2},
		{`[{"title":"A"},{"title":"B"},{"title":"C"}]`, 3},
	}
	for _, tc := range cases {
		got := jsonArrayLen(tc.raw)
		if got != tc.want {
			t.Errorf("jsonArrayLen(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}
