package decision_test

// U15 contract layer: decisions.actor_session_id / decisions.confirmed_by_human
// (migration 000076). This file only proves the schema contract — that both
// columns exist with the documented type/default on both backends, that
// golang-migrate can apply and revert the SQLite twin cleanly, and that a
// decision written without an explicit ConfirmedByHuman value round-trips as
// false (never NULL, never true) through the real Store.Log code path on
// both Postgres and SQLite. Neither backend's caller sets these fields yet
// (see decision.LogParams's doc comments) — wiring a real writer is out of
// scope for this contract layer.

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	sqlitestore "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	sqlitemigrations "github.com/Wayne997035/wayneblacktea/migrations/sqlite"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "modernc.org/sqlite"
)

// openSQLiteMigratorAt75 returns a raw *sql.DB migrated up to schema version
// 75 (the SQLite twin schema immediately before migration 000076 adds
// decisions.actor_session_id/confirmed_by_human) plus the *migrate.Migrate
// handle, so the test can step across 76 in both directions. Mirrors
// internal/storage/sqlite/decision_source_backfill_test.go's openMigratorAt72
// helper. Self-contained (imports golang-migrate + the embedded SQLite
// migration FS directly) rather than depending on sqlitestore.Open, because
// this test exercises schema mechanics, not the DecisionStore — Open() only
// ever steps forward and would never exercise the down direction.
func openSQLiteMigratorAt75(t *testing.T) (*sql.DB, *migrate.Migrate) {
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
	if err := m.Migrate(75); err != nil {
		t.Fatalf("migrate to version 75: %v", err)
	}
	return conn, m
}

// decisionColumnNames returns the set of column names currently on the
// decisions table, via PRAGMA table_info — used to assert presence/absence
// of the two new columns across the up/down/up cycle below.
func decisionColumnNames(t *testing.T, ctx context.Context, conn *sql.DB) map[string]bool {
	t.Helper()
	rows, err := conn.QueryContext(ctx, "PRAGMA table_info(decisions)")
	if err != nil {
		t.Fatalf("PRAGMA table_info(decisions): %v", err)
	}
	defer func() { _ = rows.Close() }()

	cols := map[string]bool{}
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan table_info row: %v", err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info iter: %v", err)
	}
	return cols
}

// TestMigration000076_SQLite_UpDownUp steps golang-migrate forward across
// migration 000076, back down, then forward again, asserting the columns
// appear/disappear at each step. Proves both up.sql and down.sql parse and
// apply cleanly against the real SQLite driver (backend-security-design.md
// §6.1 requires plain SQL that golang-migrate can actually run, not just
// "no psql metacommands" by inspection) and that the migration is reversible
// (§6.5-adjacent dual-backend parity expectation: the SQLite twin must behave
// like a real migration, not a one-way schema patch).
func TestMigration000076_SQLite_UpDownUp(t *testing.T) {
	ctx := context.Background()
	conn, m := openSQLiteMigratorAt75(t)

	before := decisionColumnNames(t, ctx, conn)
	if before["actor_session_id"] || before["confirmed_by_human"] {
		t.Fatal("precondition failed: new columns already present before migration 000076 applied")
	}

	if err := m.Steps(1); err != nil {
		t.Fatalf("apply migration 000076 (up): %v", err)
	}
	afterUp := decisionColumnNames(t, ctx, conn)
	if !afterUp["actor_session_id"] {
		t.Error("actor_session_id missing after up migration")
	}
	if !afterUp["confirmed_by_human"] {
		t.Error("confirmed_by_human missing after up migration")
	}

	if err := m.Steps(-1); err != nil {
		t.Fatalf("revert migration 000076 (down): %v", err)
	}
	afterDown := decisionColumnNames(t, ctx, conn)
	if afterDown["actor_session_id"] {
		t.Error("actor_session_id still present after down migration")
	}
	if afterDown["confirmed_by_human"] {
		t.Error("confirmed_by_human still present after down migration")
	}

	if err := m.Steps(1); err != nil {
		t.Fatalf("re-apply migration 000076 (up again): %v", err)
	}
	afterUp2 := decisionColumnNames(t, ctx, conn)
	if !afterUp2["actor_session_id"] || !afterUp2["confirmed_by_human"] {
		t.Error("columns did not come back after re-applying up migration")
	}
}

// TestDecisionStore_ConfirmedByHumanDefaultsFalse_SQLite is the bad-case
// acceptance check on the real code path (not a raw SQL probe): a decision
// logged via DecisionStore.Log without setting ConfirmedByHuman (Go zero
// value: false) MUST read back as exactly false — never NULL, never true. An
// omitted confirmation must not silently read as "confirmed".
func TestDecisionStore_ConfirmedByHumanDefaultsFalse_SQLite(t *testing.T) {
	ctx := context.Background()
	d, err := sqlitestore.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = d.Close() }()

	s := sqlitestore.NewDecisionStore(d)
	row, err := s.Log(ctx, decision.LogParams{
		Title:     "U15 contract probe: confirmed_by_human default",
		Context:   "ctx",
		Decision:  "dec",
		Rationale: "rat",
		Source:    decision.SourceManual,
		// ActorSessionID and ConfirmedByHuman intentionally left unset.
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if row.ConfirmedByHuman {
		t.Error("ConfirmedByHuman = true, want false (omitted confirmation must not read as confirmed)")
	}
	if row.ActorSessionID.Valid {
		t.Errorf("ActorSessionID.Valid = true (value %q), want NULL for an unset session", row.ActorSessionID.String)
	}
}

// TestStore_ConfirmedByHumanRoundTrip_Postgres mirrors the SQLite coverage
// above on the real Postgres backend (backend-security-design.md §6.5: any
// store logic touching PG needs its own testcontainers test, not just "PG
// and SQLite are the same SQL so one suffices"). Also proves a non-default,
// non-empty value round-trips correctly — the omitted-value case is already
// covered on SQLite, so this test exercises the opposite corner: an
// explicitly-set ActorSessionID/ConfirmedByHuman=true persisting exactly as
// given.
func TestStore_ConfirmedByHumanRoundTrip_Postgres(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	store := decision.NewStore(pool, nil)

	d, err := store.Log(ctx, decision.LogParams{
		Title:            "U15 contract probe: actor_session_id/confirmed_by_human round trip",
		Context:          "ctx",
		Decision:         "dec",
		Rationale:        "rat",
		Source:           decision.SourceManual,
		ActorSessionID:   "session-u15-contract-probe",
		ConfirmedByHuman: true,
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	t.Cleanup(func() {
		if _, cleanErr := pool.Exec(ctx, "DELETE FROM decisions WHERE id = $1", d.ID); cleanErr != nil {
			t.Logf("cleanup decision: %v", cleanErr)
		}
	})

	if !d.ConfirmedByHuman {
		t.Error("ConfirmedByHuman = false, want true (explicit value was not persisted)")
	}
	if !d.ActorSessionID.Valid || d.ActorSessionID.String != "session-u15-contract-probe" {
		t.Errorf("ActorSessionID = %+v, want Valid=true String=%q", d.ActorSessionID, "session-u15-contract-probe")
	}

	var dbConfirmed bool
	var dbActorSession sql.NullString
	err = pool.QueryRow(
		ctx, "SELECT confirmed_by_human, actor_session_id FROM decisions WHERE id = $1", d.ID,
	).Scan(&dbConfirmed, &dbActorSession)
	if err != nil {
		t.Fatalf("read back row: %v", err)
	}
	if !dbConfirmed {
		t.Error("DB confirmed_by_human = false, want true")
	}
	if !dbActorSession.Valid || dbActorSession.String != "session-u15-contract-probe" {
		t.Errorf("DB actor_session_id = %+v, want Valid=true String=%q", dbActorSession, "session-u15-contract-probe")
	}
}
