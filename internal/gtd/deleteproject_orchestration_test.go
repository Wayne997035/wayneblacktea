package gtd_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// fakeDeleteProjectAdapter is a scripted gtd.DeleteProjectAdapter. Unlike the
// delete-task fake it records the call order AND is driven by a name->error
// map, because the thing most worth asserting here is the ORDER of thirteen
// cleanup steps: every one of them filters on "the tasks under this project",
// and any that runs after DeleteTaskRows silently matches nothing.
type fakeDeleteProjectAdapter struct {
	beginTxErr error

	exists      bool
	precheckErr error

	taskCount     int
	countTasksErr error

	// failOn maps a recorded call name to the error that call returns.
	failOn map[string]error

	commitErr error

	calls        []string
	rollbackDone bool
}

func (f *fakeDeleteProjectAdapter) record(name string) error {
	f.calls = append(f.calls, name)
	return f.failOn[name]
}

func (f *fakeDeleteProjectAdapter) BeginTx(context.Context) error {
	f.calls = append(f.calls, "BeginTx")
	return f.beginTxErr
}

func (f *fakeDeleteProjectAdapter) WorkspacePrecheck(context.Context) (bool, error) {
	f.calls = append(f.calls, "WorkspacePrecheck")
	if f.precheckErr != nil {
		return false, f.precheckErr
	}
	return f.exists, nil
}

func (f *fakeDeleteProjectAdapter) CountTasks(context.Context) (int, error) {
	f.calls = append(f.calls, "CountTasks")
	if f.countTasksErr != nil {
		return 0, f.countTasksErr
	}
	return f.taskCount, nil
}

func (f *fakeDeleteProjectAdapter) CleanupWorkSessionTasks(context.Context) error {
	return f.record("CleanupWorkSessionTasks")
}

func (f *fakeDeleteProjectAdapter) NullifyWorkSessionsCurrentTask(context.Context) error {
	return f.record("NullifyWorkSessionsCurrentTask")
}

func (f *fakeDeleteProjectAdapter) CleanupCompletionCandidates(context.Context) error {
	return f.record("CleanupCompletionCandidates")
}

func (f *fakeDeleteProjectAdapter) ResetPromotedVisionItems(context.Context) error {
	return f.record("ResetPromotedVisionItems")
}

func (f *fakeDeleteProjectAdapter) NullifyDecisionTaskRefs(context.Context) error {
	return f.record("NullifyDecisionTaskRefs")
}

func (f *fakeDeleteProjectAdapter) NullifyKnowledgeItemTaskRefs(context.Context) error {
	return f.record("NullifyKnowledgeItemTaskRefs")
}

func (f *fakeDeleteProjectAdapter) NullifyActivityLogProjectRefs(context.Context) error {
	return f.record("NullifyActivityLogProjectRefs")
}

func (f *fakeDeleteProjectAdapter) NullifyDecisionProjectRefs(context.Context) error {
	return f.record("NullifyDecisionProjectRefs")
}

func (f *fakeDeleteProjectAdapter) NullifyKnowledgeItemProjectRefs(context.Context) error {
	return f.record("NullifyKnowledgeItemProjectRefs")
}

func (f *fakeDeleteProjectAdapter) NullifySessionHandoffProjectRefs(context.Context) error {
	return f.record("NullifySessionHandoffProjectRefs")
}

func (f *fakeDeleteProjectAdapter) NullifyWorkSessionProjectRefs(context.Context) error {
	return f.record("NullifyWorkSessionProjectRefs")
}

func (f *fakeDeleteProjectAdapter) DeleteTaskRows(context.Context) error {
	return f.record("DeleteTaskRows")
}

func (f *fakeDeleteProjectAdapter) DeleteProjectRow(context.Context) error {
	return f.record("DeleteProjectRow")
}

func (f *fakeDeleteProjectAdapter) Commit(context.Context) error {
	f.calls = append(f.calls, callCommit)
	return f.commitErr
}

func (f *fakeDeleteProjectAdapter) Rollback(context.Context) {
	f.calls = append(f.calls, "Rollback")
	f.rollbackDone = true
}

// callCommit is the recorded name of the Commit step, named once so the
// happy-path list and the "was it committed?" checks cannot drift apart.
const callCommit = "Commit"

// happyPathCalls is the exact sequence a successful delete must produce. It
// is written out rather than generated so that reordering a step in the
// production code fails this test instead of silently agreeing with it.
var happyPathCalls = []string{
	"BeginTx",
	"WorkspacePrecheck",
	"CountTasks",
	"CleanupWorkSessionTasks",
	"NullifyWorkSessionsCurrentTask",
	"CleanupCompletionCandidates",
	"ResetPromotedVisionItems",
	"NullifyDecisionTaskRefs",
	"NullifyKnowledgeItemTaskRefs",
	"NullifyActivityLogProjectRefs",
	"NullifyDecisionProjectRefs",
	"NullifyKnowledgeItemProjectRefs",
	"NullifySessionHandoffProjectRefs",
	"NullifyWorkSessionProjectRefs",
	"DeleteTaskRows",
	"DeleteProjectRow",
	callCommit,
	"Rollback",
}

// TestDeleteProjectOrchestration_HappyPathOrder is the central guard. Red
// line #9 forbids foreign keys, so nothing in the database notices a cleanup
// that runs too late: DeleteTaskRows removes the very rows the task-level
// cleanups select, and a cleanup moved after it matches zero rows while
// still reporting success. That failure is invisible at runtime and shows up
// only as a column pointing at a task that no longer exists.
func TestDeleteProjectOrchestration_HappyPathOrder(t *testing.T) {
	f := &fakeDeleteProjectAdapter{exists: true, taskCount: 7}

	n, err := gtd.DeleteProjectOrchestration(context.Background(), uuid.New(), f)
	if err != nil {
		t.Fatalf("happy path must not error, got %v", err)
	}
	if n != 7 {
		t.Errorf("returned task count = %d, want 7 (the count read inside the tx)", n)
	}
	if !reflect.DeepEqual(f.calls, happyPathCalls) {
		t.Errorf("call sequence mismatch\n got: %v\nwant: %v", f.calls, happyPathCalls)
	}
}

// TestDeleteProjectOrchestration_EveryTaskCleanupPrecedesTheRowDelete states
// the ordering rule above as a property rather than as one fixed list, so it
// keeps biting when a new cleanup is added to the sequence — the fixed list
// would just be updated alongside it.
func TestDeleteProjectOrchestration_EveryTaskCleanupPrecedesTheRowDelete(t *testing.T) {
	f := &fakeDeleteProjectAdapter{exists: true}
	if _, err := gtd.DeleteProjectOrchestration(context.Background(), uuid.New(), f); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	idx := func(name string) int {
		for i, c := range f.calls {
			if c == name {
				return i
			}
		}
		t.Fatalf("%s was never called — the orchestration dropped a cleanup step", name)
		return -1
	}

	deleteRows := idx("DeleteTaskRows")
	// Every cleanup whose predicate is "task_id IN (tasks of this project)".
	for _, cleanup := range []string{
		"CleanupWorkSessionTasks",
		"NullifyWorkSessionsCurrentTask",
		"CleanupCompletionCandidates",
		"ResetPromotedVisionItems",
		"NullifyDecisionTaskRefs",
		"NullifyKnowledgeItemTaskRefs",
	} {
		if idx(cleanup) > deleteRows {
			t.Errorf("%s runs AFTER DeleteTaskRows — its rows are already gone, so it silently matches nothing",
				cleanup)
		}
	}
	if idx("DeleteProjectRow") < deleteRows {
		t.Error("DeleteProjectRow runs before DeleteTaskRows; the project row must go last")
	}
}

// TestDeleteProjectOrchestration_MissingProjectIsSilentNoOp pins the contract
// shared with DeleteTaskOrchestration: deleting something already gone is a
// quiet zero, not an error, so a retried delete does not look like a failure.
func TestDeleteProjectOrchestration_MissingProjectIsSilentNoOp(t *testing.T) {
	f := &fakeDeleteProjectAdapter{exists: false, taskCount: 99}

	n, err := gtd.DeleteProjectOrchestration(context.Background(), uuid.New(), f)
	if err != nil {
		t.Fatalf("a missing project must not error, got %v", err)
	}
	if n != 0 {
		t.Errorf("returned %d, want 0 — nothing was deleted", n)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "Delete") || strings.HasPrefix(c, "Nullify") || strings.HasPrefix(c, "Cleanup") {
			t.Fatalf("precheck said the project is not in this workspace, yet %s ran", c)
		}
	}
	if !f.rollbackDone {
		t.Error("the transaction was left open after the no-op")
	}
}

// TestDeleteProjectOrchestration_CountIsReadBeforeAnyDeletion proves the
// returned number counts what was actually removed. Reading it after
// DeleteTaskRows would always return 0 — and 0 is indistinguishable from
// "this project had no tasks", so the bug would read as a valid answer.
func TestDeleteProjectOrchestration_CountIsReadBeforeAnyDeletion(t *testing.T) {
	f := &fakeDeleteProjectAdapter{exists: true, taskCount: 3}
	if _, err := gtd.DeleteProjectOrchestration(context.Background(), uuid.New(), f); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	countAt, deleteAt := -1, -1
	for i, c := range f.calls {
		switch c {
		case "CountTasks":
			countAt = i
		case "DeleteTaskRows":
			deleteAt = i
		}
	}
	if countAt == -1 || deleteAt == -1 {
		t.Fatalf("probe broken: CountTasks=%d DeleteTaskRows=%d in %v", countAt, deleteAt, f.calls)
	}
	if countAt > deleteAt {
		t.Error("CountTasks runs after DeleteTaskRows — the count would always be 0")
	}
}

// TestDeleteProjectOrchestration_StepFailureStopsAndRollsBack walks every
// step: each one, when it fails, must abort the sequence, name itself in the
// error, and leave nothing committed. A step whose error is swallowed would
// commit a half-cleaned project.
func TestDeleteProjectOrchestration_StepFailureStopsAndRollsBack(t *testing.T) {
	steps := []string{
		"CleanupWorkSessionTasks",
		"NullifyWorkSessionsCurrentTask",
		"CleanupCompletionCandidates",
		"ResetPromotedVisionItems",
		"NullifyDecisionTaskRefs",
		"NullifyKnowledgeItemTaskRefs",
		"NullifyActivityLogProjectRefs",
		"NullifyDecisionProjectRefs",
		"NullifyKnowledgeItemProjectRefs",
		"NullifySessionHandoffProjectRefs",
		"NullifyWorkSessionProjectRefs",
		"DeleteTaskRows",
		"DeleteProjectRow",
	}
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			boom := fmt.Errorf("%s exploded", step)
			f := &fakeDeleteProjectAdapter{
				exists:    true,
				taskCount: 5,
				failOn:    map[string]error{step: boom},
			}

			n, err := gtd.DeleteProjectOrchestration(context.Background(), uuid.New(), f)
			if err == nil {
				t.Fatalf("%s failed but the orchestration reported success", step)
			}
			if !errors.Is(err, boom) {
				t.Errorf("error does not wrap the underlying failure: %v", err)
			}
			if n != 0 {
				t.Errorf("returned %d tasks on a failed delete, want 0", n)
			}
			for _, c := range f.calls {
				if c == callCommit {
					t.Fatalf("%s failed, yet the transaction was committed", step)
				}
			}
			if !f.rollbackDone {
				t.Errorf("%s failed without a rollback", step)
			}
			if last := f.calls[len(f.calls)-2]; last != step {
				t.Errorf("steps kept running after %s failed; sequence was %v", step, f.calls)
			}
		})
	}
}

// TestDeleteProjectOrchestration_BeginAndPrecheckAndCountFailures covers the
// three calls that happen before the step loop.
func TestDeleteProjectOrchestration_BeginAndPrecheckAndCountFailures(t *testing.T) {
	boom := errors.New("boom")

	t.Run("BeginTx", func(t *testing.T) {
		f := &fakeDeleteProjectAdapter{beginTxErr: boom}
		if _, err := gtd.DeleteProjectOrchestration(context.Background(), uuid.New(), f); !errors.Is(err, boom) {
			t.Fatalf("want wrapped boom, got %v", err)
		}
		if f.rollbackDone {
			t.Error("rolled back a transaction that never opened")
		}
	})

	t.Run("WorkspacePrecheck", func(t *testing.T) {
		f := &fakeDeleteProjectAdapter{precheckErr: boom}
		if _, err := gtd.DeleteProjectOrchestration(context.Background(), uuid.New(), f); !errors.Is(err, boom) {
			t.Fatalf("want wrapped boom, got %v", err)
		}
		if !f.rollbackDone {
			t.Error("precheck failed without a rollback")
		}
	})

	t.Run("CountTasks", func(t *testing.T) {
		f := &fakeDeleteProjectAdapter{exists: true, countTasksErr: boom}
		if _, err := gtd.DeleteProjectOrchestration(context.Background(), uuid.New(), f); !errors.Is(err, boom) {
			t.Fatalf("want wrapped boom, got %v", err)
		}
		for _, c := range f.calls {
			if strings.HasPrefix(c, "Cleanup") || strings.HasPrefix(c, "Nullify") {
				t.Fatalf("CountTasks failed, yet %s ran", c)
			}
		}
	})
}

// TestDeleteProjectOrchestration_CommitFailureIsReported guards the last
// step: a failed commit must not be reported as a successful delete.
func TestDeleteProjectOrchestration_CommitFailureIsReported(t *testing.T) {
	boom := errors.New("commit refused")
	f := &fakeDeleteProjectAdapter{exists: true, taskCount: 4, commitErr: boom}

	n, err := gtd.DeleteProjectOrchestration(context.Background(), uuid.New(), f)
	if !errors.Is(err, boom) {
		t.Fatalf("want wrapped commit error, got %v", err)
	}
	if n != 0 {
		t.Errorf("returned %d tasks after a failed commit, want 0", n)
	}
}
