package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// triStateProject creates a project named name in s and returns its ID.
func triStateProject(t *testing.T, s *sqlite.GTDStore, name string) uuid.UUID {
	t.Helper()
	p, err := s.CreateProject(context.Background(), gtd.CreateProjectParams{Name: name, Title: name})
	if err != nil {
		t.Fatalf("CreateProject(%s): %v", name, err)
	}
	return p.ID
}

// triStateTask creates a task titled title in s, attached to projectID when it
// is non-nil, and returns its ID.
func triStateTask(t *testing.T, s *sqlite.GTDStore, title string, projectID *uuid.UUID) uuid.UUID {
	t.Helper()
	task, err := s.CreateTask(context.Background(), gtd.CreateTaskParams{ProjectID: projectID, Title: title})
	if err != nil {
		t.Fatalf("CreateTask(%s): %v", title, err)
	}
	return task.ID
}

// wantStoredProject reads the task back through GetTaskByID (not from the
// UpdateTask return value) and fails unless its project_id is want, where a nil
// want means NULL.
func wantStoredProject(t *testing.T, s *sqlite.GTDStore, taskID uuid.UUID, want *uuid.UUID, when string) {
	t.Helper()
	got, err := s.GetTaskByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetTaskByID (%s): %v", when, err)
	}
	switch {
	case want == nil && got.ProjectID.Valid:
		t.Errorf("%s: stored project_id = %s, want NULL", when, uuid.UUID(got.ProjectID.Bytes))
	case want != nil && !got.ProjectID.Valid:
		t.Errorf("%s: stored project_id = NULL, want %s", when, *want)
	case want != nil && uuid.UUID(got.ProjectID.Bytes) != *want:
		t.Errorf("%s: stored project_id = %s, want %s", when, uuid.UUID(got.ProjectID.Bytes), *want)
	}
}

// wantStoredTitle fails unless the stored title of the task is want.
func wantStoredTitle(t *testing.T, s *sqlite.GTDStore, taskID uuid.UUID, want, when string) {
	t.Helper()
	got, err := s.GetTaskByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetTaskByID (%s): %v", when, err)
	}
	if got.Title != want {
		t.Errorf("%s: Title = %q, want %q", when, got.Title, want)
	}
}

// TestUpdateTask_ProjectID_TriState drives the SQLite store directly through the
// three states of gtd.UpdateTaskParams.Project and the two rejections that must
// write nothing.
// [F1011-13]
func TestUpdateTask_ProjectID_TriState(t *testing.T) {
	t.Parallel() // [F0925-10]

	cases := []struct {
		name string
		run  func(t *testing.T, s *sqlite.GTDStore)
	}{
		{
			// Project == nil: the column keeps its stored value. The title changes
			// in the same call so the UPDATE demonstrably ran.
			name: "omit",
			run: func(t *testing.T, s *sqlite.GTDStore) {
				proj := triStateProject(t, s, "tri-omit")
				task := triStateTask(t, s, "tri-omit-task", &proj)

				renamed := "tri-omit-renamed"
				if _, err := s.UpdateTask(context.Background(), task, gtd.UpdateTaskParams{Title: &renamed}); err != nil {
					t.Fatalf("UpdateTask(Title only): %v", err)
				}
				wantStoredTitle(t, s, task, renamed, "after title-only update")
				wantStoredProject(t, s, task, &proj, "after title-only update")
			},
		},
		{
			// Project != nil, ID == nil: the column becomes NULL.
			name: "clear",
			run: func(t *testing.T, s *sqlite.GTDStore) {
				proj := triStateProject(t, s, "tri-clear")
				task := triStateTask(t, s, "tri-clear-task", &proj)

				if _, err := s.UpdateTask(context.Background(), task, gtd.UpdateTaskParams{Project: &gtd.ProjectRef{}}); err != nil {
					t.Fatalf("UpdateTask(Project:clear): %v", err)
				}
				wantStoredProject(t, s, task, nil, "after clear")
			},
		},
		{
			// Project != nil, ID != nil: the column becomes *ID. The task starts on
			// another project, so a write that only filled a NULL column would fail.
			name: "set",
			run: func(t *testing.T, s *sqlite.GTDStore) {
				from := triStateProject(t, s, "tri-set-from")
				to := triStateProject(t, s, "tri-set-to")
				task := triStateTask(t, s, "tri-set-task", &from)

				if _, err := s.UpdateTask(context.Background(), task, gtd.UpdateTaskParams{Project: &gtd.ProjectRef{ID: &to}}); err != nil {
					t.Fatalf("UpdateTask(Project:set): %v", err)
				}
				wantStoredProject(t, s, task, &to, "after set")
			},
		},
		{
			// An unknown project is ErrProjectNotFound, never ErrNotFound (the MCP
			// layer would blame the task), and the Title in the same call is not
			// written either.
			name: "unknown",
			run: func(t *testing.T, s *sqlite.GTDStore) {
				const original = "tri-unknown-task"
				proj := triStateProject(t, s, "tri-unknown")
				task := triStateTask(t, s, original, &proj)

				missing := uuid.New()
				rejected := "should not land"
				_, err := s.UpdateTask(context.Background(), task, gtd.UpdateTaskParams{
					Title:   &rejected,
					Project: &gtd.ProjectRef{ID: &missing},
				})
				if !errors.Is(err, gtd.ErrProjectNotFound) {
					t.Errorf("UpdateTask(unknown project): err = %v, want errors.Is(err, gtd.ErrProjectNotFound)", err)
				}
				if errors.Is(err, gtd.ErrNotFound) {
					t.Errorf("UpdateTask(unknown project): err = %v, must not be errors.Is(err, gtd.ErrNotFound)", err)
				}
				wantStoredTitle(t, s, task, original, "after rejected update")
				wantStoredProject(t, s, task, &proj, "after rejected update")
			},
		},
		{
			// The task is looked at first: an unknown task paired with an unknown
			// project answers ErrNotFound. A store that checked the project first
			// would answer ErrProjectNotFound here.
			name: "unknown_task",
			run: func(t *testing.T, s *sqlite.GTDStore) {
				missing := uuid.New()
				_, err := s.UpdateTask(context.Background(), uuid.New(), gtd.UpdateTaskParams{Project: &gtd.ProjectRef{ID: &missing}})
				if !errors.Is(err, gtd.ErrNotFound) {
					t.Errorf("UpdateTask(unknown task + unknown project): err = %v, want errors.Is(err, gtd.ErrNotFound)", err)
				}
				if errors.Is(err, gtd.ErrProjectNotFound) {
					t.Errorf("UpdateTask(unknown task + unknown project): err = %v, must not be errors.Is(err, gtd.ErrProjectNotFound)", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel() // [F0925-10]
			tc.run(t, openMem(t, ""))
		})
	}
}
