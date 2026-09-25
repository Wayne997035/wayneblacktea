package sqlite_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
)

// TestImport_RepoNameStoredEmptyWhenInvalid pins [F0925-29] for the qa-seed
// importers: they are automatic writers, so a production row whose repo_name
// breaks the workspace repo name rule is still imported, with repo_name
// stored empty. A compliant repo_name is kept.
func TestImport_RepoNameStoredEmptyWhenInvalid(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixed := pgTimeVal(time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC))

	for _, tc := range []struct {
		repo      string
		wantValid bool
	}{{"../x", false}, {"workspace/.hidden", false}, {"Flare-Go/auth", true}} {
		d, gs := openGTDDB(t)
		pid := uuid.New()
		if err := gs.ImportProject(ctx, db.Project{
			ID: pid, Name: "p-" + pid.String()[:8], Title: "P", Status: "active", Area: "projects",
			Priority: 3, RepoName: pgTextVal(tc.repo), CreatedAt: fixed, UpdatedAt: fixed,
		}); err != nil {
			t.Fatalf("ImportProject(%q): %v", tc.repo, err)
		}
		var repo sql.NullString
		if err := d.QueryRowContext(ctx, `SELECT repo_name FROM projects WHERE id = ?1`, pid.String()).Scan(&repo); err != nil {
			t.Fatalf("scan project: %v", err)
		}
		if repo.Valid != tc.wantValid || (tc.wantValid && repo.String != tc.repo) {
			t.Errorf("ImportProject(%q): stored repo_name %+v", tc.repo, repo)
		}

		dd, ds := openDecisionDB(t, ":memory:", "")
		did := uuid.New()
		if err := ds.ImportDecision(ctx, db.Decision{
			ID: did, Title: "t", Context: "c", Decision: "d", Rationale: "r",
			RepoName: pgTextVal(tc.repo), CreatedAt: fixed, Source: "manual",
		}); err != nil {
			t.Fatalf("ImportDecision(%q): %v", tc.repo, err)
		}
		if err := dd.QueryRowContext(ctx, `SELECT repo_name FROM decisions WHERE id = ?1`, did.String()).Scan(&repo); err != nil {
			t.Fatalf("scan decision: %v", err)
		}
		if repo.Valid != tc.wantValid || (tc.wantValid && repo.String != tc.repo) {
			t.Errorf("ImportDecision(%q): stored repo_name %+v", tc.repo, repo)
		}
	}
}
