package storage_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
)

// TestNewServerStores_SQLite_EndToEnd_TaskInsert proves the SQLite bundle
// returned by the factory is wired correctly enough to write through one of
// its stores and read the row back. Substitutes for the manual
// `STORAGE_BACKEND=sqlite ./bin/server` smoke test the task description
// asks for, since the harness sandbox blocks executing freshly-built
// binaries.
func TestNewServerStores_SQLite_EndToEnd_TaskInsert(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "e2e.db")
	stores, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{
		Backend:    storage.BackendSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("NewServerStores(sqlite): %v", err)
	}
	t.Cleanup(func() {
		if cerr := stores.Close(); cerr != nil {
			t.Errorf("close: %v", cerr)
		}
	})

	ctx := context.Background()

	// Create a project so we have a parent for the task; CreateProject
	// exercises gtd.Store + workspace_id NULL path (no WORKSPACE_ID env in
	// this test process).
	project, err := stores.GTD().CreateProject(ctx, gtd.CreateProjectParams{
		Name:        "e2e-smoke",
		Title:       "E2E Smoke Project",
		Area:        "engineering",
		Description: "verifies sqlite bundle insert path",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if project == nil || project.ID == [16]byte{} {
		t.Fatalf("CreateProject returned no ID: %+v", project)
	}

	task, err := stores.GTD().CreateTask(ctx, gtd.CreateTaskParams{
		Title:    "sqlite e2e smoke task",
		Priority: 4,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task == nil || task.ID == [16]byte{} {
		t.Fatalf("CreateTask returned no ID: %+v", task)
	}

	tasks, err := stores.GTD().Tasks(ctx, nil)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	found := false
	for _, row := range tasks {
		if row.ID == task.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("inserted task %s not returned by Tasks() (got %d rows)", task.ID, len(tasks))
	}
}
