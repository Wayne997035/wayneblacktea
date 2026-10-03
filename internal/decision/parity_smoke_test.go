package decision_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/testutil/conformance"
	"github.com/Wayne997035/wayneblacktea/internal/testutil/sqlitetemplate"
	"github.com/google/uuid"
)

// TestDecisionConformance_Postgres runs conformance.RunDecisionSmoke against
// the real Postgres backend (F0929-73). Skips under -short (openTestPgPool,
// pg_test_main_test.go).
func TestDecisionConformance_Postgres(t *testing.T) {
	pool := openTestPgPool(t)
	ws := uuid.New()
	store := decision.NewStore(pool, &ws)
	conformance.RunDecisionSmoke(t, store, "postgres", func(t *testing.T, id uuid.UUID, at time.Time) {
		t.Helper()
		if _, err := pool.Exec(context.Background(), "UPDATE decisions SET created_at = $1 WHERE id = $2", at, id); err != nil {
			t.Fatalf("backdate %s: %v", id, err)
		}
	})
}

// decisionParitySmokeTemplateKey is this file's own sqlitetemplate registry
// key (F0925-05) — distinct per-package, see gtd/parity_smoke_test.go's
// equivalent constant for the full rationale.
const decisionParitySmokeTemplateKey = "internal/decision/parity_smoke_test"

// decisionParitySmokeBackdateLayout mirrors the unexported sqliteMillisLayout
// used by internal/storage/sqlite/decision.go (same duplication pattern as
// internal/storage/sqlite/decision_list_test.go's sqliteBackdateLayout —
// this file lives in package decision_test, not sqlite_test, so it can't
// import the sqlite package's unexported constant).
const decisionParitySmokeBackdateLayout = "2006-01-02T15:04:05.000Z"

func decisionParitySmokeMigrator(ctx context.Context, path string) error {
	d, err := sqlite.Open(ctx, path, "")
	if err != nil {
		return err
	}
	return d.Close()
}

// TestDecisionConformance_SQLite runs conformance.RunDecisionSmoke against
// the SQLite backend. Always runs (no -short gate, no Docker needed).
func TestDecisionConformance_SQLite(t *testing.T) {
	ctx := context.Background()
	ws := uuid.New()
	path := sqlitetemplate.Path(t, decisionParitySmokeTemplateKey, decisionParitySmokeMigrator, "decision-parity.db")

	d, err := sqlite.Open(ctx, path, ws.String())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	store := sqlite.NewDecisionStore(d)
	conformance.RunDecisionSmoke(t, store, "sqlite", func(t *testing.T, id uuid.UUID, at time.Time) {
		t.Helper()
		q := "UPDATE decisions SET created_at = ?1 WHERE id = ?2"
		if err := d.ExecContext(context.Background(), q, at.UTC().Format(decisionParitySmokeBackdateLayout), id.String()); err != nil {
			t.Fatalf("backdate %s: %v", id, err)
		}
	})
}
