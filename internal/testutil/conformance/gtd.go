// Package conformance holds one shared behaviour-assertion suite per domain
// (gtd, decision, knowledge) that both the Postgres and SQLite backends run
// through — a smoke over EXISTING shared functionality only (decision
// f0fa67c2), not a new abstraction layer or an attempt at full conformance
// coverage.
//
// This package intentionally does not import internal/storage/sqlite: it
// only needs each domain's StoreIface plus its param/error types, so the
// dependency direction stays one-way (domain packages' external test files
// -> conformance -> domain packages), with zero import-cycle risk.
//
// Run*Smoke functions take *testing.T (not the testing.TB interface) because
// they use t.Run for named subtests — T.Run and B.Run have incompatible
// callback signatures, so testing.TB deliberately does not declare Run.
package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// RunGTDSmoke runs the 12 shared GTD behaviours (project create/conflict,
// task Area COALESCE, delete-project cascade, cross-workspace delete no-op, and
// the UpdateTask project_id contract: attach, omit-preserved, clear, unknown
// project, cross-workspace project, unknown task) against store, as t.Run
// subtests named backend+"/"+<op>. secondWorkspaceStore MUST be built from the
// same underlying DB as store (same pgxpool / same sqlite file) but a different
// workspace ID, so row 6's cross-workspace DeleteProject no-op and row 11's
// cross-workspace project are real cross-tenant checks, not a different database.
//
// [F1011-16] Rows 7-12 (UpdateTask_ProjectID_*) each build their OWN project and
// task fixtures: the shared fixture task above is asserted by row 5
// (DeleteProject_CascadesAndNotFound, n == 1), so moving it would break that row.
func RunGTDSmoke(t *testing.T, store, secondWorkspaceStore gtd.StoreIface, backend string) {
	ctx := context.Background()

	t.Run(backend+"/CreateProject_HappyPath", func(t *testing.T) { gtdSmokeCreateProjectHappyPath(t, ctx, store) })
	t.Run(backend+"/CreateProject_NameCollision", func(t *testing.T) { gtdSmokeCreateProjectNameCollision(t, ctx, store) })

	// Rows 3-5 share one project+task fixture: CreateTask with Area "wbt",
	// UpdateTask(Area:nil) must preserve it, UpdateTask(Area:ptr("misc"))
	// must apply it, then DeleteProject must cascade-remove the task and
	// the project itself.
	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "smoke-proj-fixture", Title: "fixture"})
	if err != nil {
		t.Fatalf("CreateProject (fixture): %v", err)
	}
	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &proj.ID, Title: "smoke-task", Area: "wbt"})
	if err != nil {
		t.Fatalf("CreateTask (fixture): %v", err)
	}

	t.Run(backend+"/UpdateTask_AreaNil_Preserved", func(t *testing.T) { gtdSmokeUpdateTaskAreaNilPreserved(t, ctx, store, task.ID) })
	t.Run(backend+"/UpdateTask_AreaSet_Applied", func(t *testing.T) { gtdSmokeUpdateTaskAreaSetApplied(t, ctx, store, task.ID) })
	t.Run(backend+"/DeleteProject_CascadesAndNotFound", func(t *testing.T) { gtdSmokeDeleteProjectCascades(t, ctx, store, proj.ID) })
	t.Run(backend+"/DeleteProject_CrossWorkspace_NoOp", func(t *testing.T) {
		gtdSmokeDeleteProjectCrossWorkspaceNoOp(t, ctx, store, secondWorkspaceStore)
	})

	// Rows 7-12 [F1011-16]: UpdateTask's project_id contract (see gtd.ProjectRef).
	t.Run(backend+"/UpdateTask_ProjectID_Attach", func(t *testing.T) { gtdSmokeProjectIDAttach(t, ctx, store) })
	t.Run(backend+"/UpdateTask_ProjectID_OmitPreserved", func(t *testing.T) { gtdSmokeProjectIDOmitPreserved(t, ctx, store) })
	t.Run(backend+"/UpdateTask_ProjectID_Clear", func(t *testing.T) { gtdSmokeProjectIDClear(t, ctx, store) })
	t.Run(backend+"/UpdateTask_ProjectID_Unknown", func(t *testing.T) { gtdSmokeProjectIDUnknown(t, ctx, store) })
	t.Run(backend+"/UpdateTask_ProjectID_CrossWorkspace", func(t *testing.T) {
		gtdSmokeProjectIDCrossWorkspace(t, ctx, store, secondWorkspaceStore)
	})
	t.Run(backend+"/UpdateTask_ProjectID_UnknownTask", func(t *testing.T) { gtdSmokeProjectIDUnknownTask(t, ctx, store) })
}

func gtdSmokeCreateProjectHappyPath(t *testing.T, ctx context.Context, store gtd.StoreIface) {
	got, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "smoke-proj", Title: "t"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if got.Name != "smoke-proj" {
		t.Errorf("Name = %q, want %q", got.Name, "smoke-proj")
	}
	if got.Status != string(gtd.ProjectStatusActive) {
		t.Errorf("Status = %q, want %q", got.Status, gtd.ProjectStatusActive)
	}
}

func gtdSmokeCreateProjectNameCollision(t *testing.T, ctx context.Context, store gtd.StoreIface) {
	if _, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "smoke-proj", Title: "t2"}); !errors.Is(err, gtd.ErrConflict) {
		t.Fatalf("CreateProject collision err = %v, want errors.Is(err, gtd.ErrConflict)", err)
	}
}

func gtdSmokeUpdateTaskAreaNilPreserved(t *testing.T, ctx context.Context, store gtd.StoreIface, taskID uuid.UUID) {
	if _, err := store.UpdateTask(ctx, taskID, gtd.UpdateTaskParams{Area: nil}); err != nil {
		t.Fatalf("UpdateTask(Area:nil): %v", err)
	}
	got, err := store.GetTaskByID(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Area != "wbt" {
		t.Errorf("Area = %q, want unchanged %q (nil Area must COALESCE, not reset)", got.Area, "wbt")
	}
}

func gtdSmokeUpdateTaskAreaSetApplied(t *testing.T, ctx context.Context, store gtd.StoreIface, taskID uuid.UUID) {
	misc := "misc"
	if _, err := store.UpdateTask(ctx, taskID, gtd.UpdateTaskParams{Area: &misc}); err != nil {
		t.Fatalf("UpdateTask(Area:misc): %v", err)
	}
	got, err := store.GetTaskByID(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Area != "misc" {
		t.Errorf("Area = %q, want %q", got.Area, "misc")
	}
}

func gtdSmokeDeleteProjectCascades(t *testing.T, ctx context.Context, store gtd.StoreIface, projID uuid.UUID) {
	n, err := store.DeleteProject(ctx, projID, "smoke-test")
	if err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if n != 1 {
		t.Errorf("DeleteProject task count = %d, want 1", n)
	}
	if _, err := store.GetProjectByID(ctx, projID); !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("GetProjectByID after delete: err = %v, want errors.Is(err, gtd.ErrNotFound)", err)
	}
}

func gtdSmokeDeleteProjectCrossWorkspaceNoOp(t *testing.T, ctx context.Context, store, secondWorkspaceStore gtd.StoreIface) {
	other, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "smoke-proj-cross-ws", Title: "cross-ws"})
	if err != nil {
		t.Fatalf("CreateProject (cross-ws fixture): %v", err)
	}
	n, err := secondWorkspaceStore.DeleteProject(ctx, other.ID, "smoke-test")
	if err != nil {
		t.Fatalf("cross-workspace DeleteProject: %v", err)
	}
	if n != 0 {
		t.Fatalf("cross-workspace DeleteProject task count = %d, want 0 (must be a no-op)", n)
	}
	if _, err := store.GetProjectByID(ctx, other.ID); err != nil {
		t.Errorf("GetProjectByID (own workspace) after cross-workspace delete attempt: %v, want the project to still exist", err)
	}
}

// gtdSmokeProject creates a project named name in s and returns its ID. Every
// UpdateTask_ProjectID_* row passes a name of its own: a repeat inside one
// workspace is an ErrConflict, and the rows above already hold "smoke-proj",
// "smoke-proj-fixture" and "smoke-proj-cross-ws".
func gtdSmokeProject(t *testing.T, ctx context.Context, s gtd.StoreIface, name string) uuid.UUID {
	t.Helper()
	p, err := s.CreateProject(ctx, gtd.CreateProjectParams{Name: name, Title: name})
	if err != nil {
		t.Fatalf("CreateProject (%s fixture): %v", name, err)
	}
	return p.ID
}

// gtdSmokeTask creates a task titled title in s, attached to projectID when it
// is non-nil, and returns its ID.
func gtdSmokeTask(t *testing.T, ctx context.Context, s gtd.StoreIface, title string, projectID *uuid.UUID) uuid.UUID {
	t.Helper()
	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: projectID, Title: title})
	if err != nil {
		t.Fatalf("CreateTask (%s fixture): %v", title, err)
	}
	return task.ID
}

// gtdSmokeWantProject asserts the project_id stored on the task row itself
// (nil want = NULL), read back through s.GetTaskByID rather than from the
// UpdateTask return value, so a store that returns a stale struct still fails.
func gtdSmokeWantProject(t *testing.T, ctx context.Context, s gtd.StoreIface, taskID uuid.UUID, want *uuid.UUID, when string) {
	t.Helper()
	got, err := s.GetTaskByID(ctx, taskID)
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

// gtdSmokeWantListed asserts whether s.Tasks(&projectID) contains taskID.
func gtdSmokeWantListed(t *testing.T, ctx context.Context, s gtd.StoreIface, projectID, taskID uuid.UUID, want bool, when string) {
	t.Helper()
	tasks, err := s.Tasks(ctx, &projectID)
	if err != nil {
		t.Fatalf("Tasks(%s) (%s): %v", projectID, when, err)
	}
	listed := false
	for i := range tasks {
		if tasks[i].ID == taskID {
			listed = true
		}
	}
	if listed != want {
		t.Errorf("%s: Tasks(project %s) lists task = %v, want %v", when, projectID, listed, want)
	}
}

// gtdSmokeWantProjectNotFound asserts err is gtd.ErrProjectNotFound and is NOT
// gtd.ErrNotFound: the MCP handler already maps ErrNotFound to "task not
// found", so a store that returned it here would blame the wrong entity.
func gtdSmokeWantProjectNotFound(t *testing.T, err error, when string) {
	t.Helper()
	if !errors.Is(err, gtd.ErrProjectNotFound) {
		t.Errorf("%s: err = %v, want errors.Is(err, gtd.ErrProjectNotFound)", when, err)
	}
	if errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("%s: err = %v, must not be errors.Is(err, gtd.ErrNotFound) (the MCP layer would say task not found)", when, err)
	}
}

// [F1011-16] Row 7: Project{ID: &p} on a task with no project attaches it.
func gtdSmokeProjectIDAttach(t *testing.T, ctx context.Context, store gtd.StoreIface) {
	proj := gtdSmokeProject(t, ctx, store, "smoke-projid-attach")
	task := gtdSmokeTask(t, ctx, store, "smoke-projid-attach-task", nil)

	if _, err := store.UpdateTask(ctx, task, gtd.UpdateTaskParams{Project: &gtd.ProjectRef{ID: &proj}}); err != nil {
		t.Fatalf("UpdateTask(Project:set): %v", err)
	}
	gtdSmokeWantProject(t, ctx, store, task, &proj, "after attach")
	gtdSmokeWantListed(t, ctx, store, proj, task, true, "after attach")
}

// [F1011-16] Row 8: an update that does not mention Project leaves project_id
// alone. The title is changed so the UPDATE demonstrably ran.
func gtdSmokeProjectIDOmitPreserved(t *testing.T, ctx context.Context, store gtd.StoreIface) {
	proj := gtdSmokeProject(t, ctx, store, "smoke-projid-omit")
	task := gtdSmokeTask(t, ctx, store, "smoke-projid-omit-task", &proj)

	renamed := "smoke-projid-omit-renamed"
	if _, err := store.UpdateTask(ctx, task, gtd.UpdateTaskParams{Title: &renamed}); err != nil {
		t.Fatalf("UpdateTask(Title only): %v", err)
	}
	if got, err := store.GetTaskByID(ctx, task); err != nil {
		t.Fatalf("GetTaskByID after title-only update: %v", err)
	} else if got.Title != renamed {
		t.Errorf("Title = %q, want %q (the update must have run)", got.Title, renamed)
	}
	gtdSmokeWantProject(t, ctx, store, task, &proj, "after title-only update")
	gtdSmokeWantListed(t, ctx, store, proj, task, true, "after title-only update")
}

// [F1011-16] Row 9: Project{} (non-nil, nil ID) clears project_id to NULL.
func gtdSmokeProjectIDClear(t *testing.T, ctx context.Context, store gtd.StoreIface) {
	proj := gtdSmokeProject(t, ctx, store, "smoke-projid-clear")
	task := gtdSmokeTask(t, ctx, store, "smoke-projid-clear-task", &proj)

	if _, err := store.UpdateTask(ctx, task, gtd.UpdateTaskParams{Project: &gtd.ProjectRef{}}); err != nil {
		t.Fatalf("UpdateTask(Project:clear): %v", err)
	}
	gtdSmokeWantProject(t, ctx, store, task, nil, "after clear")
	gtdSmokeWantListed(t, ctx, store, proj, task, false, "after clear")
}

// [F1011-16] Row 10: a project that does not exist is rejected with
// ErrProjectNotFound and nothing is written, the other fields of the same call
// included.
//
// The nil UUID runs through the same checks: it is a set to an id no project
// has, not a clear and not an omit (gtd.ProjectRef). This repo uses uuid.Nil
// as "absent" elsewhere, so a store that took that shortcut here would quietly
// clear or keep the link instead of refusing. [PR #200 R1-03]
func gtdSmokeProjectIDUnknown(t *testing.T, ctx context.Context, store gtd.StoreIface) {
	for _, c := range []struct {
		label   string
		missing uuid.UUID
	}{
		{"random", uuid.New()},
		{"nil", uuid.Nil},
	} {
		original := "smoke-projid-unknown-task-" + c.label
		proj := gtdSmokeProject(t, ctx, store, "smoke-projid-unknown-"+c.label)
		task := gtdSmokeTask(t, ctx, store, original, &proj)

		missing := c.missing
		rejected := "should not land"
		_, err := store.UpdateTask(ctx, task, gtd.UpdateTaskParams{Title: &rejected, Project: &gtd.ProjectRef{ID: &missing}})
		gtdSmokeWantProjectNotFound(t, err, "UpdateTask("+c.label+" unknown project)")

		if got, gerr := store.GetTaskByID(ctx, task); gerr != nil {
			t.Fatalf("GetTaskByID after rejected update (%s): %v", c.label, gerr)
		} else if got.Title != original {
			t.Errorf("%s: Title = %q, want unchanged %q (a rejected call must write nothing)", c.label, got.Title, original)
		}
		gtdSmokeWantProject(t, ctx, store, task, &proj, "after rejected update ("+c.label+")")
	}
}

// [F1011-16] Row 11: a project that lives in another workspace is as good as
// missing. The project is created through store (workspace A), the task through
// secondWorkspaceStore (workspace B), and B's store tries to point the task at A's
// project.
func gtdSmokeProjectIDCrossWorkspace(t *testing.T, ctx context.Context, store, secondWorkspaceStore gtd.StoreIface) {
	foreign := gtdSmokeProject(t, ctx, store, "smoke-projid-xws-proj")
	task := gtdSmokeTask(t, ctx, secondWorkspaceStore, "smoke-projid-xws-task", nil)

	_, err := secondWorkspaceStore.UpdateTask(ctx, task, gtd.UpdateTaskParams{Project: &gtd.ProjectRef{ID: &foreign}})
	gtdSmokeWantProjectNotFound(t, err, "UpdateTask(project of another workspace)")
	gtdSmokeWantProject(t, ctx, secondWorkspaceStore, task, nil, "after cross-workspace attempt")
}

// [F1011-16] Row 12: an unknown task is ErrNotFound before the project is looked
// at. The first case pairs the unknown task with an unknown project, which is
// what tells the two orders apart: a store that checked the project first
// would answer ErrProjectNotFound there. The second case pairs it with a real
// project, where no order can produce ErrProjectNotFound.
func gtdSmokeProjectIDUnknownTask(t *testing.T, ctx context.Context, store gtd.StoreIface) {
	existing := gtdSmokeProject(t, ctx, store, "smoke-projid-unknowntask")
	cases := []struct {
		name    string
		project uuid.UUID
	}{
		{"unknown task + unknown project", uuid.New()},
		{"unknown task + valid project", existing},
	}
	for _, tc := range cases {
		project := tc.project
		_, err := store.UpdateTask(ctx, uuid.New(), gtd.UpdateTaskParams{Project: &gtd.ProjectRef{ID: &project}})
		if !errors.Is(err, gtd.ErrNotFound) {
			t.Errorf("%s: err = %v, want errors.Is(err, gtd.ErrNotFound)", tc.name, err)
		}
		if errors.Is(err, gtd.ErrProjectNotFound) {
			t.Errorf("%s: err = %v, must not be errors.Is(err, gtd.ErrProjectNotFound) (the task is checked first)", tc.name, err)
		}
	}
}
