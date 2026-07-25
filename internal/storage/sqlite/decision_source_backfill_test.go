package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	sqlitemigrations "github.com/Wayne997035/wayneblacktea/migrations/sqlite"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"

	_ "modernc.org/sqlite"
)

// openMigratorAt72 returns a raw *sql.DB migrated up to schema version 72
// (the SQLite twin schema immediately before migration 000073 adds
// decisions.source) plus the *migrate.Migrate handle so the test can step to
// 73 after seeding legacy fixture rows. Mirrors buildPreExistingDB's
// low-level sql.Open + migrate wiring (migrate_test.go) instead of DB.Open,
// which applies the FULL migration set (including 000073) before the test
// gets a chance to insert pre-073 rows. Unlike the Postgres twin, SQLite's
// `ALTER TABLE ADD COLUMN` has no `IF NOT EXISTS` form, so re-executing
// 000073's up.sql body after it already ran would error on "duplicate
// column name" — version-stepping (not re-exec) is required here.
func openMigratorAt72(t *testing.T) (*sql.DB, *migrate.Migrate) {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	src, err := iofs.New(sqlitemigrations.FS, ".")
	if err != nil {
		t.Fatalf("load embedded sqlite migrations: %v", err)
	}
	driver, err := migratesqlite.WithInstance(conn, &migratesqlite.Config{NoTxWrap: true})
	if err != nil {
		t.Fatalf("init sqlite migrate driver: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		t.Fatalf("init migrate instance: %v", err)
	}
	if err := m.Migrate(72); err != nil {
		t.Fatalf("migrate to version 72: %v", err)
	}
	return conn, m
}

// preCutoffCreatedAt is a fixed timestamp before migration 000073's
// created_at backfill cutoff (2026-07-25T07:00:00.000Z, see the up.sql
// comment) — legacy fixture rows use this so the backfill predicates match
// regardless of what wall-clock time `go test` actually runs at (security
// review round 2, M-1).
const preCutoffCreatedAt = "2026-07-20T00:00:00.000Z"

// postCutoffCreatedAt is a fixed timestamp AFTER the same cutoff, used by
// the boundary test case to prove created_at is actually enforced.
const postCutoffCreatedAt = "2026-07-25T08:00:00.000Z"

// insertLegacyDecision inserts a pre-P3.0a-shaped decisions row (no source
// column yet at schema version 72) directly via the columns migration
// 000073 backfills against: title/context/decision/rationale/created_at.
func insertLegacyDecision(t *testing.T, conn *sql.DB, title, decCtx, rationale, createdAt string) string {
	t.Helper()
	id := uuid.New().String()
	_, err := conn.ExecContext(
		context.Background(),
		`INSERT INTO decisions (id, title, context, decision, rationale, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, title, decCtx, "some decision text", rationale, createdAt,
	)
	if err != nil {
		t.Fatalf("insert legacy decision %q: %v", title, err)
	}
	return id
}

// TestMigration000073_Backfill_SQLite mirrors the Postgres backfill fixture
// coverage (internal/decision's testcontainers test) on the real SQLite twin
// — backend-security-design.md §6.5 dual-backend parity requires the same
// predicate logic to be independently verified on both engines, not asserted
// "identical SQL shape so one suffices". Exact legacy positives (byte-for-
// byte match) backfill to 'auto'; case-variant, extra-whitespace, and
// unrelated rows stay 'manual'. NULL context/rationale is not separately
// covered — both columns are NOT NULL as of the originating migration
// 000012_sqlite_baseline, so a NULL near-miss is structurally unreachable.
// All fixture rows are backdated to preCutoffCreatedAt so they fall inside
// the backfill's created_at cutoff regardless of wall-clock test time,
// EXCEPT the dedicated boundary case below (security review round 2, M-1).
func TestMigration000073_Backfill_SQLite(t *testing.T) {
	ctx := context.Background()
	conn, m := openMigratorAt72(t)

	cases := []struct {
		name      string
		context   string
		rationale string
		createdAt string
		wantAuto  bool
	}{
		{"predicate1 exact match", "any context", "implicitly decided via tool invocation", preCutoffCreatedAt, true},
		{"predicate1 case-variant stays manual", "any context", "Implicitly decided via tool invocation", preCutoffCreatedAt, false},
		{"predicate1 trailing space stays manual", "any context", "implicitly decided via tool invocation ", preCutoffCreatedAt, false},
		{"predicate2 exact match", "auto-extracted from session transcript", "implicitly decided during session", preCutoffCreatedAt, true},
		{"predicate2 wrong rationale stays manual", "auto-extracted from session transcript", "some other rationale", preCutoffCreatedAt, false},
		{"predicate3 exact match", "auto-proposed by trigger_tool=confirm_proposal session=abc123", "whatever", preCutoffCreatedAt, true},
		{
			"predicate3 missing session marker stays manual", "auto-proposed by trigger_tool=confirm_proposal",
			"whatever", preCutoffCreatedAt, false,
		},
		{"predicate3 case-variant stays manual", "Auto-proposed by trigger_tool=x session=y", "whatever", preCutoffCreatedAt, false},
		{"unrelated legacy row stays manual", "some manual context", "some manual rationale", preCutoffCreatedAt, false},
		// M-1 boundary: an adversarially-crafted row whose context matches
		// predicate 3 byte-for-byte, but whose created_at is AFTER the
		// cutoff (i.e. it's a row written after P3.0a shipped, simulating a
		// caller who invoked log_decision with a predicate-shaped context)
		// MUST stay manual — the cutoff, not just the text match, gates
		// reclassification.
		{
			"predicate3 match but created_at after cutoff stays manual",
			"auto-proposed by trigger_tool=confirm_proposal session=abc123",
			"whatever", postCutoffCreatedAt, false,
		},
	}

	ids := make(map[string]string, len(cases))
	for _, tc := range cases {
		ids[tc.name] = insertLegacyDecision(t, conn, "backfill: "+tc.name, tc.context, tc.rationale, tc.createdAt)
	}

	// Step exactly one migration: 000073_decision_source (adds the column and
	// runs the 3 backfill predicates against the rows seeded above).
	if err := m.Steps(1); err != nil {
		t.Fatalf("apply migration 000073: %v", err)
	}

	for _, tc := range cases {
		var source string
		if err := conn.QueryRowContext(ctx, `SELECT source FROM decisions WHERE id = ?`, ids[tc.name]).Scan(&source); err != nil {
			t.Fatalf("%s: read source: %v", tc.name, err)
		}
		want := "manual"
		if tc.wantAuto {
			want = "auto"
		}
		if source != want {
			t.Errorf("%s: source = %q, want %q", tc.name, source, want)
		}
	}
}

// TestMigration000073_InvalidSourceWritesZeroRows_SQLite verifies the
// application-layer guard: DecisionStore.Log rejects an invalid Source
// before ever reaching the DB, so the CHECK constraint is defence-in-depth,
// not the primary enforcement point.
func TestMigration000073_InvalidSourceWritesZeroRows_SQLite(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = d.Close() }()

	s := NewDecisionStore(d)
	_, logErr := s.Log(ctx, decision.LogParams{
		Title:     "bad-source",
		Context:   "ctx",
		Decision:  "dec",
		Rationale: "rat",
		Source:    decision.Source("system"), // not manual/auto
	})
	if logErr == nil {
		t.Fatal("expected error for invalid source, got nil")
	}
	if !errors.Is(logErr, decision.ErrInvalidSource) {
		t.Errorf("expected ErrInvalidSource, got %v", logErr)
	}

	rows, err := s.All(ctx, 10)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected zero rows written for invalid source, got %d", len(rows))
	}
}
