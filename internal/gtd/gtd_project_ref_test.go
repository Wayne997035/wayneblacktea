package gtd_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

const (
	projectStateOmitted = "omitted"
	projectStateClear   = "clear"
	projectStateSet     = "set"
)

// projectStateOf reads UpdateTaskParams.Project the way every store's UpdateTask
// must: by nil-ness alone, nothing else. Project == nil is omitted, a non-nil
// Project with a nil ID is clear, a non-nil Project with an ID is set.
func projectStateOf(p gtd.UpdateTaskParams) (state string, id *uuid.UUID) {
	switch {
	case p.Project == nil:
		return projectStateOmitted, nil
	case p.Project.ID == nil:
		return projectStateClear, nil
	default:
		return projectStateSet, p.Project.ID
	}
}

// TestUpdateTaskProjectContract pins the Go-side contract that the Postgres,
// SQLite and MCP layers of update_task project_id are built on. [F1011-11]
//
//   - gtd.ErrProjectNotFound is a sentinel of its own: it does not wrap
//     gtd.ErrNotFound and gtd.ErrNotFound does not wrap it, so a missing project
//     can never be reported as a missing task.
//   - gtd.UpdateTaskParams.Project has three states and no fourth: omitted,
//     clear and set, told apart by nil-ness alone.
//
// What a store does with those states is [F1011-16]: its conformance rows live
// in internal/testutil/conformance/gtd.go (UpdateTask_ProjectID_*) and are run
// by TestGTDConformance_Postgres and TestGTDConformance_SQLite in
// parity_smoke_test.go. This test needs no database.
func TestUpdateTaskProjectContract(t *testing.T) {
	t.Run("sentinels", func(t *testing.T) {
		wrappedProject := fmt.Errorf("updating task: %w", gtd.ErrProjectNotFound)
		wrappedTask := fmt.Errorf("updating task: %w", gtd.ErrNotFound)
		cases := []struct {
			name   string
			err    error
			target error
			want   bool
		}{
			{"project sentinel matches itself", gtd.ErrProjectNotFound, gtd.ErrProjectNotFound, true},
			{"wrapped project sentinel matches itself", wrappedProject, gtd.ErrProjectNotFound, true},
			{"project sentinel is not ErrNotFound", gtd.ErrProjectNotFound, gtd.ErrNotFound, false},
			{"ErrNotFound is not the project sentinel", gtd.ErrNotFound, gtd.ErrProjectNotFound, false},
			{"wrapped project sentinel is not ErrNotFound", wrappedProject, gtd.ErrNotFound, false},
			{"wrapped ErrNotFound is not the project sentinel", wrappedTask, gtd.ErrProjectNotFound, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := errors.Is(tc.err, tc.target); got != tc.want {
					t.Errorf("errors.Is(%v, %v) = %v, want %v", tc.err, tc.target, got, tc.want)
				}
			})
		}
	})

	t.Run("tri-state", func(t *testing.T) {
		proj := uuid.New()
		nilUUID := uuid.Nil
		title := "renamed"
		cases := []struct {
			name      string
			params    gtd.UpdateTaskParams
			wantState string
			wantID    *uuid.UUID
		}{
			{"zero value is omitted", gtd.UpdateTaskParams{}, projectStateOmitted, nil},
			{"a title-only update is omitted", gtd.UpdateTaskParams{Title: &title}, projectStateOmitted, nil},
			{"empty ProjectRef is clear", gtd.UpdateTaskParams{Project: &gtd.ProjectRef{}}, projectStateClear, nil},
			{"ProjectRef with an ID is set", gtd.UpdateTaskParams{Project: &gtd.ProjectRef{ID: &proj}}, projectStateSet, &proj},
			{
				"ProjectRef with the nil UUID is set, not clear",
				gtd.UpdateTaskParams{Project: &gtd.ProjectRef{ID: &nilUUID}},
				projectStateSet, &nilUUID,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				gotState, gotID := projectStateOf(tc.params)
				if gotState != tc.wantState {
					t.Errorf("state = %q, want %q", gotState, tc.wantState)
				}
				if tc.wantID == nil && gotID != nil {
					t.Errorf("ID = %v, want nil", *gotID)
				}
				if tc.wantID != nil && (gotID == nil || *gotID != *tc.wantID) {
					t.Errorf("ID = %v, want %v", gotID, *tc.wantID)
				}
			})
		}
	})
}
