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

// RunGTDSmoke runs the 6 shared GTD behaviours (project create/conflict,
// task Area COALESCE, delete-project cascade, cross-workspace delete no-op)
// against store, as t.Run subtests named backend+"/"+<op>. secondWorkspaceStore
// MUST be built from the same underlying DB as store (same pgxpool / same
// sqlite file) but a different workspace ID, so row 6's cross-workspace
// DeleteProject no-op is a real cross-tenant check, not a different database.
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
