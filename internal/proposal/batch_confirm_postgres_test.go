package proposal_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// skipMigrations are .up.sql files that MUST NOT be applied by the proposal
// test runner — they contain psql metacommands that pgx cannot parse.
var batchSkipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true, // psql `\set` metacommand
}

// openBatchTestPgPool starts a throwaway Postgres container with full
// migrations applied and returns a pgxpool ready for use. The container is
// terminated and the pool is closed via t.Cleanup.
//
// Skip with -short flag: requires Docker and adds ~5-10 s.
func openBatchTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}

	ctx := context.Background()
	container, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_batch_test"),
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

	applyBatchUpMigrations(t, ctx, pool)
	return pool
}

func applyBatchUpMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
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
		if batchSkipMigrations[name] {
			t.Logf("applyBatchUpMigrations: skipping %s (psql-metacommand-only file)", name)
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

// TestBatchConfirm_PG_AllAccepted verifies that when all IDs are valid and
// pending the Postgres transaction commits and all entries are accepted.
func TestBatchConfirm_PG_AllAccepted(t *testing.T) {
	pool := openBatchTestPgPool(t)
	store := proposal.NewStore(pool, nil)
	ctx := context.Background()

	ids := make([]uuid.UUID, 3)
	for i := range ids {
		p, err := store.Create(ctx, proposal.CreateParams{
			Type:       proposal.TypeGoal,
			Payload:    []byte(`{"title":"pg-batch-test"}`),
			ProposedBy: "test",
		})
		if err != nil {
			t.Fatalf("Create[%d]: %v", i, err)
		}
		ids[i] = p.ID
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID)
		})
	}

	result, err := store.BatchConfirm(ctx, ids, proposal.StatusAccepted)
	if err != nil {
		t.Fatalf("BatchConfirm: %v", err)
	}
	if result.Accepted != 3 || result.Failed != 0 {
		t.Errorf("expected 3 accepted 0 failed, got %+v", result)
	}
	for i, r := range result.Results {
		if !r.OK {
			t.Errorf("result[%d] should be OK, got %+v", i, r)
		}
	}

	// none remain pending
	pending, err := store.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	for _, p := range pending {
		for _, id := range ids {
			if p.ID == id {
				t.Errorf("proposal %s should not remain pending after batch accept", id)
			}
		}
	}
}

// TestBatchConfirm_PG_SingleNotFoundRollsBackAll verifies that when one ID is
// not found the Postgres transaction rolls back and ALL entries are reported
// as failed (full-rollback atomic guarantee).
func TestBatchConfirm_PG_SingleNotFoundRollsBackAll(t *testing.T) {
	pool := openBatchTestPgPool(t)
	store := proposal.NewStore(pool, nil)
	ctx := context.Background()

	good, err := store.Create(ctx, proposal.CreateParams{
		Type:    proposal.TypeTask,
		Payload: []byte(`{"title":"rollback-test"}`),
	})
	if err != nil {
		t.Fatalf("Create good: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", good.ID)
	})

	missingID := uuid.New()
	ids := []uuid.UUID{good.ID, missingID}

	result, err := store.BatchConfirm(ctx, ids, proposal.StatusRejected)
	if err != nil {
		t.Fatalf("BatchConfirm returned unexpected error: %v", err)
	}
	// all 2 IDs should be failed because the transaction was rolled back
	if result.Failed != 2 || result.Accepted != 0 {
		t.Errorf("expected 2 failed 0 accepted (rollback), got %+v", result)
	}

	// good proposal must still be pending (rolled back)
	got, err := store.Get(ctx, good.ID)
	if err != nil {
		t.Fatalf("Get after rollback: %v", err)
	}
	if got.Status != string(proposal.StatusPending) {
		t.Errorf("good proposal should still be pending after rollback, got status=%s", got.Status)
	}
}

// TestBatchConfirm_PG_CrossWorkspaceBlocked verifies that workspace B cannot
// resolve a proposal that belongs to workspace A.
func TestBatchConfirm_PG_CrossWorkspaceBlocked(t *testing.T) {
	pool := openBatchTestPgPool(t)
	ctx := context.Background()

	wsA := uuid.New()
	storeA := proposal.NewStore(pool, &wsA)

	p, err := storeA.Create(ctx, proposal.CreateParams{
		Type:    proposal.TypeConcept,
		Payload: []byte(`{"title":"workspace-a-proposal"}`),
	})
	if err != nil {
		t.Fatalf("Create in workspace A: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID)
	})

	wsB := uuid.New()
	storeB := proposal.NewStore(pool, &wsB)

	result, err := storeB.BatchConfirm(ctx, []uuid.UUID{p.ID}, proposal.StatusAccepted)
	if err != nil {
		t.Fatalf("BatchConfirm from workspace B: %v", err)
	}
	if result.Accepted != 0 || result.Failed != 1 {
		t.Errorf("workspace B should not resolve workspace A proposal; got accepted=%d failed=%d", result.Accepted, result.Failed)
	}

	// proposal must still be pending in workspace A
	got, err := storeA.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get from workspace A after cross-workspace attempt: %v", err)
	}
	if got.Status != string(proposal.StatusPending) {
		t.Errorf("proposal should still be pending in workspace A, got status=%s", got.Status)
	}
}

// TestBatchConfirm_PG_InvalidStatus verifies that a non-accept/reject status
// returns an error before any DB operation.
func TestBatchConfirm_PG_InvalidStatus(t *testing.T) {
	pool := openBatchTestPgPool(t)
	store := proposal.NewStore(pool, nil)
	ctx := context.Background()

	_, err := store.BatchConfirm(ctx, []uuid.UUID{uuid.New()}, proposal.StatusPending)
	if err == nil {
		t.Fatal("expected error for invalid status, got nil")
	}
}

// TestBatchConfirm_PG_AlreadyResolved verifies that attempting to batch-confirm
// an already-resolved proposal rolls back the whole transaction (Postgres).
func TestBatchConfirm_PG_AlreadyResolved(t *testing.T) {
	pool := openBatchTestPgPool(t)
	store := proposal.NewStore(pool, nil)
	ctx := context.Background()

	p, err := store.Create(ctx, proposal.CreateParams{
		Type:    proposal.TypeConcept,
		Payload: []byte(`{"title":"already-done"}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID)
	})

	// resolve manually first
	if _, err := store.Resolve(ctx, p.ID, proposal.StatusAccepted); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// batch confirm again — should fail
	result, err := store.BatchConfirm(ctx, []uuid.UUID{p.ID}, proposal.StatusAccepted)
	if err != nil {
		t.Fatalf("BatchConfirm returned unexpected error: %v", err)
	}
	if result.Failed != 1 || result.Accepted != 0 {
		t.Errorf("expected 1 failed, got %+v", result)
	}
}
