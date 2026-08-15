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
	c, err := tcpostgres.Run(
		ctx,
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

// fileMapPtr is the *map[string]string literal helper for
// arch.UpsertParams.FileMap's patch semantics: a non-nil pointer (even to
// an empty map) is an explicit value, as opposed to the Go zero value nil
// which means "field omitted, keep whatever is stored". Mirrored in
// internal/storage/sqlite/arch_test.go for the SQLite backend.
func fileMapPtr(m map[string]string) *map[string]string { return &m }

// strPtr is the *string literal helper for arch.UpsertParams.Summary and
// LastCommitSHA's patch semantics (security review PR #157 round 3,
// unifying what M-3 originally gave FileMap alone): a non-nil pointer —
// even to "" — is an explicit value, as opposed to the Go zero value nil
// which means "field omitted, keep whatever is stored". Mirrored in
// internal/storage/sqlite/arch_test.go for the SQLite backend.
func strPtr(s string) *string { return &s }

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
		Summary:       strPtr("personal OS"),
		FileMap:       fileMapPtr(map[string]string{"cmd/server/main.go": "entrypoint"}),
		LastCommitSHA: strPtr("abc123"),
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
		Summary:       strPtr("old"),
		FileMap:       fileMapPtr(map[string]string{"old.go": "old"}),
		LastCommitSHA: strPtr("oldsha"),
	})
	if err != nil {
		t.Fatalf("first UpsertSnapshot: %v", err)
	}
	second, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "repo",
		Summary:       strPtr("new"),
		FileMap:       fileMapPtr(map[string]string{"new.go": "new"}),
		LastCommitSHA: strPtr("newsha"),
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

	got, err := store.UpsertSnapshot(ctx, arch.UpsertParams{Slug: "large", Summary: strPtr("large"), FileMap: fileMapPtr(fileMap)})
	if err != nil {
		t.Fatalf("UpsertSnapshot: %v", err)
	}
	if len(got.FileMap) != len(fileMap) {
		t.Fatalf("file map len = %d, want %d", len(got.FileMap), len(fileMap))
	}
}

func TestStorePostgres_EmptyFileMapStoresEmptyObject(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	got, err := store.UpsertSnapshot(context.Background(), arch.UpsertParams{Slug: "empty", Summary: strPtr("empty")})
	if err != nil {
		t.Fatalf("UpsertSnapshot: %v", err)
	}
	if got.FileMap == nil || len(got.FileMap) != 0 {
		t.Fatalf("FileMap = %#v, want empty map", got.FileMap)
	}
}

// --- M-3 patch-semantics three-state matrix (security review PR #157) ---
//
// Mirrors internal/storage/sqlite/arch_test.go's matrix for parity: FileMap
// ABSENT (nil pointer) preserves whatever is stored; FileMap present
// pointing at an EMPTY map explicitly clears it; FileMap present with
// CONTENT replaces the stored map outright (not a merge).

func TestStorePostgres_UpsertFileMapAbsent_PreservesExisting(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	ctx := context.Background()

	seeded := map[string]string{
		"cmd/server/main.go":     "entrypoint",
		"internal/mcp/server.go": "mcp wiring",
		"internal/arch/arch.go":  "arch domain types",
	}
	if _, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "pg-patch-absent",
		Summary: strPtr("first"),
		FileMap: fileMapPtr(seeded),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	// Simulate an agent that follows the read-then-write protocol but
	// never saw the existing file_map (get_project_arch's default omits
	// it, W2) and therefore omits the argument entirely on write-back.
	got, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "pg-patch-absent",
		Summary: strPtr("second, file_map omitted"),
		FileMap: nil,
	})
	if err != nil {
		t.Fatalf("map-less upsert: %v", err)
	}
	if len(got.FileMap) != len(seeded) {
		t.Fatalf("DATA LOSS: file_map omitted on upsert wiped stored map: got %d entries, want %d preserved: %v",
			len(got.FileMap), len(seeded), got.FileMap)
	}
	for path, purpose := range seeded {
		if got.FileMap[path] != purpose {
			t.Errorf("file_map[%q] = %q, want preserved %q", path, got.FileMap[path], purpose)
		}
	}
	if got.Summary != "second, file_map omitted" {
		t.Errorf("summary should still update independently of file_map: got %q", got.Summary)
	}
}

func TestStorePostgres_UpsertFileMapExplicitEmpty_Clears(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	ctx := context.Background()

	if _, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "pg-patch-explicit-empty",
		Summary: strPtr("first"),
		FileMap: fileMapPtr(map[string]string{"a.go": "a", "b.go": "b", "c.go": "c"}),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "pg-patch-explicit-empty",
		Summary: strPtr("second, explicit clear"),
		FileMap: fileMapPtr(map[string]string{}),
	})
	if err != nil {
		t.Fatalf("explicit-clear upsert: %v", err)
	}
	if len(got.FileMap) != 0 {
		t.Fatalf("explicit {} should clear file_map, got %d entries: %v", len(got.FileMap), got.FileMap)
	}
}

func TestStorePostgres_UpsertFileMapContent_Replaces(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	ctx := context.Background()

	if _, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "pg-patch-replace",
		Summary: strPtr("first"),
		FileMap: fileMapPtr(map[string]string{"old.go": "old", "also-old.go": "also old"}),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "pg-patch-replace",
		Summary: strPtr("second, replaced"),
		FileMap: fileMapPtr(map[string]string{"new.go": "new"}),
	})
	if err != nil {
		t.Fatalf("replace upsert: %v", err)
	}
	if len(got.FileMap) != 1 || got.FileMap["new.go"] != "new" {
		t.Fatalf("expected file_map replaced (not merged) with exactly {new.go: new}, got %v", got.FileMap)
	}
	if _, stillThere := got.FileMap["old.go"]; stillThere {
		t.Errorf("old.go should have been replaced away, still present: %v", got.FileMap)
	}
}

// --- round-3 patch-semantics unification: summary / last_commit_sha -----
//
// Before security review PR #157 round 3, summary and last_commit_sha were
// unconditionally EXCLUDED.<col> in the upsert SQL — an agent that followed
// the read-then-write protocol but omitted either field (stringArg folds
// "absent" and "present-but-empty" into "") silently wiped it. These mirror
// the file_map three-state matrix above for the other two patch-semantics
// fields. Mirrored in internal/storage/sqlite/arch_test.go for parity.

func TestStorePostgres_UpsertSummaryAbsent_PreservesExisting(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	ctx := context.Background()

	if _, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "pg-summary-patch-absent",
		Summary: strPtr("original summary, must survive"),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "pg-summary-patch-absent",
		LastCommitSHA: strPtr("sha-only-update"),
		// Summary omitted entirely.
	})
	if err != nil {
		t.Fatalf("summary-less upsert: %v", err)
	}
	if got.Summary != "original summary, must survive" {
		t.Fatalf("DATA LOSS: summary omitted on upsert wiped the stored value: got %q, want preserved %q",
			got.Summary, "original summary, must survive")
	}
	if got.LastCommitSHA != "sha-only-update" {
		t.Errorf("last_commit_sha should still update independently of summary: got %q", got.LastCommitSHA)
	}
}

func TestStorePostgres_UpsertSummaryExplicitEmpty_Clears(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	ctx := context.Background()

	if _, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "pg-summary-patch-explicit-empty",
		Summary: strPtr("first"),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "pg-summary-patch-explicit-empty",
		Summary: strPtr(""),
	})
	if err != nil {
		t.Fatalf("explicit-clear upsert: %v", err)
	}
	if got.Summary != "" {
		t.Fatalf("explicit \"\" should clear summary, got %q", got.Summary)
	}
}

func TestStorePostgres_UpsertSummaryContent_Replaces(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	ctx := context.Background()

	if _, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "pg-summary-patch-replace",
		Summary: strPtr("old summary"),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "pg-summary-patch-replace",
		Summary: strPtr("new summary"),
	})
	if err != nil {
		t.Fatalf("replace upsert: %v", err)
	}
	if got.Summary != "new summary" {
		t.Fatalf("expected summary replaced, got %q", got.Summary)
	}
}

func TestStorePostgres_UpsertLastCommitSHAAbsent_PreservesExisting(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	ctx := context.Background()

	if _, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "pg-sha-patch-absent",
		Summary:       strPtr("first"),
		LastCommitSHA: strPtr("original-sha"),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "pg-sha-patch-absent",
		Summary: strPtr("summary-only update"),
		// LastCommitSHA omitted entirely.
	})
	if err != nil {
		t.Fatalf("sha-less upsert: %v", err)
	}
	if got.LastCommitSHA != "original-sha" {
		t.Fatalf("DATA LOSS: last_commit_sha omitted on upsert wiped the stored value: got %q, want preserved %q",
			got.LastCommitSHA, "original-sha")
	}
}

func TestStorePostgres_UpsertLastCommitSHAExplicitEmpty_Clears(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	ctx := context.Background()

	if _, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "pg-sha-patch-explicit-empty",
		Summary:       strPtr("first"),
		LastCommitSHA: strPtr("abc123"),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "pg-sha-patch-explicit-empty",
		Summary:       strPtr("second"),
		LastCommitSHA: strPtr(""),
	})
	if err != nil {
		t.Fatalf("explicit-clear upsert: %v", err)
	}
	if got.LastCommitSHA != "" {
		t.Fatalf("explicit \"\" should clear last_commit_sha, got %q", got.LastCommitSHA)
	}
}

func TestStorePostgres_UpsertLastCommitSHAContent_Replaces(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	ctx := context.Background()

	if _, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "pg-sha-patch-replace",
		Summary:       strPtr("first"),
		LastCommitSHA: strPtr("old-sha"),
	}); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}

	got, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "pg-sha-patch-replace",
		Summary:       strPtr("second"),
		LastCommitSHA: strPtr("new-sha"),
	})
	if err != nil {
		t.Fatalf("replace upsert: %v", err)
	}
	if got.LastCommitSHA != "new-sha" {
		t.Fatalf("expected last_commit_sha replaced, got %q", got.LastCommitSHA)
	}
}

// TestStorePostgres_StalenessNotBrokenByOmittedSHA is the "new problem #2"
// regression guard from the round-3 dispatch: unifying the patch semantics
// must not silently trade "data loss" for "staleness check permanently
// wrong". The core protocol (mcpInstructions, server.go) compares
// last_commit_sha against `git rev-parse HEAD` — if omitting the field ever
// resolved to something other than "keep the existing SHA", that
// comparison would be permanently right or permanently wrong regardless of
// the real HEAD. Mirrored in internal/storage/sqlite/arch_test.go.
func TestStorePostgres_StalenessNotBrokenByOmittedSHA(t *testing.T) {
	store := arch.NewStore(openArchTestPgPool(t))
	ctx := context.Background()

	if _, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "pg-staleness",
		Summary:       strPtr("v1"),
		LastCommitSHA: strPtr("sha-A"),
	}); err != nil {
		t.Fatalf("seed upsert (sha=A): %v", err)
	}

	// Upsert that omits last_commit_sha (e.g. an agent only refreshing
	// summary/file_map) must leave sha-A in place for staleness detection.
	afterOmit, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:    "pg-staleness",
		Summary: strPtr("v2, sha omitted"),
	})
	if err != nil {
		t.Fatalf("sha-omitted upsert: %v", err)
	}
	if afterOmit.LastCommitSHA != "sha-A" {
		t.Fatalf("staleness check broken: omitting last_commit_sha changed it from %q to %q, want preserved %q",
			"sha-A", afterOmit.LastCommitSHA, "sha-A")
	}

	// An explicit new SHA must still take effect — proves "omit preserves"
	// was not implemented as "always ignore explicit values too".
	afterExplicit, err := store.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          "pg-staleness",
		Summary:       strPtr("v3, explicit sha"),
		LastCommitSHA: strPtr("sha-B"),
	})
	if err != nil {
		t.Fatalf("explicit-sha upsert: %v", err)
	}
	if afterExplicit.LastCommitSHA != "sha-B" {
		t.Fatalf("explicit last_commit_sha did not take effect: got %q, want %q", afterExplicit.LastCommitSHA, "sha-B")
	}
}
