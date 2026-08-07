package gtd_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// TestStore_WithTxCreateProject_PG_InvalidRepoName is the Postgres half of
// sprint 8-7 gap G, paired with
// internal/storage/sqlite/gtd_createprojecttx_repo_name_test.go's
// TestGTDStore_CreateProjectTx_InvalidRepoName. Unlike SQLite (which has a
// bespoke CreateProjectTx method), Postgres's confirm_proposal
// materialisation path (internal/mcp/tools_proposal.go's
// materializeFromPayloadPg) reuses the SAME validated CreateProject method
// via WithTx(tx) — see internal/gtd/store.go's WithTx doc comment — so this
// pins that the tx-scoped path was never actually missing validation on the
// PG side; the asymmetry was SQLite-only. Uses testcontainers Postgres per
// backend-security-design.md §6.5 (openTestPgPool — TestMain-managed
// singleton container, never mocked).
func TestStore_WithTxCreateProject_PG_InvalidRepoName(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = store.WithTx(tx).CreateProject(ctx, gtd.CreateProjectParams{
		Name: "pg-tx-bad-repo-" + wsID.String()[:8], Title: "t", Area: "a",
		RepoName: "bad repo name!",
	})
	if !errors.Is(err, gtd.ErrInvalidRepoName) {
		t.Errorf("expected gtd.ErrInvalidRepoName, got: %v", err)
	}
}

// TestStore_WithTxCreateProject_PG_ValidRepoName_Persists mirrors
// TestGTDStore_CreateProjectTx_ValidRepoName_Persists (SQLite side): a valid
// repo_name must still be accepted and readable after commit.
func TestStore_WithTxCreateProject_PG_ValidRepoName_Persists(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}

	name := "pg-tx-good-repo-" + wsID.String()[:8]
	created, err := store.WithTx(tx).CreateProject(ctx, gtd.CreateProjectParams{
		Name: name, Title: "t", Area: "a", RepoName: pgSeedRepoName,
	})
	if err != nil {
		t.Fatalf("CreateProject with valid repo_name: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	got, err := store.GetProjectByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetProjectByID: %v", err)
	}
	if !got.RepoName.Valid || got.RepoName.String != pgSeedRepoName {
		t.Errorf("repo_name = %+v, want pgSeedRepoName", got.RepoName)
	}
}
