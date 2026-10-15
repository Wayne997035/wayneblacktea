package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

const (
	// testDeadlockCode is spelled out and not taken from deadlockSQLState: a test that reads the constant
	// under test stays green when someone changes it.
	testDeadlockCode        = "40P01"
	testLockUnavailableCode = "55P03"
	// victimNeverRecovers is more failures than any retry budget outlasts.
	victimNeverRecovers = 99
)

// victimGTD stands in for Server.gtd. Its first `failures` delete calls (of either kind) return errFor(id)
// instead of reaching the store; every later call is passed on. calls counts every delete call. [F203-01]
type victimGTD struct {
	gtd.StoreIface
	failures int
	errFor   func(id uuid.UUID) error
	calls    int
}

func (v *victimGTD) DeleteTask(ctx context.Context, id uuid.UUID, actor string) error {
	v.calls++
	if v.calls <= v.failures {
		return v.errFor(id)
	}
	return v.StoreIface.DeleteTask(ctx, id, actor)
}

func (v *victimGTD) DeleteProjectExpecting(ctx context.Context, id uuid.UUID, actor, expectedTaskSet string) (int, error) {
	v.calls++
	if v.calls <= v.failures {
		return 0, v.errFor(id)
	}
	return v.StoreIface.DeleteProjectExpecting(ctx, id, actor, expectedTaskSet)
}

// The errors below have the shape the Store returns: the database error wrapped with %w by the step that
// failed (internal/gtd/deletetask_orchestration.go, deleteproject_orchestration.go). A bare *pgconn.PgError
// would pass a check that does not unwrap, and production never returns one.

func taskStoreErr(code string) func(uuid.UUID) error {
	return func(id uuid.UUID) error {
		return fmt.Errorf("delete task %s: cleanup completion_candidates: %w", id, &pgconn.PgError{Code: code})
	}
}

func projectStoreErr(code string) func(uuid.UUID) error {
	return func(id uuid.UUID) error {
		return fmt.Errorf("delete project %s: nullify work_sessions.project_id: %w", id, &pgconn.PgError{Code: code})
	}
}

func projectTasksChangedErr(id uuid.UUID) error {
	return fmt.Errorf("delete project %s: lock task rows: %w", id, gtd.ErrProjectTasksChanged)
}

// withVictim puts a victimGTD in front of s.gtd.
func withVictim(s *Server, failures int, errFor func(uuid.UUID) error) *victimGTD {
	v := &victimGTD{StoreIface: s.gtd, failures: failures, errFor: errFor}
	s.gtd = v
	return v
}

// confirmDeleteTask runs both steps of delete_task and returns the confirm step's result.
func confirmDeleteTask(t *testing.T, s *Server, id uuid.UUID) *mcpmsg.CallToolResult {
	t.Helper()
	token := extractToken(t, callDeleteTask(t, s, map[string]any{"task_id": id.String()}))
	return callDeleteTask(t, s, map[string]any{"task_id": id.String(), "confirm": true, "deletion_token": token})
}

// confirmDeleteProject runs both steps of delete_project and returns the confirm step's result.
func confirmDeleteProject(t *testing.T, s *Server, id uuid.UUID) *mcpmsg.CallToolResult {
	t.Helper()
	token := extractToken(t, callDeleteProject(t, s, map[string]any{"project_id": id.String()}))
	return callDeleteProject(t, s, map[string]any{"project_id": id.String(), "confirm": true, "deletion_token": token})
}

// [F203-01] A confirmed delete that PostgreSQL picks as a deadlock victim (SQLSTATE 40P01) is run again by
// the tool, at most three more times, and no other error is retried. The store is real (SQLite); only the
// first calls are replaced by an error of the shape the Store returns. The two gives_up cells state the
// attempt count as the literal 4 (user decision: at most 3 retries) and not as deleteDeadlockRetries+1: a
// test written against the constant would follow it to any new value and pin nothing.
func TestDeleteTools_DeadlockVictimRetry(t *testing.T) {
	cells := []struct {
		name string
		run  func(*testing.T)
	}{
		{"delete_task/recovers", testDeleteTaskRecovers},
		{"delete_task/gives_up", testDeleteTaskGivesUp},
		{"delete_task/other_error_not_retried", testDeleteTaskOtherErrorNotRetried},
		{"delete_project/recovers", testDeleteProjectRecovers},
		{"delete_project/gives_up", testDeleteProjectGivesUp},
		{"delete_project/tasks_changed_not_retried", testDeleteProjectTasksChangedNotRetried},
	}
	for _, c := range cells {
		t.Run(c.name, c.run)
	}
}

func testDeleteTaskRecovers(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	v := withVictim(s, 2, taskStoreErr(testDeadlockCode))

	r := confirmDeleteTask(t, s, id)
	if r.IsError || resultText(r) != "task deleted" {
		t.Errorf("confirm after two deadlock victims = (error=%v) %q, want the text %q", r.IsError, resultText(r), "task deleted")
	}
	if v.calls != 3 {
		t.Errorf("DeleteTask was called %d times, want 3 (two victims, then the delete)", v.calls)
	}
	if _, err := s.gtd.GetTaskByID(context.Background(), id); !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("GetTaskByID after the delete = %v, want gtd.ErrNotFound", err)
	}
}

func testDeleteTaskGivesUp(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	v := withVictim(s, victimNeverRecovers, taskStoreErr(testDeadlockCode))

	r := confirmDeleteTask(t, s, id)
	if !r.IsError {
		t.Errorf("confirm with every attempt a deadlock victim succeeded: %q", resultText(r))
	}
	if v.calls != 4 {
		t.Errorf("DeleteTask was called %d times, want 4 (the first call and three retries)", v.calls)
	}
}

func testDeleteTaskOtherErrorNotRetried(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	v := withVictim(s, victimNeverRecovers, taskStoreErr(testLockUnavailableCode))

	r := confirmDeleteTask(t, s, id)
	if !r.IsError {
		t.Errorf("confirm with a %s error succeeded: %q", testLockUnavailableCode, resultText(r))
	}
	if v.calls != 1 {
		t.Errorf("DeleteTask was called %d times for a %s error, want 1 (not a deadlock, not retried)", v.calls, testLockUnavailableCode)
	}
}

func testDeleteProjectRecovers(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id, _ := seedProjectWithTasks(t, s, 2)
	v := withVictim(s, 1, projectStoreErr(testDeadlockCode))

	r := confirmDeleteProject(t, s, id)
	if r.IsError || !strings.Contains(resultText(r), "project deleted") {
		t.Errorf("confirm after one deadlock victim = (error=%v) %q, want text containing %q", r.IsError, resultText(r), "project deleted")
	}
	if v.calls != 2 {
		t.Errorf("DeleteProjectExpecting was called %d times, want 2 (one victim, then the delete)", v.calls)
	}
	if projectStillExists(t, s, id) {
		t.Error("the project is still there after the retried delete reported success")
	}
}

func testDeleteProjectGivesUp(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id, _ := seedProjectWithTasks(t, s, 1)
	v := withVictim(s, victimNeverRecovers, projectStoreErr(testDeadlockCode))

	r := confirmDeleteProject(t, s, id)
	if !r.IsError {
		t.Errorf("confirm with every attempt a deadlock victim succeeded: %q", resultText(r))
	}
	if v.calls != 4 {
		t.Errorf("DeleteProjectExpecting was called %d times, want 4 (the first call and three retries)", v.calls)
	}
}

func testDeleteProjectTasksChangedNotRetried(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id, _ := seedProjectWithTasks(t, s, 1)
	v := withVictim(s, victimNeverRecovers, projectTasksChangedErr)

	r := confirmDeleteProject(t, s, id)
	if !r.IsError || !strings.Contains(resultText(r), "project changed since the preview") {
		t.Errorf("confirm = (error=%v) %q, want the error %q", r.IsError, resultText(r), "project changed since the preview")
	}
	if v.calls != 1 {
		t.Errorf("DeleteProjectExpecting was called %d times for ErrProjectTasksChanged, want 1 (not retried)", v.calls)
	}
}
