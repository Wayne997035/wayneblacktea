package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

// TestGTDStore_CreateProjectTx_InvalidRepoName is the SQLite half of sprint
// 8-7 gap G: a gap audit found that
// GTDStore.CreateProjectTx (the confirm_proposal materialisation path used by
// internal/mcp/tools_proposal.go's materializeFromPayloadSQLiteTx) never
// validated repo_name, even though its sibling non-Tx CreateProject
// (TestGTDStore_CreateProject_InvalidRepoName below) and the Postgres
// equivalent (internal/gtd/store_postgres_createprojecttx_repo_name_test.go's
// TestStore_WithTxCreateProject_PG_InvalidRepoName, reusing the SAME
// validated CreateProject method via WithTx) both already did. This is a
// SQLite-only backend asymmetry in the proposal-confirm flow, not a
// transport (MCP vs HTTP) asymmetry — see the G row's dispatch note.
func TestGTDStore_CreateProjectTx_InvalidRepoName(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	tx, err := s.DB().BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = s.CreateProjectTx(ctx, tx, gtd.CreateProjectParams{
		Name: "sqlite-tx-bad-repo", Title: "t", Area: "a",
		RepoName: "bad repo name!",
	})
	if !errors.Is(err, gtd.ErrInvalidRepoName) {
		t.Errorf("expected gtd.ErrInvalidRepoName, got: %v", err)
	}
}

// TestGTDStore_CreateProjectTx_ValidRepoName_Persists is the accept-path
// counterpart: a valid repo_name must still be persisted via CreateProjectTx
// (the fix must reject bad input without breaking good input).
func TestGTDStore_CreateProjectTx_ValidRepoName_Persists(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	tx, err := s.DB().BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}

	id, err := s.CreateProjectTx(ctx, tx, gtd.CreateProjectParams{
		Name: "sqlite-tx-good-repo", Title: "t", Area: "a",
		RepoName: sqliteSeedRepoName,
	})
	if err != nil {
		t.Fatalf("CreateProjectTx with valid repo_name: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	got, err := s.GetProjectByID(ctx, id)
	if err != nil {
		t.Fatalf("GetProjectByID: %v", err)
	}
	if !got.RepoName.Valid || got.RepoName.String != sqliteSeedRepoName {
		t.Errorf("repo_name = %+v, want sqliteSeedRepoName", got.RepoName)
	}
}
