package gtd_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// seedPGProject creates a project on the PG store, optionally transitioning
// it to a non-active status via UpdateProjectStatus. status "" or "active"
// leaves the project at its default active state.
func seedPGProject(t *testing.T, store *gtd.Store, status string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name:  "pg-proj-" + uuid.NewString()[:8],
		Title: "Filter Test Project",
		Area:  "eng",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if status != "" && status != "active" {
		if _, err := store.UpdateProjectStatus(ctx, proj.ID, gtd.ProjectStatus(status)); err != nil {
			t.Fatalf("UpdateProjectStatus %q: %v", status, err)
		}
	}
	return proj.ID
}

// TestPGStore_ProjectsFiltered_ActiveDefault verifies that empty status ("")
// and "active" both return only active projects, matching
// ListActiveProjects's contract byte-for-byte.
func TestPGStore_ProjectsFiltered_ActiveDefault(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	activeID := seedPGProject(t, store, "active")
	_ = seedPGProject(t, store, "completed")
	_ = seedPGProject(t, store, "archived")

	for _, status := range []string{"", "active"} {
		t.Run("status="+status, func(t *testing.T) {
			projects, err := store.ProjectsFiltered(ctx, status)
			if err != nil {
				t.Fatalf("ProjectsFiltered(%q): %v", status, err)
			}
			found := false
			for _, p := range projects {
				if p.Status != "active" {
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

// TestPGStore_ProjectsFiltered_ActiveDefault_ByteIdenticalToListActiveProjects
// is the direct regression lock for the "unset status MUST be byte-identical
// to the pre-existing default" acceptance criterion: same seed data, compare
// the two methods' results by ID set and ordering.
func TestPGStore_ProjectsFiltered_ActiveDefault_ByteIdenticalToListActiveProjects(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_ = seedPGProject(t, store, "active")
	}
	_ = seedPGProject(t, store, "completed") // must not appear in either result

	viaFiltered, err := store.ProjectsFiltered(ctx, "")
	if err != nil {
		t.Fatalf("ProjectsFiltered(\"\"): %v", err)
	}
	viaLegacy, err := store.ListActiveProjects(ctx)
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

// TestPGStore_ProjectsFiltered_StatusCompleted returns only completed projects.
func TestPGStore_ProjectsFiltered_StatusCompleted(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	completedID := seedPGProject(t, store, "completed")
	_ = seedPGProject(t, store, "active") // must not appear

	projects, err := store.ProjectsFiltered(ctx, "completed")
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

// TestPGStore_ProjectsFiltered_StatusArchived returns only archived projects
// — the status the production DB actually has a row in (measured directly),
// not just the active/completed pair the original bug ticket mentioned.
func TestPGStore_ProjectsFiltered_StatusArchived(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	archivedID := seedPGProject(t, store, "archived")
	_ = seedPGProject(t, store, "active")

	projects, err := store.ProjectsFiltered(ctx, "archived")
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

// TestPGStore_ProjectsFiltered_StatusOnHold returns only on_hold projects.
func TestPGStore_ProjectsFiltered_StatusOnHold(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	onHoldID := seedPGProject(t, store, "on_hold")
	_ = seedPGProject(t, store, "active")

	projects, err := store.ProjectsFiltered(ctx, "on_hold")
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

// TestPGStore_ProjectsFiltered_StatusAll returns projects of every status.
func TestPGStore_ProjectsFiltered_StatusAll(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	activeID := seedPGProject(t, store, "active")
	completedID := seedPGProject(t, store, "completed")
	archivedID := seedPGProject(t, store, "archived")
	onHoldID := seedPGProject(t, store, "on_hold")

	projects, err := store.ProjectsFiltered(ctx, "all")
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

// TestPGStore_ProjectsFiltered_WorkspaceScoping verifies that projects
// belonging to a different workspace are never returned.
func TestPGStore_ProjectsFiltered_WorkspaceScoping(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()

	wsA := uuid.New()
	wsB := uuid.New()
	storeA := newPgGTDStore(pool, &wsA)
	storeB := newPgGTDStore(pool, &wsB)

	wsBProjID := seedPGProject(t, storeB, "completed")

	projects, err := storeA.ProjectsFiltered(ctx, "all")
	if err != nil {
		t.Fatalf("ProjectsFiltered (wsA, all): %v", err)
	}
	for _, p := range projects {
		if p.ID == wsBProjID {
			t.Errorf("workspace-A store returned workspace-B project %s (workspace leak)", wsBProjID)
		}
	}
}

// TestPGStore_ProjectsFiltered_EmptyDB_NoError verifies that querying an
// empty database returns an empty result (not nil error, not panic).
func TestPGStore_ProjectsFiltered_EmptyDB_NoError(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	projects, err := store.ProjectsFiltered(ctx, "all")
	if err != nil {
		t.Fatalf("ProjectsFiltered on empty (scoped) DB: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("empty workspace should return 0 projects, got %d", len(projects))
	}
}
