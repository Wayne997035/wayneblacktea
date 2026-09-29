package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
)

// TestGTDStore_CreateTask_InvalidAssignee_SQLite verifies the p6-7
// domain-layer gate (sunk from the MCP-only guard in p6-6) rejects an
// unrecognized assignee value at CreateTask, regardless of caller.
func TestGTDStore_CreateTask_InvalidAssignee_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openMem(t, "")
	ctx := context.Background()

	_, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "bad assignee", Assignee: "gemini"})
	if err == nil {
		t.Fatal("CreateTask with unrecognized assignee must error")
	}
	if !errors.Is(err, gtd.ErrInvalidAssignee) {
		t.Errorf("expected gtd.ErrInvalidAssignee, got: %v", err)
	}
}

// TestGTDStore_CreateTask_ValidAssigneeNormalized_SQLite verifies an alias
// spelling ("claude-code") is normalized to its canonical form ("claude")
// before persisting, matching the MCP-layer behaviour.
func TestGTDStore_CreateTask_ValidAssigneeNormalized_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "aliased assignee", Assignee: "claude-code"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if !task.Assignee.Valid || task.Assignee.String != "claude" {
		t.Errorf("assignee persisted = %+v, want normalized \"claude\"", task.Assignee)
	}
}

// TestGTDStore_CreateTask_EmptyAssigneeAllowed_SQLite verifies an unowned
// (empty) assignee is still permitted — new tasks always insert as pending,
// so the in_progress guard never applies at creation time.
func TestGTDStore_CreateTask_EmptyAssigneeAllowed_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "unowned task"})
	if err != nil {
		t.Fatalf("CreateTask with no assignee must succeed: %v", err)
	}
	if task.Assignee.Valid {
		t.Errorf("expected NULL assignee, got %+v", task.Assignee)
	}
}

// TestGTDStore_BeginTask_RequiresAssignee_SQLite verifies BeginTask rejects a
// task with no existing assignee — the domain-layer gate applied to a path
// that has no assignee argument of its own.
func TestGTDStore_BeginTask_RequiresAssignee_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "unowned begin"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	_, err = s.BeginTask(ctx, task.ID)
	if err == nil {
		t.Fatal("BeginTask on an unowned task must error")
	}
	if !errors.Is(err, gtd.ErrAssigneeRequiredForInProgress) {
		t.Errorf("expected gtd.ErrAssigneeRequiredForInProgress, got: %v", err)
	}

	// Task must not have been mutated.
	reread, rerr := s.GetTaskByID(ctx, task.ID)
	if rerr != nil {
		t.Fatalf("GetTaskByID: %v", rerr)
	}
	if reread.Status != taskStatusPending {
		t.Errorf("status changed to %q despite rejected BeginTask", reread.Status)
	}
}

// TestGTDStore_BeginTask_ExistingAssigneeNotBlocked_SQLite verifies a task
// that already has an assignee begins normally with no extra plumbing.
func TestGTDStore_BeginTask_ExistingAssigneeNotBlocked_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "owned begin", Assignee: "human"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := s.BeginTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("BeginTask on an owned task must succeed: %v", err)
	}
	if got.Status != statusInProgress {
		t.Errorf("status = %q, want in_progress", got.Status)
	}
}

// TestGTDStore_UpdateTaskStatus_RequiresAssignee_SQLite verifies
// UpdateTaskStatus rejects a pending→in_progress transition when the task
// has no assignee — the same guard applied to a second no-assignee-argument
// path.
func TestGTDStore_UpdateTaskStatus_RequiresAssignee_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "unowned status"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	_, err = s.UpdateTaskStatus(ctx, task.ID, gtd.TaskStatusInProgress)
	if err == nil {
		t.Fatal("UpdateTaskStatus to in_progress on an unowned task must error")
	}
	if !errors.Is(err, gtd.ErrAssigneeRequiredForInProgress) {
		t.Errorf("expected gtd.ErrAssigneeRequiredForInProgress, got: %v", err)
	}
}

// TestGTDStore_UpdateTaskStatus_NonInProgress_NoAssigneeNeeded_SQLite
// verifies the guard is scoped to in_progress only — cancelling an unowned
// task must not require an assignee.
func TestGTDStore_UpdateTaskStatus_NonInProgress_NoAssigneeNeeded_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "unowned cancel"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := s.UpdateTaskStatus(ctx, task.ID, gtd.TaskStatusCancelled)
	if err != nil {
		t.Fatalf("UpdateTaskStatus to cancelled on an unowned task must succeed: %v", err)
	}
	if got.Status != "cancelled" {
		t.Errorf("status = %q, want cancelled", got.Status)
	}
}

// TestGTDStore_UpdateTask_InvalidAssignee_SQLite verifies UpdateTask rejects
// an unrecognized assignee value supplied on the call.
func TestGTDStore_UpdateTask_InvalidAssignee_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "bad update assignee"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	bad := "gemini"
	_, err = s.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{Assignee: &bad})
	if err == nil {
		t.Fatal("UpdateTask with unrecognized assignee must error")
	}
	if !errors.Is(err, gtd.ErrInvalidAssignee) {
		t.Errorf("expected gtd.ErrInvalidAssignee, got: %v", err)
	}
	for _, canonical := range gtd.CanonicalActors {
		if !strings.Contains(err.Error(), canonical) {
			t.Errorf("error should list canonical value %q, got: %v", canonical, err)
		}
	}
}

// TestGTDStore_UpdateTask_InProgressNoAssignee_SQLite verifies UpdateTask
// rejects a status=in_progress patch that leaves the merged assignee empty
// (no existing assignee, none supplied this call).
func TestGTDStore_UpdateTask_InProgressNoAssignee_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "unowned update"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	status := string(gtd.TaskStatusInProgress)
	_, err = s.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{Status: &status})
	if err == nil {
		t.Fatal("UpdateTask to in_progress on an unowned task must error")
	}
	if !errors.Is(err, gtd.ErrAssigneeRequiredForInProgress) {
		t.Errorf("expected gtd.ErrAssigneeRequiredForInProgress, got: %v", err)
	}
}

// TestGTDStore_UpdateTask_InProgressClearingAssignee_SQLite verifies UpdateTask
// rejects a call that simultaneously sets status=in_progress AND clears an
// EXISTING assignee to empty — the merged result (not just the two fields in
// isolation) drives the guard.
func TestGTDStore_UpdateTask_InProgressClearingAssignee_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "clearing assignee", Assignee: "claude"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	status := string(gtd.TaskStatusInProgress)
	empty := ""
	_, err = s.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{Status: &status, Assignee: &empty})
	if err == nil {
		t.Fatal("UpdateTask to in_progress while clearing assignee must error")
	}
	if !errors.Is(err, gtd.ErrAssigneeRequiredForInProgress) {
		t.Errorf("expected gtd.ErrAssigneeRequiredForInProgress, got: %v", err)
	}
}

// TestGTDStore_UpdateTask_InProgressWithNewAssignee_SQLite verifies UpdateTask
// succeeds when assignee is supplied on the SAME call that sets in_progress.
func TestGTDStore_UpdateTask_InProgressWithNewAssignee_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "new assignee this call"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	status := string(gtd.TaskStatusInProgress)
	assignee := "codex"
	updated, err := s.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{Status: &status, Assignee: &assignee})
	if err != nil {
		t.Fatalf("UpdateTask with assignee supplied this call must succeed: %v", err)
	}
	if updated.Status != statusInProgress {
		t.Errorf("status = %q, want in_progress", updated.Status)
	}
	if !updated.Assignee.Valid || updated.Assignee.String != "codex" {
		t.Errorf("assignee = %+v, want \"codex\"", updated.Assignee)
	}
}

// TestGTDStore_UpdateTask_InProgressWithExistingAssignee_SQLite verifies
// UpdateTask succeeds when the task already has an assignee and this call
// only changes status.
func TestGTDStore_UpdateTask_InProgressWithExistingAssignee_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "existing assignee", Assignee: "human"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	status := string(gtd.TaskStatusInProgress)
	updated, err := s.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{Status: &status})
	if err != nil {
		t.Fatalf("UpdateTask to in_progress on an already-owned task must succeed: %v", err)
	}
	if updated.Status != statusInProgress {
		t.Errorf("status = %q, want in_progress", updated.Status)
	}
	if !updated.Assignee.Valid || updated.Assignee.String != "human" {
		t.Errorf("assignee = %+v, want preserved \"human\"", updated.Assignee)
	}
}

// assertGuardedUpdateRejectsBlankSQLite seeds title as a pending task with
// the given raw assignee via CreateTask (which stores blank/whitespace-only
// values unchanged — TrimSpace-empty skips NormalizeActor, see CreateTask's
// assignee handling — the same way a concurrent writer could produce them),
// then asserts the guarded UPDATE's own WHERE clause alone rejects it:
// sqlite.ExecUpdateTaskStatusGuardedSQLForTest affects 0 rows and the row
// stays pending. Shared by
// TestGTDStore_UpdateTaskStatusGuarded_SQLRejectsBlankAssignee_SQLite's
// fixed case table and its full-AssigneeSpaceChars-charset sweep so gocyclo
// counts one function instead of two duplicated branch chains.
func assertGuardedUpdateRejectsBlankSQLite(t *testing.T, s *sqlite.GTDStore, ctx context.Context, title, assignee string) {
	t.Helper()
	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: title, Assignee: assignee})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	affected, err := sqlite.ExecUpdateTaskStatusGuardedSQLForTest(s, ctx, task.ID, gtd.TaskStatusInProgress, gtd.TaskStatusPending)
	if err != nil {
		t.Fatalf("ExecUpdateTaskStatusGuardedSQLForTest: %v", err)
	}
	if affected != 0 {
		t.Fatalf("rowsAffected = %d, want 0 (blank assignee must not reach in_progress)", affected)
	}

	reread, rerr := s.GetTaskByID(ctx, task.ID)
	if rerr != nil {
		t.Fatalf("GetTaskByID: %v", rerr)
	}
	if reread.Status != taskStatusPending {
		t.Errorf("status = %q, want pending", reread.Status)
	}
}

// TestGTDStore_UpdateTaskStatusGuarded_SQLRejectsBlankAssignee_SQLite
// verifies the guarded UPDATE's own WHERE clause — not just the Go pre-read
// in UpdateTaskStatusGuarded — rejects every blank-assignee variant
// RequireAssigneeForInProgress rejects, plus (in the full-charset sweep
// below) every single rune in AssigneeSpaceChars individually, since SV r3
// only verified the charset against the system sqlite3 CLI, not the actual
// modernc.org/sqlite driver this backend runs in production.
func TestGTDStore_UpdateTaskStatusGuarded_SQLRejectsBlankAssignee_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openMem(t, "")
	ctx := context.Background()

	tab := string(rune(0x09))
	asciiSpace := " "
	ideographicSpace := string(rune(0x3000)) // U+3000, CJK fullwidth space

	cases := []struct {
		name     string
		assignee string // "" seeds NULL/empty (CreateTask collapses both)
	}{
		{name: "null/empty", assignee: ""},
		{name: "tab", assignee: tab},
		{name: "ascii space", assignee: asciiSpace},
		{name: "ideographic space U+3000", assignee: ideographicSpace},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertGuardedUpdateRejectsBlankSQLite(t, s, ctx, "blank assignee sql "+tc.name, tc.assignee)
		})
	}

	t.Run("legit actor succeeds", func(t *testing.T) {
		task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "legit assignee sql", Assignee: "claude"})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}

		affected, err := sqlite.ExecUpdateTaskStatusGuardedSQLForTest(s, ctx, task.ID, gtd.TaskStatusInProgress, gtd.TaskStatusPending)
		if err != nil {
			t.Fatalf("ExecUpdateTaskStatusGuardedSQLForTest: %v", err)
		}
		if affected != 1 {
			t.Fatalf("rowsAffected = %d, want 1", affected)
		}
	})

	// Full-charset sweep: SV r3 verified all 25 unicode.IsSpace runes trim
	// correctly against the system sqlite3 CLI, but production runs
	// modernc.org/sqlite, which had never been exercised against the full
	// AssigneeSpaceChars set. One rune at a time so a single divergent rune
	// fails its own subtest instead of being masked by the others.
	t.Run("full AssigneeSpaceChars charset", func(t *testing.T) {
		for _, r := range gtd.AssigneeSpaceChars {
			t.Run(fmt.Sprintf("U+%04X", r), func(t *testing.T) {
				assertGuardedUpdateRejectsBlankSQLite(t, s, ctx, fmt.Sprintf("charset sweep U+%04X", r), string(r))
			})
		}
	})
}

// TestGTDStore_UpdateTaskStatusGuarded_RequiresAssignee_SQLite verifies the
// method itself (Go pre-read + SQL layer together) rejects a
// pending->in_progress transition when the task has no assignee, and
// succeeds when it does.
func TestGTDStore_UpdateTaskStatusGuarded_RequiresAssignee_SQLite(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openMem(t, "")
	ctx := context.Background()

	t.Run("no assignee rejected", func(t *testing.T) {
		task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "guarded unowned"})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}

		_, err = s.UpdateTaskStatusGuarded(ctx, task.ID, gtd.TaskStatusInProgress, gtd.TaskStatusPending)
		if !errors.Is(err, gtd.ErrAssigneeRequiredForInProgress) {
			t.Fatalf("UpdateTaskStatusGuarded err = %v, want gtd.ErrAssigneeRequiredForInProgress", err)
		}

		reread, rerr := s.GetTaskByID(ctx, task.ID)
		if rerr != nil {
			t.Fatalf("GetTaskByID: %v", rerr)
		}
		if reread.Status != taskStatusPending {
			t.Errorf("status = %q, want pending", reread.Status)
		}
	})

	t.Run("with assignee succeeds", func(t *testing.T) {
		task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "guarded owned", Assignee: "human"})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}

		got, err := s.UpdateTaskStatusGuarded(ctx, task.ID, gtd.TaskStatusInProgress, gtd.TaskStatusPending)
		if err != nil {
			t.Fatalf("UpdateTaskStatusGuarded: %v", err)
		}
		if got.Status != statusInProgress {
			t.Errorf("status = %q, want in_progress", got.Status)
		}
	})
}
