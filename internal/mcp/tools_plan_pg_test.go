package mcp

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// mcpPlanTestPgPool is the package-level Postgres pool for confirm_plan's PG
// atomicity test (materializePlanPg). internal/mcp had no real-Postgres
// integration test before this file — this TestMain mirrors the pattern
// already established in internal/gtd/pg_test_main_test.go (same container
// image, same migration-skip list, same -short guard) rather than inventing
// a new one.
var mcpPlanTestPgPool *pgxpool.Pool

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(runMCPPlanPgTestMain(m))
}

func runMCPPlanPgTestMain(m *testing.M) int {
	if testing.Short() {
		return m.Run()
	}
	ctx := context.Background()
	// pgvector/pgvector:pg16 — same image internal/gtd's TestMain uses; the
	// knowledge migration does CREATE EXTENSION vector, so the vanilla
	// postgres:16-alpine image fails to apply migrations.
	c, err := tcpostgres.Run(
		ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_mcp_plan_test"),
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

	if err := applyAllUpMigrationsForMCPPlanTest(ctx, pool); err != nil {
		log.Printf("apply migrations: %v", err)
		return 1
	}

	mcpPlanTestPgPool = pool
	return m.Run()
}

// skipMCPPlanMigrations mirrors internal/gtd/pg_test_main_test.go's
// skipMigrations: 000011 uses a psql `\set` metacommand pgx cannot parse.
var skipMCPPlanMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true,
}

func applyAllUpMigrationsForMCPPlanTest(ctx context.Context, pool *pgxpool.Pool) error {
	entries, err := migrationfs.FS.ReadDir(".")
	if err != nil {
		return fmt.Errorf("read embedded migrations dir: %w", err)
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
		if skipMCPPlanMigrations[name] {
			continue
		}
		body, err := migrationfs.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}

// newPgPlanTestServer returns a minimal *Server wired with only the three
// fields materializePlanPg needs (pool, pgGTD, pgDecision) — not the full
// storage.NewServerStores bundle, which additionally requires a WORKSPACE_ID
// env var and an embedding client that confirm_plan's PG path never touches.
func newPgPlanTestServer(t *testing.T) (*Server, uuid.UUID) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	wsID := uuid.New()
	return &Server{
		pool:       mcpPlanTestPgPool,
		pgGTD:      gtd.NewStore(mcpPlanTestPgPool, &wsID),
		pgDecision: decision.NewStore(mcpPlanTestPgPool, &wsID),
	}, wsID
}

// TestHandleConfirmPlan_Postgres_AtomicRollbackOnMidPhaseFailure is the
// Postgres half of confirm_plan's atomicity guarantee: when a later phase
// fails, an EARLIER phase task created in the same call MUST NOT persist.
//
// NOTE on mutation testing: swapping this path's tx.Rollback(ctx) for
// tx.Commit(ctx) does NOT turn this specific test red, because a CHECK
// constraint violation (the trigger used here) aborts the Postgres
// transaction server-side — Postgres itself treats COMMIT on an aborted
// transaction as an implicit rollback, independent of what the Go code
// calls. TestHandleConfirmPlan_Postgres_AtomicRollbackAcrossTaskAndDecision
// below IS the test that mutation-self-certifies the explicit
// defer tx.Rollback(ctx) call, because its failure trigger
// (sanitize.ValidateNoTagNoise) is rejected in Go BEFORE any SQL statement
// runs, so Postgres never sees an error and never auto-aborts the tx — only
// the Go-level rollback call determines the outcome there.
func TestHandleConfirmPlan_Postgres_AtomicRollbackOnMidPhaseFailure(t *testing.T) {
	s, wsID := newPgPlanTestServer(t)
	ctx := context.Background()

	r := callConfirmPlan(t, s, map[string]any{
		"phases": `[
			{"title":"PG Atomic A","description":"A","priority":1},
			{"title":"PG Atomic B","description":"B","priority":99}
		]`,
	})
	if !r.IsError {
		t.Fatalf("expected error for out-of-range priority, got success: %s", resultText(r))
	}
	text := resultText(r)
	if strings.Contains(text, "PG Atomic A") {
		t.Errorf("rolled-back task must not be reported as created, got: %s", text)
	}

	var count int
	err := mcpPlanTestPgPool.QueryRow(
		ctx,
		`SELECT COUNT(*) FROM tasks WHERE title IN ('PG Atomic A','PG Atomic B') AND workspace_id = $1`,
		wsID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("querying tasks: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 tasks after rollback, got %d — transaction did not roll back", count)
	}
}

// TestHandleConfirmPlan_Postgres_AtomicRollbackAcrossTaskAndDecision proves
// the Postgres transaction spans BOTH loops: a task that succeeds is still
// rolled back when a LATER decision in the same call fails. sanitize.
// ValidateNoTagNoise (called by decision.Store.Log — see
// internal/decision/store.go) rejects tool-call serialization fragments,
// giving a real, confirm_plan-reachable per-decision failure trigger on the
// Postgres backend (SQLite's Log doesn't have this — see the SQLite cross
// -domain test's doc comment for why that one takes a different route).
func TestHandleConfirmPlan_Postgres_AtomicRollbackAcrossTaskAndDecision(t *testing.T) {
	s, wsID := newPgPlanTestServer(t)
	ctx := context.Background()

	r := callConfirmPlan(t, s, map[string]any{
		"phases":    `[{"title":"PG Atomic Cross A","description":"A","priority":1}]`,
		"decisions": `[{"title":"Cross Decision","decision":"Use X</decision>"}]`,
	})
	if !r.IsError {
		t.Fatalf("expected error from tag-noise-rejected decision, got success: %s", resultText(r))
	}
	text := resultText(r)
	if strings.Contains(text, "PG Atomic Cross A") {
		t.Errorf("task rolled back by the later decision failure must not be reported as created, got: %s", text)
	}

	var count int
	err := mcpPlanTestPgPool.QueryRow(
		ctx,
		`SELECT COUNT(*) FROM tasks WHERE title = 'PG Atomic Cross A' AND workspace_id = $1`,
		wsID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("querying tasks: %v", err)
	}
	if count != 0 {
		t.Errorf("expected task to be rolled back by the decision-loop failure, got %d rows", count)
	}
}
