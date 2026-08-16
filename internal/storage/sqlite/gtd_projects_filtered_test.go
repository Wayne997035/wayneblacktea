package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// seedSQLiteProject creates a project in the GTDStore, optionally
// transitioning it to a non-active status via UpdateProjectStatus. status ""
// or "active" leaves the project at its default active state.
func seedSQLiteProject(t *testing.T, s *sqlite.GTDStore, status string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	proj, err := s.CreateProject(ctx, gtd.CreateProjectParams{
		Name:  "sqlite-proj-" + uuid.NewString()[:8],
		Title: "Filter Test Project",
		Area:  "eng",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if status != "" && status != statusActive {
		if _, err := s.UpdateProjectStatus(ctx, proj.ID, gtd.ProjectStatus(status)); err != nil {
			t.Fatalf("UpdateProjectStatus %q: %v", status, err)
		}
	}
	return proj.ID
}

// TestSQLiteStore_ProjectsFiltered_ActiveDefault verifies that empty status
// ("") and "active" both return only active projects.
func TestSQLiteStore_ProjectsFiltered_ActiveDefault(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	activeID := seedSQLiteProject(t, s, statusActive)
	_ = seedSQLiteProject(t, s, "completed")
	_ = seedSQLiteProject(t, s, "archived")

	for _, status := range []string{"", statusActive} {
		t.Run("status="+status, func(t *testing.T) {
			projects, err := s.ProjectsFiltered(ctx, status)
			if err != nil {
				t.Fatalf("ProjectsFiltered(%q): %v", status, err)
			}
			found := false
			for _, p := range projects {
				if p.Status != statusActive {
					t.Errorf("active filter returned project with status %q", p.Status)
				}
				if p.ID == activeID {
					found = true
				}
			}
			if !found {
				t.Errorf("active project %s not returned", activeID)
			}
		})
	}
}

// TestSQLiteStore_ProjectsFiltered_ActiveDefault_ByteIdenticalToListActiveProjects
// is the SQLite-side regression lock mirroring the PG-side test of the same
// name: unset status MUST return the exact same rows, in the exact same
// order, as ListActiveProjects.
func TestSQLiteStore_ProjectsFiltered_ActiveDefault_ByteIdenticalToListActiveProjects(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_ = seedSQLiteProject(t, s, statusActive)
	}
	_ = seedSQLiteProject(t, s, "completed") // must not appear in either result

	viaFiltered, err := s.ProjectsFiltered(ctx, "")
	if err != nil {
		t.Fatalf("ProjectsFiltered(\"\"): %v", err)
	}
	viaLegacy, err := s.ListActiveProjects(ctx)
	if err != nil {
		t.Fatalf("ListActiveProjects: %v", err)
	}
	if len(viaFiltered) != len(viaLegacy) {
		t.Fatalf("row count mismatch: ProjectsFiltered=%d, ListActiveProjects=%d", len(viaFiltered), len(viaLegacy))
	}
	for i := range viaFiltered {
		if viaFiltered[i].ID != viaLegacy[i].ID {
			t.Errorf("order/identity mismatch at index %d: ProjectsFiltered=%s, ListActiveProjects=%s",
				i, viaFiltered[i].ID, viaLegacy[i].ID)
		}
	}
}

// TestSQLiteStore_ProjectsFiltered_StatusCompleted returns only completed projects.
func TestSQLiteStore_ProjectsFiltered_StatusCompleted(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	completedID := seedSQLiteProject(t, s, "completed")
	_ = seedSQLiteProject(t, s, statusActive) // must not appear

	projects, err := s.ProjectsFiltered(ctx, "completed")
	if err != nil {
		t.Fatalf("ProjectsFiltered(completed): %v", err)
	}
	found := false
	for _, p := range projects {
		if p.Status != "completed" {
			t.Errorf("status=completed returned project with status %q", p.Status)
		}
		if p.ID == completedID {
			found = true
		}
	}
	if !found {
		t.Errorf("completed project %s not returned", completedID)
	}
}

// TestSQLiteStore_ProjectsFiltered_StatusArchived returns only archived
// projects — the status the production DB actually has a row in.
func TestSQLiteStore_ProjectsFiltered_StatusArchived(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	archivedID := seedSQLiteProject(t, s, "archived")
	_ = seedSQLiteProject(t, s, statusActive)

	projects, err := s.ProjectsFiltered(ctx, "archived")
	if err != nil {
		t.Fatalf("ProjectsFiltered(archived): %v", err)
	}
	found := false
	for _, p := range projects {
		if p.Status != "archived" {
			t.Errorf("status=archived returned project with status %q", p.Status)
		}
		if p.ID == archivedID {
			found = true
		}
	}
	if !found {
		t.Errorf("archived project %s not returned", archivedID)
	}
}

// TestSQLiteStore_ProjectsFiltered_StatusOnHold returns only on_hold projects.
func TestSQLiteStore_ProjectsFiltered_StatusOnHold(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	onHoldID := seedSQLiteProject(t, s, "on_hold")
	_ = seedSQLiteProject(t, s, statusActive)

	projects, err := s.ProjectsFiltered(ctx, "on_hold")
	if err != nil {
		t.Fatalf("ProjectsFiltered(on_hold): %v", err)
	}
	found := false
	for _, p := range projects {
		if p.Status != "on_hold" {
			t.Errorf("status=on_hold returned project with status %q", p.Status)
		}
		if p.ID == onHoldID {
			found = true
		}
	}
	if !found {
		t.Errorf("on_hold project %s not returned", onHoldID)
	}
}

// TestSQLiteStore_ProjectsFiltered_StatusAll returns projects of every status.
func TestSQLiteStore_ProjectsFiltered_StatusAll(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	activeID := seedSQLiteProject(t, s, statusActive)
	completedID := seedSQLiteProject(t, s, "completed")
	archivedID := seedSQLiteProject(t, s, "archived")
	onHoldID := seedSQLiteProject(t, s, "on_hold")

	projects, err := s.ProjectsFiltered(ctx, "all")
	if err != nil {
		t.Fatalf("ProjectsFiltered(all): %v", err)
	}
	found := make(map[uuid.UUID]bool)
	for _, p := range projects {
		found[p.ID] = true
	}
	for _, id := range []uuid.UUID{activeID, completedID, archivedID, onHoldID} {
		if !found[id] {
			t.Errorf("status=all did not return project %s", id)
		}
	}
}

// TestSQLiteStore_ProjectsFiltered_WorkspaceScoping verifies that projects
// from a different workspace are not returned, using ONE shared on-disk
// SQLite file opened by two workspace-scoped stores — the same real
// regression guard pattern TestSQLiteStore_TasksFiltered_WorkspaceScoping
// uses (two separate :memory: DBs would pass vacuously even with the
// workspace_id predicate deleted).
func TestSQLiteStore_ProjectsFiltered_WorkspaceScoping(t *testing.T) {
	wsA := uuid.NewString()
	wsB := uuid.NewString()
	sharedPath := filepath.Join(t.TempDir(), "shared-projects.db")
	storeA := openFileStore(t, sharedPath, wsA)
	storeB := openFileStore(t, sharedPath, wsB)
	ctx := context.Background()

	wsBProjID := seedSQLiteProject(t, storeB, "completed")

	projects, err := storeA.ProjectsFiltered(ctx, "all")
	if err != nil {
		t.Fatalf("ProjectsFiltered (wsA, all): %v", err)
	}
	for _, p := range projects {
		if p.ID == wsBProjID {
			t.Errorf("workspace-A store returned workspace-B project %s", wsBProjID)
		}
	}

	// Own-workspace visibility regression guard: a project seeded via storeA
	// on the same shared physical file MUST still be returned — proves the
	// predicate filters by workspace, not by hiding everything.
	wsAProjID := seedSQLiteProject(t, storeA, "completed")
	projectsA, err := storeA.ProjectsFiltered(ctx, "all")
	if err != nil {
		t.Fatalf("ProjectsFiltered (wsA, own project): %v", err)
	}
	found := false
	for _, p := range projectsA {
		if p.ID == wsAProjID {
			found = true
		}
	}
	if !found {
		t.Errorf("workspace-A store did not return its own project %s", wsAProjID)
	}
}

// TestSQLiteStore_ProjectsFiltered_WorkspaceScoping_ActiveDefault verifies
// the ""/"active" branch's own separate workspace_id predicate also enforces
// isolation — a different SQL branch than the "all" test above, mirroring
// TestSQLiteStore_TasksFiltered_WorkspaceScoping_StatusAll's rationale for
// why each branch needs its own dedicated scoping test.
func TestSQLiteStore_ProjectsFiltered_WorkspaceScoping_ActiveDefault(t *testing.T) {
	wsA := uuid.NewString()
	wsB := uuid.NewString()
	sharedPath := filepath.Join(t.TempDir(), "shared-projects-active.db")
	storeA := openFileStore(t, sharedPath, wsA)
	storeB := openFileStore(t, sharedPath, wsB)
	ctx := context.Background()

	wsBProjID := seedSQLiteProject(t, storeB, statusActive)

	projects, err := storeA.ProjectsFiltered(ctx, "")
	if err != nil {
		t.Fatalf("ProjectsFiltered (wsA, default): %v", err)
	}
	for _, p := range projects {
		if p.ID == wsBProjID {
			t.Errorf("workspace-A store with default status returned workspace-B project %s", wsBProjID)
		}
	}
}

// TestSQLiteStore_ProjectsFiltered_EmptyDB_NoError verifies that querying an
// empty database returns an empty result (not nil error, not panic).
func TestSQLiteStore_ProjectsFiltered_EmptyDB_NoError(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	projects, err := s.ProjectsFiltered(ctx, "all")
	if err != nil {
		t.Fatalf("ProjectsFiltered on empty DB: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("empty DB should return 0 projects, got %d", len(projects))
	}
}
