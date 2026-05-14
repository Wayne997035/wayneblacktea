//go:build integration

package arch_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/arch"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var archSkipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true,
}

func openArchTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_arch_test"),
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

	applyArchUpMigrations(t, ctx, pool)
	return pool
}

func applyArchUpMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
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
		if archSkipMigrations[name] {
			t.Logf("applyArchUpMigrations: skipping %s", name)
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

func TestStorePostgres_UpsertAndGet(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	ctx := context.Background()

	got, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "wayneblacktea",
		Summary:       "personal OS",
		FileMap:       map[string]string{"cmd/server/main.go": "entrypoint"},
		LastCommitSHA: "abc123",
	})
	if err != nil {
		t.Fatalf("UpsertSnapshot: %v", err)
	}
	if got.Slug != "wayneblacktea" || got.FileMap["cmd/server/main.go"] != "entrypoint" {
		t.Fatalf("upserted snapshot = %+v", got)
	}

	fetched, err := store.GetSnapshot(ctx, "wayneblacktea")
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if fetched.ID != got.ID || fetched.LastCommitSHA != "abc123" {
		t.Fatalf("fetched snapshot = %+v, want ID %s sha abc123", fetched, got.ID)
	}
}

func TestStorePostgres_UpsertReplacesExisting(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	ctx := context.Background()

	first, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "repo",
		Summary:       "old",
		FileMap:       map[string]string{"old.go": "old"},
		LastCommitSHA: "oldsha",
	})
	if err != nil {
		t.Fatalf("first UpsertSnapshot: %v", err)
	}
	second, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "repo",
		Summary:       "new",
		FileMap:       map[string]string{"new.go": "new"},
		LastCommitSHA: "newsha",
	})
	if err != nil {
		t.Fatalf("second UpsertSnapshot: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("upsert changed ID: got %s, want %s", second.ID, first.ID)
	}
	if second.Summary != "new" || second.FileMap["new.go"] != "new" || second.LastCommitSHA != "newsha" {
		t.Fatalf("snapshot was not replaced: %+v", second)
	}
}

func TestStorePostgres_GetSnapshotNotFound(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	if _, err := store.GetSnapshot(context.Background(), "missing"); !errors.Is(err, arch.ErrNotFound) {
		t.Fatalf("GetSnapshot missing err = %v, want ErrNotFound", err)
	}
}

func TestStorePostgres_LargeFileMapJSONRoundTrip(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	ctx := context.Background()
	fileMap := make(map[string]string, 150)
	for i := 0; i < 150; i++ {
		fileMap[fmt.Sprintf("internal/pkg/file_%03d.go", i)] = strings.Repeat("purpose ", 20)
	}

	got, err := store.UpsertSnapshot(ctx, arch.UpsertParams{Slug: "large", Summary: "large", FileMap: fileMap})
	if err != nil {
		t.Fatalf("UpsertSnapshot: %v", err)
	}
	if len(got.FileMap) != len(fileMap) {
		t.Fatalf("file map len = %d, want %d", len(got.FileMap), len(fileMap))
	}
}

func TestStorePostgres_EmptyFileMapStoresEmptyObject(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	got, err := store.UpsertSnapshot(context.Background(), arch.UpsertParams{Slug: "empty", Summary: "empty"})
	if err != nil {
		t.Fatalf("UpsertSnapshot: %v", err)
	}
	if got.FileMap == nil || len(got.FileMap) != 0 {
		t.Fatalf("FileMap = %#v, want empty map", got.FileMap)
	}
}
