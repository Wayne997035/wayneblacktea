package aicost

import (
	"context"
	"flag"
	"log"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

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

// newTestRecorder returns a pgRecorder whose writeSync is accessible from
// within this package (same package = white-box test). The returned recorder
// also has the semaphore wired so Record() is exercisable.
func newTestRecorder(pool *pgxpool.Pool) *pgRecorder {
	return &pgRecorder{
		pool: pool,
		sem:  make(chan struct{}, recordSemCap),
	}
}

// TestPgRecorder_Record_WritesRow verifies that pgRecorder inserts a row and
// that the inserted values match the RecordParams. Uses writeSync for
// deterministic row visibility (no race with the background goroutine).
func TestPgRecorder_Record_WritesRow(t *testing.T) {
	pool := openAICostTestPool(t)
	ctx := context.Background()
	wsID := uuid.New()

	rec := newTestRecorder(pool)
	p := RecordParams{
		Caller:           "test.caller",
		Model:            "claude-haiku-4-5",
		InputTokens:      1_000_000,
		OutputTokens:     1_000_000,
		CacheReadTokens:  500,
		CacheWriteTokens: 100,
	}
	if err := rec.writeSync(ctx, &wsID, p); err != nil {
		t.Fatalf("writeSync: %v", err)
	}

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

	rec := newTestRecorder(pool)
	// Should not panic or error on nil workspace.
	if err := rec.writeSync(ctx, nil, RecordParams{
		Caller:       "test.nil-workspace",
		Model:        "claude-sonnet-4-6",
		InputTokens:  100,
		OutputTokens: 50,
	}); err != nil {
		t.Fatalf("writeSync nil workspace: %v", err)
	}

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

	rec := newTestRecorder(pool)
	if err := rec.writeSync(ctx, &wsID, RecordParams{
		Caller:       "test.unknown-model",
		Model:        "claude-unknown-99",
		InputTokens:  500,
		OutputTokens: 200,
	}); err != nil {
		t.Fatalf("writeSync unknown model: %v", err)
	}

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

// ---------------------------------------------------------------------------
// FIX 2: PgStore.AICostLast30d tests
// ---------------------------------------------------------------------------

// seedAICostLast30dFixture inserts the standard set of rows for
// TestPgStore_AICostLast30d and returns the workspace ID used.
//
// Rows inserted:
//   - 2× haiku in wsID workspace (within 30d)
//   - 1× sonnet in wsID workspace (within 30d)
//   - 1× opus in oldWsID workspace (backdated 31 days — outside window)
func seedAICostLast30dFixture(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	wsID := uuid.New()
	rec := newTestRecorder(pool)

	// 2× haiku rows: cost each = 1M@0.25 + 1M@1.25 = 1_500_000 micro → total 3_000_000.
	for i := 0; i < 2; i++ {
		if err := rec.writeSync(ctx, &wsID, RecordParams{
			Caller:       "test.last30d.haiku",
			Model:        "claude-haiku-4-5",
			InputTokens:  1_000_000,
			OutputTokens: 1_000_000,
		}); err != nil {
			t.Fatalf("writeSync haiku row %d: %v", i, err)
		}
	}

	// 1× sonnet row: cost = 100k@3 + 10k@15 = 300_000 + 150_000 = 450_000 micro.
	if err := rec.writeSync(ctx, &wsID, RecordParams{
		Caller:       "test.last30d.sonnet",
		Model:        "claude-sonnet-4-6",
		InputTokens:  100_000,
		OutputTokens: 10_000,
	}); err != nil {
		t.Fatalf("writeSync sonnet row: %v", err)
	}

	// 1× opus row in a separate workspace, backdated outside the 30d window.
	oldWsID := uuid.New()
	if err := rec.writeSync(ctx, &oldWsID, RecordParams{
		Caller:       "test.last30d.old",
		Model:        "claude-opus-4-8",
		InputTokens:  1_000,
		OutputTokens: 500,
	}); err != nil {
		t.Fatalf("writeSync old row: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE ai_cost_ledger SET created_at = NOW() - INTERVAL '31 days'
		   WHERE workspace_id = $1 AND caller = 'test.last30d.old'`,
		oldWsID,
	); err != nil {
		t.Fatalf("backdate old row: %v", err)
	}
	return wsID
}

// assertAICostLast30dRows checks the expected ordering, aggregation, and
// micro-USD conversion for rows returned by AICostLast30d in the standard
// fixture (haiku × 2, sonnet × 1, opus outside window).
func assertAICostLast30dRows(t *testing.T, rows []CostRow, grandTotal int64) {
	t.Helper()
	const (
		wantHaikuMicro  = int64(3_000_000) // 2 rows × 1_500_000
		wantSonnetMicro = int64(450_000)   // 100k@3 + 10k@15
	)
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d; want 2 (got: %+v)", len(rows), rows)
	}

	// Rows ordered by total_cost_usd_micro DESC: haiku > sonnet.
	if rows[0].Model != "claude-haiku-4-5" {
		t.Errorf("rows[0].Model = %q; want claude-haiku-4-5", rows[0].Model)
	}
	if rows[1].Model != "claude-sonnet-4-6" {
		t.Errorf("rows[1].Model = %q; want claude-sonnet-4-6", rows[1].Model)
	}

	// Haiku aggregation.
	if rows[0].TotalCostUSD != float64(wantHaikuMicro)/1_000_000 {
		t.Errorf("haiku TotalCostUSD = %v; want %v", rows[0].TotalCostUSD, float64(wantHaikuMicro)/1_000_000)
	}
	if rows[0].InputTokens != 2_000_000 {
		t.Errorf("haiku InputTokens = %d; want 2_000_000", rows[0].InputTokens)
	}
	if rows[0].OutputTokens != 2_000_000 {
		t.Errorf("haiku OutputTokens = %d; want 2_000_000", rows[0].OutputTokens)
	}

	// Sonnet aggregation.
	if rows[1].TotalCostUSD != float64(wantSonnetMicro)/1_000_000 {
		t.Errorf("sonnet TotalCostUSD = %v; want %v", rows[1].TotalCostUSD, float64(wantSonnetMicro)/1_000_000)
	}
	if rows[1].InputTokens != 100_000 {
		t.Errorf("sonnet InputTokens = %d; want 100_000", rows[1].InputTokens)
	}
	if rows[1].OutputTokens != 10_000 {
		t.Errorf("sonnet OutputTokens = %d; want 10_000", rows[1].OutputTokens)
	}

	// Grand total.
	wantGrand := wantHaikuMicro + wantSonnetMicro
	if grandTotal != wantGrand {
		t.Errorf("grandTotal = %d; want %d", grandTotal, wantGrand)
	}
}

// TestPgStore_AICostLast30d verifies per-model aggregation, micro-USD→float64
// conversion, ordering by total cost DESC, and 30-day window filtering.
//
// Rows are inserted via writeSync (the synchronous path) so visibility is
// immediate — no race with background goroutines.
func TestPgStore_AICostLast30d(t *testing.T) {
	pool := openAICostTestPool(t)
	wsID := seedAICostLast30dFixture(t, pool)

	// Query via PgStore using wsID workspace — old-row workspace is excluded.
	store := NewPgStore(pool, &wsID)
	rows, grandTotal, err := store.AICostLast30d(context.Background())
	if err != nil {
		t.Fatalf("AICostLast30d: %v", err)
	}
	assertAICostLast30dRows(t, rows, grandTotal)
}

// TestPgStore_AICostLast30d_Empty verifies that an empty ledger returns nil
// rows, zero grand total, and no error.
func TestPgStore_AICostLast30d_Empty(t *testing.T) {
	pool := openAICostTestPool(t)
	ctx := context.Background()
	wsID := uuid.New() // fresh workspace — guaranteed empty

	store := NewPgStore(pool, &wsID)
	rows, grandTotal, err := store.AICostLast30d(ctx)
	if err != nil {
		t.Fatalf("AICostLast30d on empty workspace: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows for fresh workspace, got %d", len(rows))
	}
	if grandTotal != 0 {
		t.Errorf("expected grandTotal=0, got %d", grandTotal)
	}
}

// TestPgStore_AICostLast30d_OutsideWindowExcluded verifies that rows older
// than 30 days are excluded from the result even when the workspace matches.
func TestPgStore_AICostLast30d_OutsideWindowExcluded(t *testing.T) {
	pool := openAICostTestPool(t)
	ctx := context.Background()
	wsID := uuid.New()

	rec := newTestRecorder(pool)
	if err := rec.writeSync(ctx, &wsID, RecordParams{
		Caller:       "test.outside-window",
		Model:        "claude-haiku-4-5",
		InputTokens:  1_000_000,
		OutputTokens: 1_000_000,
	}); err != nil {
		t.Fatalf("writeSync: %v", err)
	}
	// Push the row outside the 30-day window.
	_, err := pool.Exec(ctx,
		`UPDATE ai_cost_ledger SET created_at = $1
		   WHERE workspace_id = $2 AND caller = 'test.outside-window'`,
		time.Now().AddDate(0, 0, -31),
		wsID,
	)
	if err != nil {
		t.Fatalf("backdate row: %v", err)
	}

	store := NewPgStore(pool, &wsID)
	rows, grandTotal, err := store.AICostLast30d(ctx)
	if err != nil {
		t.Fatalf("AICostLast30d: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows (all outside 30d), got %d", len(rows))
	}
	if grandTotal != 0 {
		t.Errorf("expected grandTotal=0, got %d", grandTotal)
	}
}

// ---------------------------------------------------------------------------
// ITEM 2: async-path and semaphore-full tests
// ---------------------------------------------------------------------------

// TestPgRecorder_Record_AsyncPath verifies that Record (the fire-and-forget
// goroutine path) eventually writes a ledger row. Because Record is
// asynchronous we poll until the row appears or a short deadline expires.
func TestPgRecorder_Record_AsyncPath(t *testing.T) {
	pool := openAICostTestPool(t)
	ctx := context.Background()
	wsID := uuid.New()

	// Use the public constructor so the production semaphore is wired.
	rec := NewPgRecorder(pool)
	p := RecordParams{
		Caller:       "test.async-path",
		Model:        "claude-sonnet-4-6",
		InputTokens:  50_000,
		OutputTokens: 5_000,
	}
	// Record is fire-and-forget: it returns before the row is visible.
	rec.Record(ctx, &wsID, p)

	// Poll for eventual consistency (goroutine writes within recordWriteTimeout).
	// 2 s is well inside the 5 s write timeout and avoids flaky test on slow CI.
	deadline := time.Now().Add(2 * time.Second)
	var count int
	for time.Now().Before(deadline) {
		err := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM ai_cost_ledger
			  WHERE workspace_id = $1 AND caller = 'test.async-path'`,
			wsID,
		).Scan(&count)
		if err != nil {
			t.Fatalf("count query: %v", err)
		}
		if count > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if count == 0 {
		t.Error("async Record did not write a row within 2 s")
	}
}

// TestPgRecorder_Record_SemaphoreFull verifies that when the semaphore is
// exhausted, Record drops the write (no row written, no panic).
func TestPgRecorder_Record_SemaphoreFull(t *testing.T) {
	pool := openAICostTestPool(t)
	ctx := context.Background()
	wsID := uuid.New()

	// Build a recorder and pre-fill the semaphore so every subsequent Record
	// hits the 'default' branch and is dropped.
	rec := newTestRecorder(pool)

	// Pre-fill the semaphore to capacity.
	for i := 0; i < recordSemCap; i++ {
		rec.sem <- struct{}{}
	}
	// Restore the semaphore after the test so deferred cleanup works.
	defer func() {
		for i := 0; i < recordSemCap; i++ {
			<-rec.sem
		}
	}()

	p := RecordParams{
		Caller:       "test.sem-full",
		Model:        "claude-haiku-4-5",
		InputTokens:  1_000,
		OutputTokens: 500,
	}
	// This call should silently drop (sem full → default branch) without panic.
	rec.Record(ctx, &wsID, p)

	// Give any erroneously-spawned goroutine a moment to write — there should
	// be none because the semaphore is full.
	time.Sleep(200 * time.Millisecond)

	var count int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM ai_cost_ledger
		  WHERE workspace_id = $1 AND caller = 'test.sem-full'`,
		wsID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Errorf("sem-full drop: expected 0 rows written, got %d", count)
	}
}
