package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestAICostLedgerPrunerAdapter_DeletesOnlyExpiredRows exercises the
// parameterized-cutoff DELETE introduced by NewAICostLedgerPrunerAdapter
// against a real Postgres (backend-security-design.md §6.5 — any code path
// touching PG gets a testcontainers test by default, unconditionally). Seeds
// rows inside and outside the 30-day retention window and asserts only the
// expired ones are removed.
func TestAICostLedgerPrunerAdapter_DeletesOnlyExpiredRows(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()

	type seed struct {
		id         uuid.UUID
		createdAt  time.Time
		shouldKeep bool
		label      string
	}
	now := time.Now().UTC()
	seeds := []seed{
		{uuid.New(), now.AddDate(0, 0, -40), false, "40 days old (>30d, expired)"},
		{uuid.New(), now.AddDate(0, 0, -31), false, "31 days old (>30d, expired)"},
		{uuid.New(), now.AddDate(0, 0, -29), true, "29 days old (<30d, kept)"},
		{uuid.New(), now.AddDate(0, 0, -1), true, "1 day old (<30d, kept)"},
	}

	for _, s := range seeds {
		_, err := pool.Exec(ctx, `INSERT INTO ai_cost_ledger
			(id, caller, model, input_tokens, output_tokens, created_at)
			VALUES ($1, 'test-caller', 'test-model', 10, 5, $2)`,
			s.id, s.createdAt)
		if err != nil {
			t.Fatalf("seed %s: %v", s.label, err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, "DELETE FROM ai_cost_ledger WHERE id = $1", s.id)
		})
	}

	spec := PrunerSpec{
		Name:      "ai_cost_ledger",
		Store:     NewAICostLedgerPrunerAdapter(pool),
		Retention: 30 * 24 * time.Hour,
	}
	runPrune(spec)

	for _, s := range seeds {
		var count int
		err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM ai_cost_ledger WHERE id = $1", s.id).Scan(&count)
		if err != nil {
			t.Fatalf("count %s: %v", s.label, err)
		}
		if s.shouldKeep && count != 1 {
			t.Errorf("%s: count = %d, want 1 (should be kept)", s.label, count)
		}
		if !s.shouldKeep && count != 0 {
			t.Errorf("%s: count = %d, want 0 (should be deleted)", s.label, count)
		}
	}
}
