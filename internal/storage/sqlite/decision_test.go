package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

func openDecisionDB(t *testing.T, dsn, workspaceID string) (*sqlite.DB, *sqlite.DecisionStore) {
	t.Helper()
	d, err := sqlite.Open(context.Background(), dsn, workspaceID)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, sqlite.NewDecisionStore(d)
}

func seedDecisionProject(t *testing.T, d *sqlite.DB, name string) uuid.UUID {
	t.Helper()
	p, err := sqlite.NewGTDStore(d).CreateProject(context.Background(), gtd.CreateProjectParams{
		Name:  name,
		Title: name,
	})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return p.ID
}

func TestDecisionStore_LogAndListRoundTrip(t *testing.T) {
	d, s := openDecisionDB(t, ":memory:", "")
	projectID := seedDecisionProject(t, d, "decision-round-trip")

	got, err := s.Log(context.Background(), decision.LogParams{
		ProjectID:    &projectID,
		RepoName:     "wayneblacktea",
		Title:        "Use SQLite stores",
		Context:      "single binary mode",
		Decision:     "implement domain stores",
		Rationale:    "remove Postgres requirement",
		Alternatives: "keep Postgres only",
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if got.Title != "Use SQLite stores" || !got.ProjectID.Valid || !got.RepoName.Valid || !got.Alternatives.Valid {
		t.Fatalf("unexpected decision row: %+v", got)
	}

	rows, err := s.All(context.Background(), 10)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != got.ID {
		t.Fatalf("expected logged decision, got %+v", rows)
	}
}

func TestDecisionStore_NullOptionalFields(t *testing.T) {
	_, s := openDecisionDB(t, ":memory:", "")
	row, err := s.Log(context.Background(), decision.LogParams{
		Title:     "No optional fields",
		Context:   "ctx",
		Decision:  "decide",
		Rationale: "because",
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if row.ProjectID.Valid || row.RepoName.Valid || row.Alternatives.Valid {
		t.Fatalf("expected NULL optionals, got %+v", row)
	}
}

func TestDecisionStore_EmptyQueriesReturnEmpty(t *testing.T) {
	_, s := openDecisionDB(t, ":memory:", "")
	ctx := context.Background()
	for name, fn := range map[string]func() (int, error){
		"All": func() (int, error) {
			rows, err := s.All(ctx, 5)
			return len(rows), err
		},
		"ByRepo": func() (int, error) {
			rows, err := s.ByRepo(ctx, "none", 5)
			return len(rows), err
		},
		"ByProject": func() (int, error) {
			rows, err := s.ByProject(ctx, uuid.New(), 5)
			return len(rows), err
		},
	} {
		got, err := fn()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != 0 {
			t.Fatalf("%s expected empty result, got %d rows", name, got)
		}
	}
}

func TestDecisionStore_ByRepoByProjectAndLimit(t *testing.T) {
	d, s := openDecisionDB(t, ":memory:", "")
	projectID := seedDecisionProject(t, d, "decision-filter")
	if _, err := s.Log(context.Background(), decision.LogParams{
		ProjectID: &projectID, RepoName: "repo-a", Title: "a1", Context: "c", Decision: "d", Rationale: "r",
	}); err != nil {
		t.Fatalf("Log a1: %v", err)
	}
	if _, err := s.Log(context.Background(), decision.LogParams{
		RepoName: "repo-b", Title: "b1", Context: "c", Decision: "d", Rationale: "r",
	}); err != nil {
		t.Fatalf("Log b1: %v", err)
	}

	byRepo, err := s.ByRepo(context.Background(), "repo-a", 1)
	if err != nil {
		t.Fatalf("ByRepo: %v", err)
	}
	if len(byRepo) != 1 || byRepo[0].Title != "a1" {
		t.Fatalf("unexpected ByRepo result: %+v", byRepo)
	}
	byProject, err := s.ByProject(context.Background(), projectID, 5)
	if err != nil {
		t.Fatalf("ByProject: %v", err)
	}
	if len(byProject) != 1 || byProject[0].Title != "a1" {
		t.Fatalf("unexpected ByProject result: %+v", byProject)
	}
}

func TestDecisionStore_WorkspaceIsolation(t *testing.T) {
	ctx := context.Background()
	wsA, wsB := uuid.New().String(), uuid.New().String()
	dsn := "file:decision-" + uuid.New().String() + "?mode=memory&cache=shared"
	_, storeA := openDecisionDB(t, dsn, wsA)
	_, storeB := openDecisionDB(t, dsn, wsB)

	if _, err := storeA.Log(ctx, decision.LogParams{
		RepoName: "repo", Title: "only-a", Context: "c", Decision: "d", Rationale: "r",
	}); err != nil {
		t.Fatalf("Log A: %v", err)
	}
	rowsB, err := storeB.All(ctx, 10)
	if err != nil {
		t.Fatalf("All B: %v", err)
	}
	if len(rowsB) != 0 {
		t.Fatalf("workspace B should not see A rows: %+v", rowsB)
	}
}

// (Removed) TestDecisionStore_FKViolationForMissingProject — the schema
// no longer enforces a foreign key from decisions.project_id to projects.id
// (red line #9; migration 000026). Referential integrity for project IDs
// is now expected to be applied in code at the service/handler layer when
// a use case requires it; the storage layer accepts orphan project_id by
// design (matching the Postgres backend behaviour after migration 000026).

func TestDecisionStore_ContextCanceled(t *testing.T) {
	_, s := openDecisionDB(t, ":memory:", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.All(ctx, 1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
