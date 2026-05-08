package knowledge_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// knowledgeSkipMigrations are .up.sql files that use psql metacommands and
// cannot be applied via pgx. See parallel list in internal/gtd.
var knowledgeSkipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true,
}

// openKnowledgePgPool starts a throwaway Postgres+pgvector container, applies
// all migrations, and returns a pool. Skipped when -short is set (requires Docker).
func openKnowledgePgPool(t *testing.T) *pgxpool.Pool {
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
		t.Fatalf("get connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	applyKnowledgeMigrations(t, ctx, pool)
	return pool
}

func applyKnowledgeMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
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
		if knowledgeSkipMigrations[name] {
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

// TestKnowledgeStore_Pg_UpdateLearningValue verifies that the Postgres-backed
// knowledge.Store.UpdateLearningValue persists the star-rating and returns
// ErrNotFound for unknown IDs. Mirrors TestKnowledgeStore_UpdateLearningValue
// in internal/storage/sqlite/knowledge_test.go.
func TestKnowledgeStore_Pg_UpdateLearningValue(t *testing.T) {
	pool := openKnowledgePgPool(t)
	wsID := uuid.New()
	store := knowledge.NewStore(pool, nil, &wsID)
	ctx := context.Background()

	item, err := store.AddItem(ctx, knowledge.AddItemParams{
		Type: "til", Title: "Ebbinghaus effect", Content: "Forgetting curve",
	})
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	// Happy path: update and verify round-trip.
	for _, v := range []int{1, 3, 5} {
		if err := store.UpdateLearningValue(ctx, item.ID, v); err != nil {
			t.Errorf("UpdateLearningValue(%d): %v", v, err)
		}
		got, err := store.GetByID(ctx, item.ID)
		if err != nil {
			t.Fatalf("GetByID after update to %d: %v", v, err)
		}
		if !got.LearningValue.Valid || int(got.LearningValue.Int32) != v {
			t.Errorf("after UpdateLearningValue(%d): got LearningValue=%+v", v, got.LearningValue)
		}
	}

	// Unknown ID → ErrNotFound.
	if err := store.UpdateLearningValue(ctx, uuid.New(), 3); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown ID, got %v", err)
	}
}
