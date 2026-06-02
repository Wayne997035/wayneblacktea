package aicost

import (
	"context"
	"flag"
	"log"
	"os"
	"sort"
	"strings"
	"testing"

	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// skipMigrationsAICost lists .up.sql files that cannot be applied in the test
// container (contain psql metacommands). Mirrors schedulerSkipMigrations.
var skipMigrationsAICost = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true,
}

var testAICostPool *pgxpool.Pool

func TestMain(m *testing.M) {
	// flag.Parse must be called before testing.Short() is valid.
	flag.Parse()
	os.Exit(runIntegration(m))
}

func runIntegration(m *testing.M) int {
	if testing.Short() {
		return m.Run()
	}
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_aicost_test"),
		tcpostgres.WithUsername("wbt"),
		tcpostgres.WithPassword("wbt"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Printf("start postgres container: %v", err)
		return 1
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

	applyMigrationsAICost(ctx, pool)
	testAICostPool = pool
	return m.Run()
}

func applyMigrationsAICost(ctx context.Context, pool *pgxpool.Pool) {
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
		if skipMigrationsAICost[name] {
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

func openAICostTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return testAICostPool
}

// TestPgRecorder_Record_WritesRow verifies that pgRecorder inserts a row and
// that the inserted values match the RecordParams.
func TestPgRecorder_Record_WritesRow(t *testing.T) {
	pool := openAICostTestPool(t)
	ctx := context.Background()
	wsID := uuid.New()

	rec := NewPgRecorder(pool)
	p := RecordParams{
		Caller:           "test.caller",
		Model:            "claude-haiku-4-5",
		InputTokens:      1_000_000,
		OutputTokens:     1_000_000,
		CacheReadTokens:  500,
		CacheWriteTokens: 100,
	}
	rec.Record(ctx, &wsID, p)

	// Verify the row was written.
	var (
		caller           string
		model            string
		inputTokens      int64
		outputTokens     int64
		cacheReadTokens  int64
		cacheWriteTokens int64
		costUSDMicro     int64
	)
	err := pool.QueryRow(ctx,
		`SELECT caller, model, input_tokens, output_tokens,
		        cache_read_tokens, cache_write_tokens, cost_usd_micro
		   FROM ai_cost_ledger
		  WHERE workspace_id = $1
		  ORDER BY created_at DESC
		  LIMIT 1`,
		wsID,
	).Scan(&caller, &model, &inputTokens, &outputTokens,
		&cacheReadTokens, &cacheWriteTokens, &costUSDMicro)
	if err != nil {
		t.Fatalf("query ledger row: %v", err)
	}
	if caller != "test.caller" {
		t.Errorf("caller = %q; want %q", caller, "test.caller")
	}
	if model != "claude-haiku-4-5" {
		t.Errorf("model = %q; want %q", model, "claude-haiku-4-5")
	}
	if inputTokens != 1_000_000 {
		t.Errorf("input_tokens = %d; want %d", inputTokens, 1_000_000)
	}
	if outputTokens != 1_000_000 {
		t.Errorf("output_tokens = %d; want %d", outputTokens, 1_000_000)
	}
	if cacheReadTokens != 500 {
		t.Errorf("cache_read_tokens = %d; want %d", cacheReadTokens, 500)
	}
	if cacheWriteTokens != 100 {
		t.Errorf("cache_write_tokens = %d; want %d", cacheWriteTokens, 100)
	}
	// Expected cost: haiku 1M in @0.25 + 1M out @1.25 = 1.5 USD = 1_500_000 micro
	wantCost := int64(1_500_000)
	if costUSDMicro != wantCost {
		t.Errorf("cost_usd_micro = %d; want %d", costUSDMicro, wantCost)
	}
}

// TestPgRecorder_Record_NilWorkspace verifies that nil workspace_id is handled.
func TestPgRecorder_Record_NilWorkspace(t *testing.T) {
	pool := openAICostTestPool(t)
	ctx := context.Background()

	rec := NewPgRecorder(pool)
	// Should not panic or error on nil workspace.
	rec.Record(ctx, nil, RecordParams{
		Caller:       "test.nil-workspace",
		Model:        "claude-sonnet-4-6",
		InputTokens:  100,
		OutputTokens: 50,
	})

	var count int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ai_cost_ledger WHERE caller = 'test.nil-workspace'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row for nil-workspace caller, got %d", count)
	}
}

// TestPgRecorder_Record_UnknownModel verifies that unknown model inserts a row
// with cost_usd_micro=0 (does not fail the write).
func TestPgRecorder_Record_UnknownModel(t *testing.T) {
	pool := openAICostTestPool(t)
	ctx := context.Background()
	wsID := uuid.New()

	rec := NewPgRecorder(pool)
	rec.Record(ctx, &wsID, RecordParams{
		Caller:       "test.unknown-model",
		Model:        "claude-unknown-99",
		InputTokens:  500,
		OutputTokens: 200,
	})

	var cost int64
	err := pool.QueryRow(ctx,
		`SELECT cost_usd_micro FROM ai_cost_ledger
		  WHERE workspace_id = $1 AND model = 'claude-unknown-99'
		  ORDER BY created_at DESC LIMIT 1`,
		wsID,
	).Scan(&cost)
	if err != nil {
		t.Fatalf("query cost: %v", err)
	}
	if cost != 0 {
		t.Errorf("unknown model cost_usd_micro = %d; want 0", cost)
	}
}
