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

// [F1003-09] SQLite twin of
// internal/storage/migration_000087_index_integration_test.go — see
// migrations/sqlite/000087_pending_proposals_status_pending_sort_key.up.sql
// for the index shape and rationale. package sqlite (not sqlite_test):
// mirrors migration_000085_index_test.go's convention.

// openMigratorAt86 mirrors openMigratorAt85: steps to the version
// immediately before the migration under test.
func openMigratorAt86(t *testing.T) (*sql.DB, *migrate.Migrate) {
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
	if err := m.Migrate(86); err != nil {
		t.Fatalf("migrate to version 86: %v", err)
	}
	return conn, m
}

const (
	wantIdx000087SQLiteOld = "CREATE INDEX idx_pending_proposals_status_pending " +
		"ON pending_proposals(created_at DESC) WHERE status = 'pending'"
	wantIdx000087SQLiteNew = "CREATE INDEX idx_pending_proposals_status_pending " +
		"ON pending_proposals(created_at DESC,id DESC) WHERE status = 'pending'"
	// wantIdxWorkspacePendingSort is the net-new workspace-leading
	// composite — see the migration file's header comment for why.
	wantIdxWorkspacePendingSort = "CREATE INDEX idx_pending_proposals_workspace_pending_sort " +
		"ON pending_proposals(workspace_id,created_at DESC,id DESC) WHERE status = 'pending'"
)

// TestMigration000087_PendingProposalsSortKeyIndexExists proves migration
// 000087 realigns idx_pending_proposals_status_pending to the new column
// list, and that down restores the OLD shape (not absence — this index is
// a realignment of an existing object, not net-new): up to 87, down to 86
// (old shape restored), and back up to 87 (new shape restored).
func TestMigration000087_PendingProposalsSortKeyIndexExists(t *testing.T) {
	t.Parallel() // [F1003-09]
	conn, m := openMigratorAt86(t)

	got := sqliteIndexSQL(t, conn, "idx_pending_proposals_status_pending")
	if got != canonicalizeSQL(wantIdx000087SQLiteOld) {
		t.Fatalf("pre-087 index shape mismatch:\n got:  %s\n want: %s", got, canonicalizeSQL(wantIdx000087SQLiteOld))
	}

	if err := m.Migrate(87); err != nil {
		t.Fatalf("migrate to 87: %v", err)
	}
	got = sqliteIndexSQL(t, conn, "idx_pending_proposals_status_pending")
	if got != canonicalizeSQL(wantIdx000087SQLiteNew) {
		t.Errorf("post-087 index shape mismatch:\n got:  %s\n want: %s", got, canonicalizeSQL(wantIdx000087SQLiteNew))
	}

	if err := m.Migrate(86); err != nil {
		t.Fatalf("migrate down to 86: %v", err)
	}
	got = sqliteIndexSQL(t, conn, "idx_pending_proposals_status_pending")
	if got != canonicalizeSQL(wantIdx000087SQLiteOld) {
		t.Errorf("post-down index shape mismatch (want the OLD shape restored, not absence):\n got:  %s\n want: %s",
			got, canonicalizeSQL(wantIdx000087SQLiteOld))
	}

	if err := m.Migrate(87); err != nil {
		t.Fatalf("re-migrate to 87 after down: %v", err)
	}
	got = sqliteIndexSQL(t, conn, "idx_pending_proposals_status_pending")
	if got != canonicalizeSQL(wantIdx000087SQLiteNew) {
		t.Errorf("post-re-up index shape mismatch:\n got:  %s\n want: %s", got, canonicalizeSQL(wantIdx000087SQLiteNew))
	}
}

// TestMigration000087_WorkspacePendingSortIndexExists proves the net-new
// workspace-leading composite is absent pre-087, exists with the exact
// expected shape post-087, and is fully reversible (down drops it
// entirely — it is net-new, unlike the realigned
// idx_pending_proposals_status_pending above).
func TestMigration000087_WorkspacePendingSortIndexExists(t *testing.T) {
	t.Parallel() // [F1003-09]
	conn, m := openMigratorAt86(t)

	if got := sqliteIndexSQL(t, conn, "idx_pending_proposals_workspace_pending_sort"); got != "" {
		t.Fatalf("idx_pending_proposals_workspace_pending_sort exists before migration 000087 has run (got %q)", got)
	}

	if err := m.Migrate(87); err != nil {
		t.Fatalf("migrate to 87: %v", err)
	}
	got := sqliteIndexSQL(t, conn, "idx_pending_proposals_workspace_pending_sort")
	if got != canonicalizeSQL(wantIdxWorkspacePendingSort) {
		t.Errorf("index shape mismatch:\n got:  %s\n want: %s", got, canonicalizeSQL(wantIdxWorkspacePendingSort))
	}

	if err := m.Migrate(86); err != nil {
		t.Fatalf("migrate down to 86: %v", err)
	}
	if got := sqliteIndexSQL(t, conn, "idx_pending_proposals_workspace_pending_sort"); got != "" {
		t.Errorf("idx_pending_proposals_workspace_pending_sort still exists after the down migration (got %q)", got)
	}

	if err := m.Migrate(87); err != nil {
		t.Fatalf("re-migrate to 87 after down: %v", err)
	}
	got = sqliteIndexSQL(t, conn, "idx_pending_proposals_workspace_pending_sort")
	if got != canonicalizeSQL(wantIdxWorkspacePendingSort) {
		t.Errorf("index missing or wrong shape after re-up:\n got:  %s\n want: %s", got, canonicalizeSQL(wantIdxWorkspacePendingSort))
	}
}

// seedPendingProposalsForExplain inserts n pending proposals into the given
// workspace, mirroring the shape ListPendingPage filters/sorts on.
func seedPendingProposalsForExplain(t *testing.T, conn *sql.DB, workspaceID string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		id := workspaceID + "-pp-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		if _, err := conn.ExecContext(
			ctx,
			`INSERT INTO pending_proposals (id, workspace_id, type, payload, status) VALUES (?, ?, 'task', '{}', 'pending')`,
			id, workspaceID,
		); err != nil {
			t.Fatalf("seed pending_proposal %d: %v", i, err)
		}
	}
}

// explainPendingProposalsQueryPlan runs ListPendingPage's exact query shape
// (mirrored, since the production query is a function-local const — see
// internal/storage/sqlite/proposal.go's ListPendingPage) through EXPLAIN
// QUERY PLAN, binding workspaceArg either to a concrete value or nil.
func explainPendingProposalsQueryPlan(t *testing.T, conn *sql.DB, workspaceArg any) string {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), `EXPLAIN QUERY PLAN
		SELECT id FROM pending_proposals
		WHERE status = 'pending'
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY created_at DESC, id DESC
		LIMIT 20 OFFSET 0`, workspaceArg)
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
	return plan
}

// TestMigration000087_PendingProposalsSortKeyIndexUsedByQuery is the
// EXPLAIN QUERY PLAN proof that the realigned index is actually used, pre-
// and post-087. Unlike migration 000086's index, this index does NOT lead
// with workspace_id (per spec cd88edfd's Target behaviour — workspace_id's
// near-1 cardinality in this single-tenant app isn't worth leading the
// composite with), so the production (workspace-scoped) and legacy-NULL
// calls share the identical index-usage story here, no PG/SQLite
// divergence to document for the OR-wrapper reason discussed on the 000086
// test.
func TestMigration000087_PendingProposalsSortKeyIndexUsedByQuery(t *testing.T) {
	t.Parallel() // [F1003-09]
	conn, m := openMigratorAt86(t)
	seedPendingProposalsForExplain(t, conn, "ws-pp", 20)

	// The pre-087 index covers created_at DESC (the first ORDER BY term) but
	// not id DESC, so SQLite only needs a *partial* sort for the tiebreaker —
	// rendered as "USE TEMP B-TREE FOR LAST TERM OF ORDER BY", not the full
	// "... FOR ORDER BY" wording migration 000086's test sees (that query's
	// pre-fix index covered neither sort term). Match broadly on "B-TREE" so
	// this assertion isn't coupled to SQLite's exact wording for "some sort
	// node is present".
	preplan := explainPendingProposalsQueryPlan(t, conn, "ws-pp")
	t.Logf("pre-087 EXPLAIN QUERY PLAN:\n%s", preplan)
	if !strings.Contains(preplan, "B-TREE") {
		t.Fatalf("expected pre-087 plan to need a sort (old index has no id tiebreaker), got:\n%s", preplan)
	}

	if err := m.Migrate(87); err != nil {
		t.Fatalf("migrate to 87: %v", err)
	}
	postplan := explainPendingProposalsQueryPlan(t, conn, "ws-pp")
	t.Logf("post-087 EXPLAIN QUERY PLAN:\n%s", postplan)
	if !strings.Contains(postplan, "idx_pending_proposals_status_pending") {
		t.Fatalf("expected idx_pending_proposals_status_pending referenced, got:\n%s", postplan)
	}
	if strings.Contains(postplan, "B-TREE") {
		t.Fatalf("post-087 plan still needs a sort — the id DESC tiebreaker should have removed it entirely:\n%s", postplan)
	}
}
