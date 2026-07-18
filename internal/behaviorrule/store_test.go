//go:build integration

package behaviorrule_test

import (
	"context"
	"flag"
	"log"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
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
	c, err := tcpostgres.Run(
		ctx,
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

// TestStore_Propose verifies that Propose creates a row with status='proposed'.
func TestStore_Propose(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := behaviorrule.New(pool, &wsID)
	ctx := context.Background()

	srcID := uuid.New()
	r, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "when task is long overdue",
		Action:      "ping user with a reminder",
		SourceType:  "manual",
		SourceID:    &srcID,
		Confidence:  0.70,
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if r.Status != "proposed" {
		t.Errorf("expected status 'proposed', got %q", r.Status)
	}
	if r.Condition != "when task is long overdue" {
		t.Errorf("unexpected condition: %q", r.Condition)
	}
	if r.SourceType != "manual" {
		t.Errorf("unexpected source_type: %q", r.SourceType)
	}
	if r.Confidence != 0.70 {
		t.Errorf("expected confidence 0.70, got %v", r.Confidence)
	}
	if r.SourceID == nil || *r.SourceID != srcID {
		t.Errorf("unexpected source_id: %v", r.SourceID)
	}
}

// TestStore_ApplyOutcome_SuccessOnProposed verifies that applying 'success' to
// a proposed rule transitions status to 'active' AND increments confidence.
func TestStore_ApplyOutcome_SuccessOnProposed(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := behaviorrule.New(pool, &wsID)
	ctx := context.Background()

	r, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "condition A",
		Action:      "action A",
		SourceType:  "reflection",
		Confidence:  0.50,
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	updated, err := store.ApplyOutcome(ctx, r.ID, "success")
	if err != nil {
		t.Fatalf("ApplyOutcome success: %v", err)
	}
	if updated.Status != "active" {
		t.Errorf("expected status 'active' after success on proposed, got %q", updated.Status)
	}
	// 0.50 + 0.05 = 0.55
	if updated.Confidence < 0.549 || updated.Confidence > 0.551 {
		t.Errorf("expected confidence ~0.55, got %v", updated.Confidence)
	}
}

// TestStore_ApplyOutcome_SuccessOnActive verifies that applying 'success' to
// an active rule updates confidence only (no status change).
func TestStore_ApplyOutcome_SuccessOnActive(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := behaviorrule.New(pool, &wsID)
	ctx := context.Background()

	r, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "condition B",
		Action:      "action B",
		SourceType:  "outcome",
		Confidence:  0.60,
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	// Promote to active first
	active, err := store.ApplyOutcome(ctx, r.ID, "success")
	if err != nil {
		t.Fatalf("ApplyOutcome first success: %v", err)
	}
	if active.Status != "active" {
		t.Fatalf("expected active status, got %q", active.Status)
	}

	// Second success: confidence should increase, status stays active
	updated, err := store.ApplyOutcome(ctx, r.ID, "success")
	if err != nil {
		t.Fatalf("ApplyOutcome second success: %v", err)
	}
	if updated.Status != "active" {
		t.Errorf("status changed unexpectedly: got %q", updated.Status)
	}
	// 0.60 + 0.05 + 0.05 = 0.70
	if updated.Confidence < 0.699 || updated.Confidence > 0.701 {
		t.Errorf("expected confidence ~0.70, got %v", updated.Confidence)
	}
}

// TestStore_ApplyOutcome_Failure verifies that applying 'failure' decrements
// confidence without changing status.
func TestStore_ApplyOutcome_Failure(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := behaviorrule.New(pool, &wsID)
	ctx := context.Background()

	r, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "condition C",
		Action:      "action C",
		SourceType:  "manual",
		Confidence:  0.50,
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	updated, err := store.ApplyOutcome(ctx, r.ID, "failure")
	if err != nil {
		t.Fatalf("ApplyOutcome failure: %v", err)
	}
	if updated.Status != "proposed" {
		t.Errorf("status should not change on failure; got %q", updated.Status)
	}
	// 0.50 - 0.10 = 0.40
	if updated.Confidence < 0.399 || updated.Confidence > 0.401 {
		t.Errorf("expected confidence ~0.40, got %v", updated.Confidence)
	}
}

// TestStore_ApplyOutcome_ConfidenceCap verifies confidence never exceeds 1.00.
func TestStore_ApplyOutcome_ConfidenceCap(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := behaviorrule.New(pool, &wsID)
	ctx := context.Background()

	r, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "condition cap",
		Action:      "action cap",
		SourceType:  "manual",
		Confidence:  0.98,
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	updated, err := store.ApplyOutcome(ctx, r.ID, "success")
	if err != nil {
		t.Fatalf("ApplyOutcome: %v", err)
	}
	if updated.Confidence > 1.00 {
		t.Errorf("confidence exceeded 1.00: got %v", updated.Confidence)
	}
}

// TestStore_ApplyOutcome_ConfidenceFloor verifies confidence never goes below 0.00.
func TestStore_ApplyOutcome_ConfidenceFloor(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := behaviorrule.New(pool, &wsID)
	ctx := context.Background()

	r, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "condition floor",
		Action:      "action floor",
		SourceType:  "manual",
		Confidence:  0.05,
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	updated, err := store.ApplyOutcome(ctx, r.ID, "failure")
	if err != nil {
		t.Fatalf("ApplyOutcome: %v", err)
	}
	if updated.Confidence < 0.00 {
		t.Errorf("confidence went below 0.00: got %v", updated.Confidence)
	}
}

// TestStore_Deprecate verifies Deprecate sets status='deprecated'.
func TestStore_Deprecate(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := behaviorrule.New(pool, &wsID)
	ctx := context.Background()

	r, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "condition dep",
		Action:      "action dep",
		SourceType:  "manual",
	})
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	updated, err := store.Deprecate(ctx, r.ID)
	if err != nil {
		t.Fatalf("Deprecate: %v", err)
	}
	if updated.Status != "deprecated" {
		t.Errorf("expected status 'deprecated', got %q", updated.Status)
	}

	// Idempotent: second Deprecate should not error
	_, err = store.Deprecate(ctx, r.ID)
	if err != nil {
		t.Errorf("second Deprecate returned unexpected error: %v", err)
	}
}

// TestStore_PruneOlderThan verifies TTL pruning respects status filter.
func TestStore_PruneOlderThan(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := behaviorrule.New(pool, &wsID)
	ctx := context.Background()

	// Propose and immediately deprecate one rule
	dep, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "deprecated rule",
		Action:      "old action",
		SourceType:  "manual",
	})
	if err != nil {
		t.Fatalf("Propose deprecated: %v", err)
	}
	if _, err := store.Deprecate(ctx, dep.ID); err != nil {
		t.Fatalf("Deprecate: %v", err)
	}

	// Propose and promote one rule to active
	active, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "active rule",
		Action:      "live action",
		SourceType:  "manual",
	})
	if err != nil {
		t.Fatalf("Propose active: %v", err)
	}
	if _, err := store.ApplyOutcome(ctx, active.ID, "success"); err != nil {
		t.Fatalf("promote to active: %v", err)
	}

	// Propose one rule that stays proposed
	proposed, err := store.Propose(ctx, behaviorrule.CreateParams{
		WorkspaceID: &wsID,
		Condition:   "proposed rule",
		Action:      "pending action",
		SourceType:  "manual",
	})
	if err != nil {
		t.Fatalf("Propose proposed: %v", err)
	}

	// Prune with a future cutoff — all deprecated rows are older than future
	cutoff := time.Now().Add(1 * time.Minute)
	n, err := store.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if n < 1 {
		t.Errorf("expected at least 1 row deleted, got %d", n)
	}

	// Verify survivorship via List (no dedicated GetByID — removed as dead code).
	remaining, err := store.List(ctx, behaviorrule.ListParams{WorkspaceID: &wsID, Limit: 20})
	if err != nil {
		t.Fatalf("List after prune: %v", err)
	}
	remainingIDs := make(map[uuid.UUID]bool, len(remaining))
	for _, r := range remaining {
		remainingIDs[r.ID] = true
	}
	if remainingIDs[dep.ID] {
		t.Error("deprecated row should have been pruned but still exists")
	}
	if !remainingIDs[active.ID] {
		t.Error("active row was unexpectedly pruned")
	}
	if !remainingIDs[proposed.ID] {
		t.Error("proposed row was unexpectedly pruned")
	}
}
