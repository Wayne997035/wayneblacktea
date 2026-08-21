//go:build integration

package workspace_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
)

// TestSyncRepo_OmittedKnownIssuesPreserved is U7's PG bad-case red test for
// Ω6 (2026-08-20-mcp-surface-spec.md), mirroring the SQLite test in
// internal/storage/sqlite. Postgres already COALESCE-preserved known_issues
// by accident (sync_repo never sends that field, so EXCLUDED.known_issues
// was always NULL), but path/description/language/current_branch/
// next_planned_step were unconditionally overwritten
// (`path = EXCLUDED.path`, etc., no CASE guard) — a sync_repo-shaped call
// supplying only current_branch silently wiped every other field. All 5 are
// now presence-aware (CASE WHEN $N IS NULL THEN repos.<col> ELSE
// EXCLUDED.<col> END, sql/queries/workspace.sql's UpsertRepo).
func TestSyncRepo_OmittedKnownIssuesPreserved(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := workspace.NewStore(pool, &wsID)

	repoName := "wbt-omission-preserve-pg"

	// seed: full repo row, including known_issues=["x"].
	seeded, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{
		Name:            repoName,
		Path:            strPtr("/repos/wbt"),
		Description:     strPtr("original description"),
		Language:        strPtr("go"),
		CurrentBranch:   strPtr("main"),
		KnownIssues:     []string{"x"},
		NextPlannedStep: strPtr("original next step"),
	})
	if err != nil {
		t.Fatalf("seed UpsertRepo: %v", err)
	}
	t.Cleanup(func() {
		if _, delErr := pool.Exec(ctx, `DELETE FROM repos WHERE id = $1`, seeded.ID); delErr != nil {
			t.Logf("cleanup repo: %v", delErr)
		}
	})

	// bad case: sync_repo(name, current_branch="feature/y") only — every
	// other field, including known_issues, omitted.
	updated, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{
		Name:          repoName,
		CurrentBranch: strPtr("feature/y"),
	})
	if err != nil {
		t.Fatalf("sync_repo-shaped UpsertRepo: %v", err)
	}
	if updated.ID != seeded.ID {
		t.Fatalf("expected same repo row (upsert, not new insert): seeded=%s updated=%s", seeded.ID, updated.ID)
	}
	if len(updated.KnownIssues) != 1 || updated.KnownIssues[0] != "x" {
		t.Errorf("known_issues = %v, want preserved [\"x\"] (not wiped to [])", updated.KnownIssues)
	}
	if !updated.CurrentBranch.Valid || updated.CurrentBranch.String != "feature/y" {
		t.Errorf("current_branch = %+v, want updated to \"feature/y\"", updated.CurrentBranch)
	}
	if !updated.Path.Valid || updated.Path.String != "/repos/wbt" {
		t.Errorf("path = %+v, want preserved \"/repos/wbt\"", updated.Path)
	}
	if !updated.Description.Valid || updated.Description.String != "original description" {
		t.Errorf("description = %+v, want preserved \"original description\"", updated.Description)
	}
	if !updated.Language.Valid || updated.Language.String != "go" {
		t.Errorf("language = %+v, want preserved \"go\"", updated.Language)
	}
	if !updated.NextPlannedStep.Valid || updated.NextPlannedStep.String != "original next step" {
		t.Errorf("next_planned_step = %+v, want preserved \"original next step\"", updated.NextPlannedStep)
	}

	// reload independently — defence-in-depth in case UpsertRepo's own
	// returned row ever drifted from what's actually persisted.
	reloaded, err := store.RepoByName(ctx, repoName)
	if err != nil {
		t.Fatalf("RepoByName: %v", err)
	}
	if len(reloaded.KnownIssues) != 1 || reloaded.KnownIssues[0] != "x" {
		t.Errorf("reloaded known_issues = %v, want preserved [\"x\"]", reloaded.KnownIssues)
	}
}
