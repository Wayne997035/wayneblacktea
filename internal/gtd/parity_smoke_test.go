package gtd_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/testutil/conformance"
	"github.com/Wayne997035/wayneblacktea/internal/testutil/sqlitetemplate"
	"github.com/google/uuid"
)

// TestGTDConformance_Postgres runs conformance.RunGTDSmoke against the real
// Postgres backend, closing the F0929-73 parity gap: 0 shared behaviour
// assertions ran against both backends before this ticket. Skips under
// -short (openTestPgPool, store_postgres_test.go).
func TestGTDConformance_Postgres(t *testing.T) {
	pool := openTestPgPool(t)
	wsA := uuid.New()
	wsB := uuid.New()
	storeA := gtd.NewStore(pool, &wsA)
	storeB := gtd.NewStore(pool, &wsB)
	conformance.RunGTDSmoke(t, storeA, storeB, "postgres")
}

// gtdParitySmokeTemplateKey is this file's own sqlitetemplate registry key
// (F0925-05) — distinct from internal/storage/sqlite's own "internal/storage/sqlite"
// key, since sqlitetemplate's contract requires one key map to exactly one
// Migrator for the process's lifetime and internal/gtd's test binary is a
// different process-lifetime scope than internal/storage/sqlite's.
const gtdParitySmokeTemplateKey = "internal/gtd/parity_smoke_test"

// gtdParitySmokeMigrator builds a fully-migrated SQLite database by running
// the real production Open/Close path once, then closing it so the template
// file is a plain, checkpointed copy source (same shape as
// internal/storage/sqlite/template_helper_test.go's templateMigrator).
func gtdParitySmokeMigrator(ctx context.Context, path string) error {
	d, err := sqlite.Open(ctx, path, "")
	if err != nil {
		return err
	}
	return d.Close()
}

// TestGTDConformance_SQLite runs conformance.RunGTDSmoke against the SQLite
// backend. Unlike the Postgres twin, this always runs (no -short gate) —
// SQLite needs no Docker.
func TestGTDConformance_SQLite(t *testing.T) {
	ctx := context.Background()
	path := sqlitetemplate.Path(t, gtdParitySmokeTemplateKey, gtdParitySmokeMigrator, "gtd-parity.db")

	wsA := uuid.New()
	wsB := uuid.New()

	dA, err := sqlite.Open(ctx, path, wsA.String())
	if err != nil {
		t.Fatalf("sqlite.Open (wsA): %v", err)
	}
	t.Cleanup(func() { _ = dA.Close() })

	dB, err := sqlite.Open(ctx, path, wsB.String())
	if err != nil {
		t.Fatalf("sqlite.Open (wsB): %v", err)
	}
	t.Cleanup(func() { _ = dB.Close() })

	storeA := sqlite.NewGTDStore(dA)
	storeB := sqlite.NewGTDStore(dB)
	conformance.RunGTDSmoke(t, storeA, storeB, "sqlite")
}
