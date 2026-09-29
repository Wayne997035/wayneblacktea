package decision_test

import (
	"context"
	"testing"

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
	conformance.RunDecisionSmoke(t, store, "postgres")
}

// decisionParitySmokeTemplateKey is this file's own sqlitetemplate registry
// key (F0925-05) — distinct per-package, see gtd/parity_smoke_test.go's
// equivalent constant for the full rationale.
const decisionParitySmokeTemplateKey = "internal/decision/parity_smoke_test"

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
	conformance.RunDecisionSmoke(t, store, "sqlite")
}
