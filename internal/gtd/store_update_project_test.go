package gtd_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// projectIDTitle is the title every fixture task starts with; the cases that
// must prove "a rejected call wrote nothing" compare against it.
const projectIDTitle = "projectid-original"

// projectIDNewProject creates a project named name and returns its ID.
func projectIDNewProject(t *testing.T, store *gtd.Store, name string) uuid.UUID {
	t.Helper()
	p, err := store.CreateProject(context.Background(), gtd.CreateProjectParams{Name: name, Title: name})
	if err != nil {
		t.Fatalf("CreateProject(%s): %v", name, err)
	}
	return p.ID
}

// projectIDNewTask creates a task titled projectIDTitle, attached to projectID
// when it is non-nil, and returns its ID.
func projectIDNewTask(t *testing.T, store *gtd.Store, projectID *uuid.UUID) uuid.UUID {
	t.Helper()
	task, err := store.CreateTask(context.Background(), gtd.CreateTaskParams{ProjectID: projectID, Title: projectIDTitle})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return task.ID
}

// projectIDWantStored reads the task back through GetTaskByID, so a stale
// UpdateTask return value cannot satisfy it, and checks project_id against want
// (nil = NULL) and the title against wantTitle.
func projectIDWantStored(t *testing.T, store *gtd.Store, taskID uuid.UUID, want *uuid.UUID, wantTitle, when string) {
	t.Helper()
	got, err := store.GetTaskByID(context.Background(), taskID)
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
	if got.Title != wantTitle {
		t.Errorf("%s: stored title = %q, want %q", when, got.Title, wantTitle)
	}
}

// omit: an update that leaves Project nil must not rewrite project_id. The
// title changes so the UPDATE demonstrably ran.
func projectIDCaseOmit(t *testing.T, store *gtd.Store) {
	proj := projectIDNewProject(t, store, "projectid-omit")
	task := projectIDNewTask(t, store, &proj)

	renamed := "projectid-omit-renamed"
	if _, err := store.UpdateTask(context.Background(), task, gtd.UpdateTaskParams{Title: &renamed}); err != nil {
		t.Fatalf("UpdateTask(title only): %v", err)
	}
	projectIDWantStored(t, store, task, &proj, renamed, "after title-only update")
}

// clear: a non-nil ProjectRef with a nil ID writes NULL.
func projectIDCaseClear(t *testing.T, store *gtd.Store) {
	proj := projectIDNewProject(t, store, "projectid-clear")
	task := projectIDNewTask(t, store, &proj)

	got, err := store.UpdateTask(context.Background(), task, gtd.UpdateTaskParams{Project: &gtd.ProjectRef{}})
	if err != nil {
		t.Fatalf("UpdateTask(Project clear): %v", err)
	}
	if got.ProjectID.Valid {
		t.Errorf("returned row: project_id = %s, want NULL", uuid.UUID(got.ProjectID.Bytes))
	}
	projectIDWantStored(t, store, task, nil, projectIDTitle, "after clear")
}

// set: a ProjectRef carrying an existing project's ID moves the task there.
func projectIDCaseSet(t *testing.T, store *gtd.Store) {
	from := projectIDNewProject(t, store, "projectid-set-from")
	to := projectIDNewProject(t, store, "projectid-set-to")
	task := projectIDNewTask(t, store, &from)

	got, err := store.UpdateTask(context.Background(), task, gtd.UpdateTaskParams{Project: &gtd.ProjectRef{ID: &to}})
	if err != nil {
		t.Fatalf("UpdateTask(Project set): %v", err)
	}
	if !got.ProjectID.Valid || uuid.UUID(got.ProjectID.Bytes) != to {
		t.Errorf("returned row: project_id = %v, want %s", got.ProjectID, to)
	}
	projectIDWantStored(t, store, task, &to, projectIDTitle, "after set")
}

// unknown: a project that does not exist is ErrProjectNotFound, not
// ErrNotFound, and the same call's other fields are not written either.
func projectIDCaseUnknown(t *testing.T, store *gtd.Store) {
	proj := projectIDNewProject(t, store, "projectid-unknown")
	task := projectIDNewTask(t, store, &proj)

	missing := uuid.New()
	rejected := "should not land"
	_, err := store.UpdateTask(
		context.Background(), task,
		gtd.UpdateTaskParams{Title: &rejected, Project: &gtd.ProjectRef{ID: &missing}},
	)
	if !errors.Is(err, gtd.ErrProjectNotFound) {
		t.Errorf("err = %v, want errors.Is(err, gtd.ErrProjectNotFound)", err)
	}
	if errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("err = %v, must not be errors.Is(err, gtd.ErrNotFound)", err)
	}
	projectIDWantStored(t, store, task, &proj, projectIDTitle, "after rejected update")
}

// unknown_task: an unknown task is ErrNotFound even when the project is unknown
// too, so the task is looked at before the project.
func projectIDCaseUnknownTask(t *testing.T, store *gtd.Store) {
	missing := uuid.New()
	_, err := store.UpdateTask(
		context.Background(), uuid.New(),
		gtd.UpdateTaskParams{Project: &gtd.ProjectRef{ID: &missing}},
	)
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("err = %v, want errors.Is(err, gtd.ErrNotFound)", err)
	}
	if errors.Is(err, gtd.ErrProjectNotFound) {
		t.Errorf("err = %v, must not be errors.Is(err, gtd.ErrProjectNotFound)", err)
	}
}

// [F1011-12] TestStore_UpdateTask_ProjectID runs the tri-state project_id
// contract (gtd.ProjectRef) straight against the Postgres store. The same
// behaviours also run through TestGTDConformance_Postgres; this test is the
// named anchor that does not depend on the shared conformance rows. Each
// subtest gets a fresh workspace, so project names never collide.
func TestStore_UpdateTask_ProjectID(t *testing.T) {
	pool := openTestPgPool(t)

	cases := []struct {
		name string
		run  func(t *testing.T, store *gtd.Store)
	}{
		{"omit", projectIDCaseOmit},
		{"clear", projectIDCaseClear},
		{"set", projectIDCaseSet},
		{"unknown", projectIDCaseUnknown},
		{"unknown_task", projectIDCaseUnknownTask},
	}
	for _, tc := range cases {
		run := tc.run
		t.Run(tc.name, func(t *testing.T) {
			wsID := uuid.New()
			run(t, newPgGTDStore(pool, &wsID))
		})
	}
}
