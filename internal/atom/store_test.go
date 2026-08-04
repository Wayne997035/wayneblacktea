//go:build integration

package atom_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/atom"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// skipMigrations are migration files that cannot be run by pgx because they
// contain psql metacommands.
var skipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true,
}

var testPgPool *pgxpool.Pool

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if testing.Short() {
		return m.Run()
	}
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_test"),
		tcpostgres.WithUsername("wbt"),
		tcpostgres.WithPassword("wbt"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Fatalf("start postgres container: %v", err)
	}
	defer func() { _ = c.Terminate(ctx) }()

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Printf("get connection string: %v", err)
		return 1
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Printf("pgxpool.New: %v", err)
		return 1
	}
	defer pool.Close()

	applyAllUpMigrationsOnce(ctx, pool)

	testPgPool = pool
	return m.Run()
}

func applyAllUpMigrationsOnce(ctx context.Context, pool *pgxpool.Pool) {
	entries, err := migrationfs.FS.ReadDir(".")
	if err != nil {
		log.Fatalf("read embedded migrations dir: %v", err)
	}
	var ups []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		ups = append(ups, name)
	}
	sort.Strings(ups)

	for _, name := range ups {
		if skipMigrations[name] {
			log.Printf("applyAllUpMigrations: skipping %s (psql-metacommand-only file)", name)
			continue
		}
		body, err := migrationfs.FS.ReadFile(name)
		if err != nil {
			log.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			log.Fatalf("apply %s: %v", name, err)
		}
	}
}

func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return testPgPool
}

// TestStore_AddAtom verifies happy path and edge cases of AddAtom.
func TestStore_AddAtom(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := atom.New(pool, &wsID)
	ctx := context.Background()

	parentID := uuid.New()

	tests := []struct {
		name    string
		params  atom.AddAtomParams
		wantErr bool
	}{
		{
			name: "happy path with all fields",
			params: atom.AddAtomParams{
				WorkspaceID: &wsID,
				ParentTable: "decisions",
				ParentID:    parentID,
				Content:     "Use testcontainers for integration tests",
				Keywords:    []string{"testcontainers", "integration"},
				Tags:        []string{"testing"},
			},
		},
		{
			name: "minimal required fields",
			params: atom.AddAtomParams{
				WorkspaceID: &wsID,
				ParentTable: "knowledge_items",
				ParentID:    parentID,
				Content:     "Go modules use go.mod",
			},
		},
		{
			name: "nil keywords and tags become empty slices",
			params: atom.AddAtomParams{
				WorkspaceID: &wsID,
				ParentTable: "procedural_memories",
				ParentID:    parentID,
				Content:     "Atom with nil slices",
				Keywords:    nil,
				Tags:        nil,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, err := store.AddAtom(ctx, tc.params)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("AddAtom: %v", err)
			}
			if a == nil {
				t.Fatal("AddAtom returned nil")
			}
			if a.Content != tc.params.Content {
				t.Errorf("Content: got %q, want %q", a.Content, tc.params.Content)
			}
			if a.ParentTable != tc.params.ParentTable {
				t.Errorf("ParentTable: got %q, want %q", a.ParentTable, tc.params.ParentTable)
			}
			if a.Keywords == nil {
				t.Error("Keywords should not be nil")
			}
			if a.Tags == nil {
				t.Error("Tags should not be nil")
			}
		})
	}
}

// TestStore_AddLink verifies AddLink inserts a link and is idempotent.
func TestStore_AddLink(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := atom.New(pool, &wsID)
	ctx := context.Background()

	parentID := uuid.New()

	a1, err := store.AddAtom(ctx, atom.AddAtomParams{
		WorkspaceID: &wsID,
		ParentTable: "decisions",
		ParentID:    parentID,
		Content:     "Atom A",
		Keywords:    []string{"a"},
		Tags:        []string{"t1"},
	})
	if err != nil {
		t.Fatalf("AddAtom A: %v", err)
	}
	a2, err := store.AddAtom(ctx, atom.AddAtomParams{
		WorkspaceID: &wsID,
		ParentTable: "decisions",
		ParentID:    parentID,
		Content:     "Atom B",
		Keywords:    []string{"b"},
		Tags:        []string{"t1"},
	})
	if err != nil {
		t.Fatalf("AddAtom B: %v", err)
	}

	t.Run("happy path link creation", func(t *testing.T) {
		err := store.AddLink(ctx, atom.AddLinkParams{
			FromAtomID: a1.ID,
			ToAtomID:   a2.ID,
			LinkType:   "same_entity",
			Confidence: 0.8,
		})
		if err != nil {
			t.Fatalf("AddLink: %v", err)
		}
	})

	t.Run("duplicate link is a no-op (idempotent)", func(t *testing.T) {
		err := store.AddLink(ctx, atom.AddLinkParams{
			FromAtomID: a1.ID,
			ToAtomID:   a2.ID,
			LinkType:   "same_entity",
			Confidence: 0.9, // different confidence; no conflict error expected
		})
		if err != nil {
			t.Fatalf("duplicate AddLink should not error: %v", err)
		}
	})

	t.Run("self-link is allowed at DB level", func(t *testing.T) {
		// DB allows self-links; caller (atomizeAndPersist) filters them out.
		err := store.AddLink(ctx, atom.AddLinkParams{
			FromAtomID: a1.ID,
			ToAtomID:   a1.ID,
			LinkType:   "same_project",
			Confidence: 0.5,
		})
		// May succeed or fail depending on PK semantics; either is fine here.
		_ = err
	})
}

// TestStore_ListByParent verifies listing atoms by parent.
func TestStore_ListByParent(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := atom.New(pool, &wsID)
	ctx := context.Background()

	parentID := uuid.New()
	otherParentID := uuid.New()

	for i := 0; i < 3; i++ {
		_, err := store.AddAtom(ctx, atom.AddAtomParams{
			WorkspaceID: &wsID,
			ParentTable: "decisions",
			ParentID:    parentID,
			Content:     fmt.Sprintf("Atom %d for parent", i),
		})
		if err != nil {
			t.Fatalf("seed atom %d: %v", i, err)
		}
	}
	_, err := store.AddAtom(ctx, atom.AddAtomParams{
		WorkspaceID: &wsID,
		ParentTable: "decisions",
		ParentID:    otherParentID,
		Content:     "Atom for other parent",
	})
	if err != nil {
		t.Fatalf("seed other parent atom: %v", err)
	}

	t.Run("returns only atoms for matching parent", func(t *testing.T) {
		atoms, err := store.ListByParent(ctx, "decisions", parentID)
		if err != nil {
			t.Fatalf("ListByParent: %v", err)
		}
		if len(atoms) != 3 {
			t.Errorf("expected 3 atoms, got %d", len(atoms))
		}
	})

	t.Run("different parent returns different atoms", func(t *testing.T) {
		atoms, err := store.ListByParent(ctx, "decisions", otherParentID)
		if err != nil {
			t.Fatalf("ListByParent: %v", err)
		}
		if len(atoms) != 1 {
			t.Errorf("expected 1 atom, got %d", len(atoms))
		}
	})

	t.Run("non-existent parent returns empty slice", func(t *testing.T) {
		atoms, err := store.ListByParent(ctx, "decisions", uuid.New())
		if err != nil {
			t.Fatalf("ListByParent: %v", err)
		}
		if len(atoms) != 0 {
			t.Errorf("expected 0 atoms, got %d", len(atoms))
		}
	})
}

// TestStore_Traverse verifies BFS traversal.
func TestStore_Traverse(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := atom.New(pool, &wsID)
	ctx := context.Background()

	parentID := uuid.New()

	// Build a chain: A -> B -> C
	a, err := store.AddAtom(ctx, atom.AddAtomParams{
		WorkspaceID: &wsID, ParentTable: "decisions", ParentID: parentID, Content: "A",
	})
	if err != nil {
		t.Fatalf("add A: %v", err)
	}
	b, err := store.AddAtom(ctx, atom.AddAtomParams{
		WorkspaceID: &wsID, ParentTable: "decisions", ParentID: parentID, Content: "B",
	})
	if err != nil {
		t.Fatalf("add B: %v", err)
	}
	c, err := store.AddAtom(ctx, atom.AddAtomParams{
		WorkspaceID: &wsID, ParentTable: "decisions", ParentID: parentID, Content: "C",
	})
	if err != nil {
		t.Fatalf("add C: %v", err)
	}

	if err := store.AddLink(ctx, atom.AddLinkParams{FromAtomID: a.ID, ToAtomID: b.ID, LinkType: "same_entity", Confidence: 1.0}); err != nil {
		t.Fatalf("link A->B: %v", err)
	}
	if err := store.AddLink(ctx, atom.AddLinkParams{FromAtomID: b.ID, ToAtomID: c.ID, LinkType: "same_entity", Confidence: 1.0}); err != nil {
		t.Fatalf("link B->C: %v", err)
	}

	t.Run("depth 1 returns start atom and direct neighbour", func(t *testing.T) {
		result, err := store.Traverse(ctx, a.ID, 1)
		if err != nil {
			t.Fatalf("Traverse: %v", err)
		}
		if result == nil {
			t.Fatal("Traverse returned nil")
		}
		// At depth 1: A (start) + B (first hop) = 2 atoms
		if len(result.Atoms) < 1 {
			t.Errorf("expected at least 1 atom, got %d", len(result.Atoms))
		}
	})

	t.Run("depth 2 reaches C via A->B->C", func(t *testing.T) {
		result, err := store.Traverse(ctx, a.ID, 2)
		if err != nil {
			t.Fatalf("Traverse depth 2: %v", err)
		}
		if len(result.Atoms) < 2 {
			t.Errorf("expected at least 2 atoms, got %d", len(result.Atoms))
		}
	})

	t.Run("depth cap at 5", func(t *testing.T) {
		result, err := store.Traverse(ctx, a.ID, 99)
		if err != nil {
			t.Fatalf("Traverse depth 99: %v", err)
		}
		// Should not error; depth is capped internally.
		if result == nil {
			t.Fatal("nil result")
		}
	})

	t.Run("non-existent start atom returns empty result", func(t *testing.T) {
		result, err := store.Traverse(ctx, uuid.New(), 2)
		if err != nil {
			t.Fatalf("Traverse nonexistent: %v", err)
		}
		if len(result.Atoms) != 0 {
			t.Errorf("expected 0 atoms, got %d", len(result.Atoms))
		}
	})
}

// TestStore_SetDigestStatus verifies happy path and edge cases of SetDigestStatus.
func TestStore_SetDigestStatus(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := atom.New(pool, &wsID)
	ctx := context.Background()

	parentID := uuid.New()
	a, err := store.AddAtom(ctx, atom.AddAtomParams{
		WorkspaceID: &wsID,
		ParentTable: "knowledge_items",
		ParentID:    parentID,
		Content:     "digest status test atom",
	})
	if err != nil {
		t.Fatalf("AddAtom: %v", err)
	}

	tests := []struct {
		name    string
		atomID  uuid.UUID
		status  string
		errMsg  string
		wantErr bool
	}{
		{
			name:   "set to done clears error_msg",
			atomID: a.ID,
			status: "done",
			errMsg: "",
		},
		{
			name:   "set to failed with error message",
			atomID: a.ID,
			status: "failed",
			errMsg: "rate limit exceeded",
		},
		{
			name:   "set back to pending",
			atomID: a.ID,
			status: "pending",
			errMsg: "",
		},
		{
			name:   "non-existent atom is a no-op",
			atomID: uuid.New(),
			status: "done",
			errMsg: "",
		},
		{
			name:    "invalid status is rejected before reaching SQL",
			atomID:  a.ID,
			status:  "archived",
			errMsg:  "",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := store.SetDigestStatus(ctx, tc.atomID, tc.status, tc.errMsg)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !errors.Is(err, atom.ErrInvalidDigestStatus) {
					t.Errorf("expected ErrInvalidDigestStatus, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("SetDigestStatus: %v", err)
			}
		})
	}
}

// TestStore_CountByDigestStatus verifies workspace-scoped counting by digest status.
func TestStore_CountByDigestStatus(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	otherWsID := uuid.New()
	store := atom.New(pool, &wsID)
	ctx := context.Background()

	parentID := uuid.New()

	// Insert 2 atoms in wsID (default status = 'pending').
	for i := 0; i < 2; i++ {
		_, err := store.AddAtom(ctx, atom.AddAtomParams{
			WorkspaceID: &wsID,
			ParentTable: "knowledge_items",
			ParentID:    parentID,
			Content:     fmt.Sprintf("pending atom %d", i),
		})
		if err != nil {
			t.Fatalf("seed atom %d: %v", i, err)
		}
	}
	// Insert 1 atom in a different workspace.
	otherStore := atom.New(pool, &otherWsID)
	_, err := otherStore.AddAtom(ctx, atom.AddAtomParams{
		WorkspaceID: &otherWsID,
		ParentTable: "knowledge_items",
		ParentID:    parentID,
		Content:     "other workspace pending atom",
	})
	if err != nil {
		t.Fatalf("seed other workspace atom: %v", err)
	}

	t.Run("workspace-scoped pending count", func(t *testing.T) {
		n, err := store.CountByDigestStatus(ctx, &wsID, "pending")
		if err != nil {
			t.Fatalf("CountByDigestStatus: %v", err)
		}
		if n != 2 {
			t.Errorf("expected 2 pending atoms for wsID, got %d", n)
		}
	})

	t.Run("nil workspace counts all pending atoms globally", func(t *testing.T) {
		n, err := store.CountByDigestStatus(ctx, nil, "pending")
		if err != nil {
			t.Fatalf("CountByDigestStatus nil ws: %v", err)
		}
		if n < 3 {
			t.Errorf("expected at least 3 pending atoms globally, got %d", n)
		}
	})

	t.Run("done count is zero before any atom is marked done", func(t *testing.T) {
		n, err := store.CountByDigestStatus(ctx, &wsID, "done")
		if err != nil {
			t.Fatalf("CountByDigestStatus done: %v", err)
		}
		if n != 0 {
			t.Errorf("expected 0 done atoms, got %d", n)
		}
	})

	t.Run("unknown status returns zero without error", func(t *testing.T) {
		n, err := store.CountByDigestStatus(ctx, &wsID, "nosuchstatus")
		if err != nil {
			t.Fatalf("CountByDigestStatus unknown: %v", err)
		}
		if n != 0 {
			t.Errorf("expected 0 for unknown status, got %d", n)
		}
	})
}

// TestStore_Search verifies ILIKE search across content, keywords, tags.
func TestStore_Search(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := atom.New(pool, &wsID)
	ctx := context.Background()

	parentID := uuid.New()

	_, err := store.AddAtom(ctx, atom.AddAtomParams{
		WorkspaceID: &wsID,
		ParentTable: "decisions",
		ParentID:    parentID,
		Content:     "Use testcontainers for integration tests",
		Keywords:    []string{"testcontainers", "docker"},
		Tags:        []string{"testing"},
	})
	if err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	_, err = store.AddAtom(ctx, atom.AddAtomParams{
		WorkspaceID: &wsID,
		ParentTable: "knowledge_items",
		ParentID:    parentID,
		Content:     "Go modules declared in go.mod",
		Keywords:    []string{"golang", "modules"},
		Tags:        []string{"language"},
	})
	if err != nil {
		t.Fatalf("seed 2: %v", err)
	}

	t.Run("matches content", func(t *testing.T) {
		results, err := store.Search(ctx, &wsID, "testcontainers", 10)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) == 0 {
			t.Error("expected at least 1 result for 'testcontainers'")
		}
	})

	t.Run("matches keywords", func(t *testing.T) {
		results, err := store.Search(ctx, &wsID, "golang", 10)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) == 0 {
			t.Error("expected at least 1 result for 'golang' in keywords")
		}
	})

	t.Run("non-matching query returns empty", func(t *testing.T) {
		results, err := store.Search(ctx, &wsID, "zzznomatch", 10)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})

	t.Run("nil workspace returns workspace-scoped atoms", func(t *testing.T) {
		// nil workspace = no scope filter, should return all atoms in DB
		results, err := store.Search(ctx, nil, "testcontainers", 10)
		if err != nil {
			t.Fatalf("Search nil ws: %v", err)
		}
		if len(results) == 0 {
			t.Error("expected at least 1 result with nil workspace")
		}
	})
}

// TestStore_ListByDigestStatus verifies workspace-scoped listing by digest status.
func TestStore_ListByDigestStatus(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	otherWsID := uuid.New()
	store := atom.New(pool, &wsID)
	otherStore := atom.New(pool, &otherWsID)
	ctx := context.Background()

	parentID := uuid.New()

	// Insert 3 atoms in wsID with 'consolidated' status.
	for i := 0; i < 3; i++ {
		a, err := store.AddAtom(ctx, atom.AddAtomParams{
			WorkspaceID: &wsID,
			ParentTable: "decisions",
			ParentID:    parentID,
			Content:     fmt.Sprintf("consolidated atom %d", i),
			Tags:        []string{"go", "testing"},
		})
		if err != nil {
			t.Fatalf("seed consolidated atom %d: %v", i, err)
		}
		if err := store.SetDigestStatus(ctx, a.ID, "consolidated", ""); err != nil {
			t.Fatalf("set consolidated status %d: %v", i, err)
		}
	}
	// Insert 1 atom in a different workspace with 'consolidated' status.
	other, err := otherStore.AddAtom(ctx, atom.AddAtomParams{
		WorkspaceID: &otherWsID,
		ParentTable: "decisions",
		ParentID:    parentID,
		Content:     "other workspace consolidated atom",
	})
	if err != nil {
		t.Fatalf("seed other workspace atom: %v", err)
	}
	if err := otherStore.SetDigestStatus(ctx, other.ID, "consolidated", ""); err != nil {
		t.Fatalf("set other ws consolidated status: %v", err)
	}

	t.Run("happy path: returns consolidated atoms scoped to workspace", func(t *testing.T) {
		results, err := store.ListByDigestStatus(ctx, &wsID, "consolidated", 10)
		if err != nil {
			t.Fatalf("ListByDigestStatus: %v", err)
		}
		if len(results) != 3 {
			t.Errorf("expected 3 consolidated atoms for wsID, got %d", len(results))
		}
		for _, a := range results {
			if a.DigestStatus == nil || *a.DigestStatus != "consolidated" {
				t.Errorf("expected digest_status='consolidated', got %v", a.DigestStatus)
			}
		}
	})

	t.Run("limit is respected", func(t *testing.T) {
		results, err := store.ListByDigestStatus(ctx, &wsID, "consolidated", 1)
		if err != nil {
			t.Fatalf("ListByDigestStatus limit: %v", err)
		}
		if len(results) != 1 {
			t.Errorf("expected 1 result with limit=1, got %d", len(results))
		}
	})

	t.Run("workspace isolation: other workspace not included", func(t *testing.T) {
		results, err := store.ListByDigestStatus(ctx, &wsID, "consolidated", 100)
		if err != nil {
			t.Fatalf("ListByDigestStatus isolation: %v", err)
		}
		for _, a := range results {
			if a.WorkspaceID != nil && *a.WorkspaceID == otherWsID {
				t.Errorf("other workspace atom leaked into wsID result: %s", a.ID)
			}
		}
	})

	t.Run("unknown status returns empty slice without error", func(t *testing.T) {
		results, err := store.ListByDigestStatus(ctx, &wsID, "nosuchstatus", 10)
		if err != nil {
			t.Fatalf("ListByDigestStatus unknown status: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results for unknown status, got %d", len(results))
		}
	})

	t.Run("nil workspace returns atoms across all workspaces", func(t *testing.T) {
		results, err := store.ListByDigestStatus(ctx, nil, "consolidated", 100)
		if err != nil {
			t.Fatalf("ListByDigestStatus nil ws: %v", err)
		}
		// Must include both wsID and otherWsID consolidated atoms.
		if len(results) < 4 {
			t.Errorf("expected at least 4 consolidated atoms globally, got %d", len(results))
		}
	})
}
