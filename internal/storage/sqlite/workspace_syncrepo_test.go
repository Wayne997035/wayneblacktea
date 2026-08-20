package sqlite_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/jackc/pgx/v5/pgtype"
)

// seedSyncRepoOmissionFixture creates the full repo row
// TestSyncRepo_OmittedKnownIssuesPreserved seeds before exercising the
// omission-shaped update. Extracted (like the two helpers below) purely to
// keep the parent test's own cyclomatic complexity under the gocyclo
// threshold — nesting the same checks as t.Run closures does NOT achieve
// this: gocyclo walks into FuncLit bodies as part of the enclosing
// FuncDecl's complexity, so only genuinely separate top-level functions
// reduce the count.
func seedSyncRepoOmissionFixture(t *testing.T, s *sqlite.WorkspaceStore, ctx context.Context) db.Repo {
	t.Helper()
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
	return *seeded
}

// preservedTextField names one pgtype.Text column plus the value it must
// hold after the omission-shaped sync_repo-style update in
// TestSyncRepo_OmittedKnownIssuesPreserved.
type preservedTextField struct {
	name string
	got  pgtype.Text
	want string
}

// assertPreservedTextFields checks every pgtype.Text column the omission
// update must have left correct — current_branch (explicitly set on this
// call) plus path/description/language/next_planned_step (all omitted,
// must survive unchanged). Table-driven so the branch itself appears once in
// the source, not once per field.
func assertPreservedTextFields(t *testing.T, updated db.Repo) {
	t.Helper()
	fields := []preservedTextField{
		{"current_branch", updated.CurrentBranch, "feature/y"},
		{"path", updated.Path, "/repos/wbt"},
		{"description", updated.Description, "original description"},
		{"language", updated.Language, "go"},
		{"next_planned_step", updated.NextPlannedStep, "original next step"},
	}
	for _, f := range fields {
		if !f.got.Valid || f.got.String != f.want {
			t.Errorf("%s = %+v, want %q", f.name, f.got, f.want)
		}
	}
}

// assertKnownIssuesPreserved checks the bad case's core assertion — known_issues
// must survive an omission-shaped update, not get wiped to []. label
// distinguishes the two call sites (the update's own response vs. an
// independent reload) in failure output.
func assertKnownIssuesPreserved(t *testing.T, label string, got []string) {
	t.Helper()
	if len(got) != 1 || got[0] != "x" {
		t.Errorf("%s: known_issues = %v, want preserved [\"x\"] (not wiped to [])", label, got)
	}
}

// TestSyncRepo_OmittedKnownIssuesPreserved is U7's SQLite bad-case red test
// for Ω6 (2026-08-20-mcp-surface-spec.md): before this fix, UpsertRepo's SQL
// unconditionally overwrote known_issues (`known_issues = excluded.known_issues`,
// with sync_repo never sending that field) — every sync_repo call silently
// wiped known_issues to []. Postgres already COALESCE-preserved known_issues
// (by accident: sync_repo never sets it, so EXCLUDED.known_issues was always
// NULL there); SQLite diverged. Both backends are now presence-aware
// (nil KnownIssues -> preserve). Same scenario also covers path/description/
// language/next_planned_step: seeded, then a sync_repo-shaped call supplying
// only current_branch must leave every other field untouched too — including
// the independent reload round-trip (defence-in-depth in case UpsertRepo's
// own returned row ever drifted from what's actually persisted).
func TestSyncRepo_OmittedKnownIssuesPreserved(t *testing.T) {
	s := openWorkspaceStore(t, ":memory:", "")
	ctx := context.Background()

	seeded := seedSyncRepoOmissionFixture(t, s, ctx)

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

	assertPreservedTextFields(t, *updated)
	assertKnownIssuesPreserved(t, "post-update response", updated.KnownIssues)

	reloaded, err := s.RepoByName(ctx, "wbt")
	if err != nil {
		t.Fatalf("RepoByName: %v", err)
	}
	assertKnownIssuesPreserved(t, "independent reload", reloaded.KnownIssues)
}
