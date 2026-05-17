//go:build integration

package arch_test

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

	"github.com/Wayne997035/wayneblacktea/internal/arch"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var archSkipMigrations = map[string]bool{
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
		tcpostgres.WithDatabase("wbt_arch_test"),
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

	applyArchUpMigrationsOnce(ctx, pool)

	testPgPool = pool
	return m.Run()
}

func applyArchUpMigrationsOnce(ctx context.Context, pool *pgxpool.Pool) {
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
		if archSkipMigrations[name] {
			log.Printf("applyArchUpMigrations: skipping %s", name)
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

func openArchTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return testPgPool
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
