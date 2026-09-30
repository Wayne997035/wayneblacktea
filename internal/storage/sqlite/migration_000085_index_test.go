package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	sqlitemigrations "github.com/Wayne997035/wayneblacktea/migrations/sqlite"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "modernc.org/sqlite"
)

// [F0930-06] SQLite twin of
// internal/storage/migration_000085_index_integration_test.go — see
// migrations/sqlite/000085_pending_proposals_source_entity_idx.up.sql for
// the index shape and rationale. package sqlite (not sqlite_test): this
// file needs no unexported access, but stays in-package to mirror
// query_indexes_migration_test.go's convention for this migration-DDL test
// family.

// openMigratorAt84 mirrors openMigratorAt80 (query_indexes_migration_test.go):
// a raw *sql.DB stepped to the version immediately before the migration
// under test, so DB.Open's full-chain apply (which would run 000085 too)
// never gets a chance to run before the test asserts the pre-migration
// state.
func openMigratorAt84(t *testing.T) (*sql.DB, *migrate.Migrate) {
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
	if err := m.Migrate(84); err != nil {
		t.Fatalf("migrate to version 84: %v", err)
	}
	return conn, m
}

// wantIdx000085SQLite is the expected full CREATE INDEX text (table, column
// order, the json_extract expression, and the WHERE type = 'task' partial
// predicate — see the migration file's header comment for why the SQLite
// twin is partial where the PG twin isn't), canonicalized the same way
// TestGoldenSchemaEquivalence compares INDEX statements (see
// canonicalizeSQL). Exact-text comparison, not just an existence check — a
// wrong column order, a dropped column, or a dropped WHERE clause would
// still pass a bare sqlite_master-name lookup.
const wantIdx000085SQLite = "CREATE INDEX idx_pending_proposals_type_proposer_source " +
	"ON pending_proposals(type, proposed_by, json_extract(payload, '$.source_entity_id')) " +
	"WHERE type = 'task'"

// TestMigration000085_ProposalDedupIndexExists proves migration 000085
// creates idx_pending_proposals_type_proposer_source in SQLite with the
// exact expected shape, and that the migration is reversible: up to 85,
// down to 84, and back up to 85 again without error (mirrors
// TestMigration000081_QueryIndexesExist).
func TestMigration000085_ProposalDedupIndexExists(t *testing.T) {
	t.Parallel() // [F0930-06]
	conn, m := openMigratorAt84(t)

	if got := sqliteIndexSQL(t, conn, "idx_pending_proposals_type_proposer_source"); got != "" {
		t.Fatalf("index exists before migration 000085 has run (got %q)", got)
	}

	if err := m.Migrate(85); err != nil {
		t.Fatalf("migrate to 85: %v", err)
	}
	got := sqliteIndexSQL(t, conn, "idx_pending_proposals_type_proposer_source")
	if got == "" {
		t.Fatal("index does not exist after migration 000085")
	}
	if got != canonicalizeSQL(wantIdx000085SQLite) {
		t.Errorf("index shape mismatch:\n got:  %s\n want: %s", got, canonicalizeSQL(wantIdx000085SQLite))
	}

	if err := m.Migrate(84); err != nil {
		t.Fatalf("migrate down to 84: %v", err)
	}
	if got := sqliteIndexSQL(t, conn, "idx_pending_proposals_type_proposer_source"); got != "" {
		t.Errorf("index still exists after the down migration (got %q)", got)
	}

	if err := m.Migrate(85); err != nil {
		t.Fatalf("re-migrate to 85 after down: %v", err)
	}
	got = sqliteIndexSQL(t, conn, "idx_pending_proposals_type_proposer_source")
	if got != canonicalizeSQL(wantIdx000085SQLite) {
		t.Errorf("index missing or wrong shape after re-up:\n got:  %s\n want: %s", got, canonicalizeSQL(wantIdx000085SQLite))
	}
}

// TestMigration000085_ProposalDedupIndexUsedByQuery is the EXPLAIN QUERY
// PLAN proof (sprint-0930 D10 ambiguity resolution, SQLite twin of
// internal/storage.TestMigration000085_ProposalDedupIndexUsedByQuery) that
// idx_pending_proposals_type_proposer_source is not just present but
// actually usable for the dedup predicate shape
// DecisionsPendingOutcomeReview's NOT EXISTS subquery filters on: type,
// proposed_by, AND the json_extract(payload, '$.source_entity_id')
// expression — all three MUST appear in the index seek condition, not
// pushed to a residual scan filter.
func TestMigration000085_ProposalDedupIndexUsedByQuery(t *testing.T) {
	t.Parallel() // [F0930-06]
	conn, m := openMigratorAt84(t)
	if err := m.Migrate(85); err != nil {
		t.Fatalf("migrate to 85: %v", err)
	}

	ctx := context.Background()
	if _, err := conn.ExecContext(
		ctx, `INSERT INTO pending_proposals
		(id, workspace_id, type, payload, status, proposed_by, created_at)
		VALUES ('probe-id', 'ws1', 'task', '{"source_entity_id":"probe"}', 'pending',
		        'scheduler:decision_outcome_review', strftime('%Y-%m-%dT%H:%M:%fZ','now'))`,
	); err != nil {
		t.Fatalf("seed pending_proposals row: %v", err)
	}

	// Isolated single-table form of DecisionsPendingOutcomeReview's dedup
	// NOT EXISTS predicate (internal/storage/sqlite/cognitive_jobs.go): same
	// three equality/expression conditions, with the correlated d.id
	// replaced by a bound parameter so the plan can be inspected directly.
	rows, err := conn.QueryContext(
		ctx, `EXPLAIN QUERY PLAN
		SELECT 1 FROM pending_proposals p
		WHERE p.type = 'task'
		  AND p.proposed_by = 'scheduler:decision_outcome_review'
		  AND json_extract(p.payload, '$.source_entity_id') = ?1`,
		"00000000-0000-0000-0000-000000000000",
	)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var plan string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan EXPLAIN QUERY PLAN row: %v", err)
		}
		plan += detail + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate EXPLAIN QUERY PLAN rows: %v", err)
	}
	t.Logf("EXPLAIN QUERY PLAN:\n%s", plan)

	if !strings.Contains(plan, "USING COVERING INDEX idx_pending_proposals_type_proposer_source") {
		t.Fatalf("expected a SEARCH using idx_pending_proposals_type_proposer_source, got:\n%s", plan)
	}
	for _, want := range []string{"type=?", "proposed_by=?", "<expr>=?"} {
		if !strings.Contains(plan, want) {
			t.Errorf("index seek condition missing %q — predicate fell back to a residual filter instead of the index seek.\nplan: %s", want, plan)
		}
	}
}
