//go:build integration

package discipline_test

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// skipMigrations are migration files that cannot be run by pgx because they
// contain psql metacommands. Mirrors the atom-store integration test pattern.
var skipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true,
}

func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}

	ctx := context.Background()
	container, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_test"),
		tcpostgres.WithUsername("wbt"),
		tcpostgres.WithPassword("wbt"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if tErr := container.Terminate(ctx); tErr != nil {
			t.Logf("terminate container: %v", tErr)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	applyAllUpMigrations(t, ctx, pool)
	return pool
}

func applyAllUpMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	entries, err := migrationfs.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded migrations dir: %v", err)
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
			t.Logf("skipping %s (psql metacommands)", name)
			continue
		}
		body, err := migrationfs.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}

func TestPgStore_InsertAndRecentMutating(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := discipline.NewPgStore(pool, &wsID)
	ctx := context.Background()

	tests := []struct {
		name   string
		params discipline.InsertParams
	}{
		{
			name: "mutating with all fields",
			params: discipline.InsertParams{
				SessionID:  "mcp-1-1000",
				RepoName:   "wayneblacktea",
				ToolName:   "log_decision",
				IsMutating: true,
			},
		},
		{
			name: "read-only minimal",
			params: discipline.InsertParams{
				SessionID:  "mcp-1-1001",
				ToolName:   "list_tasks",
				IsMutating: false,
			},
		},
		{
			name: "mutating with linked decision",
			params: discipline.InsertParams{
				SessionID:        "mcp-1-1002",
				ToolName:         "complete_task",
				IsMutating:       true,
				LinkedDecisionID: ptrUUID(uuid.New()),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.Insert(ctx, tc.params); err != nil {
				t.Fatalf("Insert: %v", err)
			}
		})
	}

	// All three rows persisted; mutating filter pulls only the two
	// mutating ones, scoped to the test workspace.
	got, err := store.RecentMutating(ctx, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("RecentMutating: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 mutating events, got %d (%+v)", len(got), got)
	}
	for _, ev := range got {
		if !ev.IsMutating {
			t.Errorf("non-mutating leaked: %+v", ev)
		}
		if ev.WorkspaceID == nil || *ev.WorkspaceID != wsID {
			t.Errorf("workspace_id mismatch: got %v want %v", ev.WorkspaceID, wsID)
		}
	}
}

func TestPgStore_RecentDecisionTimes(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := discipline.NewPgStore(pool, &wsID)
	ctx := context.Background()

	for _, p := range []discipline.InsertParams{
		{SessionID: "alpha", ToolName: "log_decision", IsMutating: true},
		{SessionID: "alpha", ToolName: "confirm_plan", IsMutating: true},
		{SessionID: "alpha", ToolName: "add_task", IsMutating: true},
		{SessionID: "beta", ToolName: "log_decision", IsMutating: true},
	} {
		if err := store.Insert(ctx, p); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	t.Run("alpha gets two decision rows", func(t *testing.T) {
		got, err := store.RecentDecisionTimes(ctx, "alpha", time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("RecentDecisionTimes: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("alpha decisions: want 2, got %d", len(got))
		}
	})

	t.Run("beta gets one", func(t *testing.T) {
		got, err := store.RecentDecisionTimes(ctx, "beta", time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("RecentDecisionTimes: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("beta decisions: want 1, got %d", len(got))
		}
	})

	t.Run("non-existent session returns zero", func(t *testing.T) {
		got, err := store.RecentDecisionTimes(ctx, "gamma", time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("RecentDecisionTimes: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("gamma decisions: want 0, got %d", len(got))
		}
	})
}

func TestPgStore_WorkspaceScoping(t *testing.T) {
	pool := openTestPgPool(t)
	wsA := uuid.New()
	wsB := uuid.New()
	storeA := discipline.NewPgStore(pool, &wsA)
	storeB := discipline.NewPgStore(pool, &wsB)
	ctx := context.Background()

	// One mutating event into each workspace.
	if err := storeA.Insert(ctx, discipline.InsertParams{
		SessionID:  "sA",
		ToolName:   "add_task",
		IsMutating: true,
	}); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	if err := storeB.Insert(ctx, discipline.InsertParams{
		SessionID:  "sB",
		ToolName:   "add_task",
		IsMutating: true,
	}); err != nil {
		t.Fatalf("insert B: %v", err)
	}

	gotA, err := storeA.RecentMutating(ctx, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("storeA RecentMutating: %v", err)
	}
	if len(gotA) != 1 {
		t.Fatalf("storeA: want 1 event, got %d", len(gotA))
	}
	if gotA[0].SessionID != "sA" {
		t.Errorf("storeA leaked from B: %s", gotA[0].SessionID)
	}

	gotB, err := storeB.RecentMutating(ctx, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("storeB RecentMutating: %v", err)
	}
	if len(gotB) != 1 {
		t.Fatalf("storeB: want 1 event, got %d", len(gotB))
	}
	if gotB[0].SessionID != "sB" {
		t.Errorf("storeB leaked from A: %s", gotB[0].SessionID)
	}
}

func ptrUUID(id uuid.UUID) *uuid.UUID { return &id }
