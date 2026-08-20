package sqlite_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/workspace"
)

// TestSyncRepo_OmittedKnownIssuesPreserved is U7's SQLite bad-case red test
// for Ω6 (2026-08-20-mcp-surface-spec.md): before this fix, UpsertRepo's SQL
// unconditionally overwrote known_issues (`known_issues = excluded.known_issues`,
// with sync_repo never sending that field) — every sync_repo call silently
// wiped known_issues to []. Postgres already COALESCE-preserved known_issues
// (by accident: sync_repo never sets it, so EXCLUDED.known_issues was always
// NULL there); SQLite diverged. Both backends are now presence-aware
// (nil KnownIssues -> preserve). Same scenario also covers path/description/
// language/next_planned_step: seeded, then a sync_repo-shaped call supplying
// only current_branch must leave every other field untouched too.
func TestSyncRepo_OmittedKnownIssuesPreserved(t *testing.T) {
	s := openWorkspaceStore(t, ":memory:", "")
	ctx := context.Background()

	// seed: full repo row, including known_issues=["x"].
	seeded, err := s.UpsertRepo(ctx, workspace.UpsertRepoParams{
		Name:            "wbt",
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

	// bad case: sync_repo(name, current_branch="feature/y") only — every
	// other field, including known_issues, omitted.
	updated, err := s.UpsertRepo(ctx, workspace.UpsertRepoParams{
		Name:          "wbt",
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
	reloaded, err := s.RepoByName(ctx, "wbt")
	if err != nil {
		t.Fatalf("RepoByName: %v", err)
	}
	if len(reloaded.KnownIssues) != 1 || reloaded.KnownIssues[0] != "x" {
		t.Errorf("reloaded known_issues = %v, want preserved [\"x\"]", reloaded.KnownIssues)
	}
}
