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

// toolCompleteTask is the MCP tool name used in autolog middleware tests.
// Declared as a constant to satisfy goconst (repeated ≥3 times in this file).
const toolCompleteTask = "complete_task"

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

func (m *mockGTDStore) ProjectsByRepoName(_ context.Context, _ string) ([]db.Project, error) {
	return nil, nil
}

func (m *mockGTDStore) CreateProject(_ context.Context, _ gtd.CreateProjectParams) (*db.Project, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) Tasks(_ context.Context, _ *uuid.UUID) ([]db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) TasksByProjectAllStatuses(_ context.Context, _ uuid.UUID) ([]db.Task, error) {
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

func (m *mockGTDStore) UpdateGoal(_ context.Context, _ uuid.UUID, _ gtd.UpdateGoalParams) (*db.Goal, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) UpdateProject(_ context.Context, _ uuid.UUID, _ gtd.UpdateProjectParams) (*db.Project, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) DeleteTask(_ context.Context, _ uuid.UUID) error { return errMockNotImpl }

func (m *mockGTDStore) WeeklyProgress(_ context.Context) (int64, int64, error) {
	return 0, 0, errMockNotImpl
}

func (m *mockGTDStore) ListActivityLogsSince(_ context.Context, _ time.Time, _ int32) ([]db.ActivityLog, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) TopPendingTask(_ context.Context) (*db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) TasksByDueDateRange(_ context.Context, _, _ time.Time) ([]db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) TasksForTimeline(_ context.Context, _, _ time.Time) ([]db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) WorkspaceID() pgtype.UUID { return pgtype.UUID{} }

func (m *mockGTDStore) RecentCompletedTasks(_ context.Context, _ uuid.UUID, _ int32) ([]db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) RecentActivityByProject(_ context.Context, _ uuid.UUID, _ time.Time, _ int32) ([]db.ActivityLog, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) UpcomingTasks(_ context.Context, _ time.Time, _, _ int) ([]db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) UpdateTask(_ context.Context, _ uuid.UUID, _ gtd.UpdateTaskParams) (*db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) AddChecklistItem(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ gtd.ChecklistItem) ([]gtd.ChecklistItem, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) UpdateChecklistItem(
	_ context.Context, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID, _ gtd.UpdateChecklistItemParams,
) ([]gtd.ChecklistItem, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) DeleteChecklistItem(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID) error {
	return errMockNotImpl
}

func (m *mockGTDStore) BeginTask(_ context.Context, _ uuid.UUID, _ uuid.UUID) (*db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) BatchCompleteTasksByPRMatch(_ context.Context, _ []gtd.Match) (int, error) {
	return 0, errMockNotImpl
}

func (m *mockGTDStore) GetTaskByID(_ context.Context, _ uuid.UUID) (*db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockGTDStore) TasksFiltered(_ context.Context, _ gtd.TaskFilter) ([]db.Task, error) {
	return nil, errMockNotImpl
}

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
			tool:         toolCompleteTask,
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
			tool:       toolCompleteTask,
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

// mockGTDStoreWithTaskLookup embeds mockGTDStore and also satisfies
// taskProjectGetter, enabling tests that verify project_id enrichment.
type mockGTDStoreWithTaskLookup struct {
	*mockGTDStore
	// tasksByID maps task UUID to the task returned by GetTaskByID.
	// If the UUID is not present, GetTaskByID returns gtd.ErrNotFound.
	tasksByID map[uuid.UUID]*db.Task
}

func (m *mockGTDStoreWithTaskLookup) GetTaskByID(_ context.Context, id uuid.UUID) (*db.Task, error) {
	if t, ok := m.tasksByID[id]; ok {
		return t, nil
	}
	return nil, gtd.ErrNotFound
}

// waitForLogsWithLookup is like waitForLogs but for mockGTDStoreWithTaskLookup.
func waitForLogsWithLookup(t *testing.T, store *mockGTDStoreWithTaskLookup, n int, deadline time.Duration) []logCall {
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

// TestAutoLogMiddleware_ProjectIDEnrichment verifies that autoLogMiddleware
// populates project_id on the activity_log entry when the GTD store implements
// taskProjectGetter and the task has a project_id.
func TestAutoLogMiddleware_ProjectIDEnrichment(t *testing.T) {
	projectID := uuid.New()
	taskID := uuid.New()
	unknownTaskID := uuid.New()

	validTask := &db.Task{
		ID: taskID,
		ProjectID: func() pgtype.UUID {
			return pgtype.UUID{Bytes: [16]byte(projectID), Valid: true}
		}(),
	}

	cases := []struct {
		name          string
		tool          string
		args          map[string]any
		wantProjectID *uuid.UUID
		wantLogged    bool
		wantAction    string
	}{
		{
			name:          "complete_task with known task propagates project_id",
			tool:          toolCompleteTask,
			args:          map[string]any{"task_id": taskID.String(), "artifact": "https://example.com/pr/1"},
			wantProjectID: &projectID,
			wantLogged:    true,
			wantAction:    "task:completed",
		},
		{
			name:          "begin_task with known task propagates project_id",
			tool:          "begin_task",
			args:          map[string]any{"task_id": taskID.String()},
			wantProjectID: &projectID,
			wantLogged:    true,
			wantAction:    "task:begin",
		},
		{
			name:          "update_task with known task propagates project_id",
			tool:          "update_task",
			args:          map[string]any{"task_id": taskID.String()},
			wantProjectID: &projectID,
			wantLogged:    true,
			wantAction:    "task:updated",
		},
		{
			name:          "complete_task with unknown task_id falls back to nil project_id",
			tool:          toolCompleteTask,
			args:          map[string]any{"task_id": unknownTaskID.String()},
			wantProjectID: nil,
			wantLogged:    true,
			wantAction:    "task:completed",
		},
		{
			name:          "complete_task with invalid task_id falls back to nil project_id",
			tool:          toolCompleteTask,
			args:          map[string]any{"task_id": "not-a-uuid"},
			wantProjectID: nil,
			wantLogged:    true,
			wantAction:    "task:completed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner := &mockGTDStore{}
			store := &mockGTDStoreWithTaskLookup{
				mockGTDStore: inner,
				tasksByID:    map[uuid.UUID]*db.Task{taskID: validTask},
			}
			srv := &Server{gtd: store}
			mw := srv.autoLogMiddleware()
			wrapped := mw(successHandler)

			req := mcpmsg.CallToolRequest{}
			req.Params.Name = tc.tool
			req.Params.Arguments = tc.args

			_, err := wrapped(context.Background(), req)
			if err != nil {
				t.Fatalf("wrapped handler returned unexpected error: %v", err)
			}

			logs := waitForLogsWithLookup(t, store, 1, 2*time.Second)
			if len(logs) != 1 {
				t.Fatalf("expected exactly 1 log entry, got %d", len(logs))
			}
			got := logs[0]

			if got.action != tc.wantAction {
				t.Errorf("action = %q, want %q", got.action, tc.wantAction)
			}
			if tc.wantProjectID == nil {
				if got.projectID != nil {
					t.Errorf("projectID = %v, want nil", got.projectID)
				}
			} else {
				if got.projectID == nil {
					t.Errorf("projectID = nil, want %v", *tc.wantProjectID)
				} else if *got.projectID != *tc.wantProjectID {
					t.Errorf("projectID = %v, want %v", *got.projectID, *tc.wantProjectID)
				}
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
		{toolCompleteTask, map[string]any{"task_id": "t1", "artifact": "link"}, "task:completed", true},
		{"add_task", map[string]any{"title": "fix bug"}, "task:added", true},
		{"log_decision", map[string]any{"title": "go with echo"}, "decision:logged", true},
		{"confirm_plan", map[string]any{"phases": `[{}]`, "decisions": `[{},{}]`}, "plan:confirmed", true},
		{"set_session_handoff", map[string]any{"intent": "next: finish PR"}, "session:handoff", true},
		{"start_work", map[string]any{"repo_name": "my-repo"}, "worksession:started", true},
		{"finish_work", map[string]any{"session_id": "abc-123"}, "worksession:finished", true},
		{"checkpoint_work", map[string]any{"session_id": "abc-123"}, "worksession:checkpointed", true},
		{"get_active_work", map[string]any{"repo_name": "my-repo"}, "", false},
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

// TestAutologSem_SaturationDropPath asserts the drop path in autoLogMiddleware:
// when autologSem is at capacity, the select default branch fires immediately
// (slog.Warn + drop), and the wrapped call returns without blocking.
// Pattern mirrors TestClassifySem_SelectDefaultDrops_WhenFull in middleware_classify_test.go.
func TestAutologSem_SaturationDropPath(t *testing.T) {
	mock := &mockGTDStore{}

	// Build a Server with a saturated autologSem (capacity = 50).
	const semCap = 50
	sem := make(chan struct{}, semCap)
	for i := 0; i < semCap; i++ {
		sem <- struct{}{}
	}
	t.Cleanup(func() {
		for len(sem) > 0 {
			<-sem
		}
	})

	srv := &Server{gtd: mock, autologSem: sem}
	mw := srv.autoLogMiddleware()
	wrapped := mw(successHandler)

	req := mcpmsg.CallToolRequest{}
	req.Params.Name = toolCompleteTask
	req.Params.Arguments = map[string]any{"task_id": "abc-123", "artifact": "https://github.com/pr/1"}

	// The call must return quickly — the goroutine launch is dropped, not queued.
	start := time.Now()
	res, err := wrapped(context.Background(), req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("wrapped handler returned unexpected error: %v", err)
	}
	if res == nil || res.IsError {
		t.Fatalf("wrapped handler returned error result")
	}
	// The response must arrive well under 100 ms — no goroutine was launched.
	if elapsed > 100*time.Millisecond {
		t.Errorf("call took %v; want < 100ms when semaphore is full", elapsed)
	}

	// Give any rogue goroutine a moment to fire (it must NOT, semaphore was full).
	time.Sleep(50 * time.Millisecond)
	logs := mock.recordedLogs()
	if len(logs) != 0 {
		t.Errorf("expected 0 log entries when semaphore is saturated, got %d", len(logs))
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
