package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/validator"
	sqlitemigrations "github.com/Wayne997035/wayneblacktea/migrations/sqlite"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "modernc.org/sqlite"
)

// [F0925-33][F0925-34] Migration 000084 clears repo_name values that violate
// validator.ValidRepoPath (internal/validator/repo_name.go:52) from the
// eight SQLite tables that carry the column — see
// migrations/sqlite/000084_repo_name_cleanup.up.sql for the full table list
// and predicate rationale.

// openMigratorAt83 mirrors openMigratorAt79 (deletion_tombstones_migration_test.go):
// a raw *sql.DB stepped to the version immediately before migration 000084,
// so DB.Open's full-chain apply (which would run 000084 too) never gets a
// chance to run before a test seeds pre-migration data.
func openMigratorAt83(t *testing.T) (*sql.DB, *migrate.Migrate) {
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
	if err := m.Migrate(83); err != nil {
		t.Fatalf("migrate to version 83: %v", err)
	}
	return conn, m
}

// execRepoRow runs an INSERT for one of the repo_name cleanup tests and
// fails the test on error.
func execRepoRow(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := conn.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("insert: %v (query=%s args=%v)", err, query, args)
	}
}

// readRepoName reads back repo_name for one row. table is always one of the
// eight literal names in cleanupTableSpecs below, never caller input.
func readRepoName(t *testing.T, conn *sql.DB, table, id string) sql.NullString {
	t.Helper()
	var v sql.NullString
	query := fmt.Sprintf(`SELECT repo_name FROM %s WHERE id = ?`, table) //nolint:gosec // table is a hardcoded literal from cleanupTableSpecs, never caller input
	if err := conn.QueryRowContext(context.Background(), query, id).Scan(&v); err != nil {
		t.Fatalf("read %s id=%s: %v", table, id, err)
	}
	return v
}

// cleanupTableSpec describes how to seed one of the eight repo_name-carrying
// tables with a minimal valid row so only repo_name varies. args binds id
// (always TEXT, including for discipline_events' INTEGER PK — modernc.org/
// sqlite applies column-affinity conversion to a TEXT-bound id against an
// INTEGER column on both insert and read, verified empirically) and
// repoName (a string sample, "" or nil for the NULL case).
type cleanupTableSpec struct {
	name    string
	notNull bool // true: cleanup SETs '' (work_sessions, procedural_memories); false: SETs NULL
	insert  string
	args    func(id string, repoName any) []any
}

var cleanupTableSpecs = []cleanupTableSpec{
	{
		name: "decisions", notNull: false,
		insert: `INSERT INTO decisions (id, repo_name, title, context, decision, rationale) VALUES (?, ?, 't', 'c', 'd', 'r')`,
		args:   func(id string, repoName any) []any { return []any{id, repoName} },
	},
	{
		name: "session_handoffs", notNull: false,
		insert: `INSERT INTO session_handoffs (id, repo_name, intent) VALUES (?, ?, 'i')`,
		args:   func(id string, repoName any) []any { return []any{id, repoName} },
	},
	{
		name: "vision_items", notNull: false,
		insert: `INSERT INTO vision_items (id, repo_name, title, why_blocked) VALUES (?, ?, 't', 'w')`,
		args:   func(id string, repoName any) []any { return []any{id, repoName} },
	},
	{
		name: "procedural_memories", notNull: true,
		insert: `INSERT INTO procedural_memories (id, repo_name, title) VALUES (?, ?, 't')`,
		args:   func(id string, repoName any) []any { return []any{id, repoName} },
	},
	{
		name: "discipline_events", notNull: false,
		insert: `INSERT INTO discipline_events (id, repo_name, session_id, tool_name) VALUES (?, ?, 's', 'tool')`,
		args:   func(id string, repoName any) []any { return []any{id, repoName} },
	},
	{
		name: "completion_candidates", notNull: false,
		// reason is unique per (task_id, reason) — id is globally unique in
		// this test, so binding it as reason too keeps every row distinct.
		insert: `INSERT INTO completion_candidates (id, repo_name, task_id, reason, confidence) VALUES (?, ?, 'task', ?, 'high')`,
		args:   func(id string, repoName any) []any { return []any{id, repoName, id} },
	},
	{
		name: "projects", notNull: false,
		// name is UNIQUE — id doubles as name for the same reason as above.
		insert: `INSERT INTO projects (id, repo_name, name, title) VALUES (?, ?, ?, 't')`,
		args:   func(id string, repoName any) []any { return []any{id, repoName, id} },
	},
	{
		name: "work_sessions", notNull: true,
		// status='planned' (not 'in_progress') so this general seeding never
		// exercises idx_work_sessions_one_active — that collision has its
		// own dedicated test below. workspace_id doubles as id so every row
		// is in its own workspace regardless.
		insert: `INSERT INTO work_sessions (id, repo_name, workspace_id, title, goal, status, source) VALUES (?, ?, ?, 't', 'g', 'planned', 'manual')`,
		args:   func(id string, repoName any) []any { return []any{id, repoName, id} },
	},
}

// repoNameCleanupSamples mirrors the accept/reject fixtures in
// internal/validator/repo_name_test.go (productionRepoNames, the accept
// list, and rejectedRepoPaths — kept in sync manually since those vars are
// unexported), minus the empty string which this test seeds separately per
// table. The oracle for each sample is validator.IsValidRepoName itself
// (called below), never a hand-written expected list.
var repoNameCleanupSamples = []string{
	// accept
	"_project", "owner/repo", "a.b/c_d-e", "a", "A1", "1234567890", "my_repo.name", "trailing.dot.",
	"chatbot", "chatbot-go", "chat-gateway", "chat-web", "cmoney-backend", "demo",
	"example-app-go-pgx", "Flare-Go/auth", "food-photo-log", "go_web", "intelligence-flow",
	"kdc-p2p-report/kdc-p2p-server-status-check", "neomart-api",
	"skcloud-admin-portal/skcloud-advertisement-image", "skcloud-uid-udid-server/skcloud-uid-udid",
	"wayneblacktea",
	// reject. "repo\x00" (rejectedRepoPaths' "null byte" case) is
	// deliberately excluded here — see
	// TestMigration000084_EmbeddedNULByteResidualRisk for why the GLOB
	// predicate cannot detect it and why that gap is accepted, not a bug.
	"../etc/passwd", "a/../b", "./a", ".x", "_project/.claude", "-x", "a/-b", "--help",
	"a//b", "/a", "a/", "a b", "a;b", "a\nb", "repo\n", "repo\t",
	"repo$(cmd)", "repo`cmd`", "中文", "bad repo name!",
	// length boundary (TestValidRepoPath_LengthBoundary)
	strings.Repeat("a", 100), strings.Repeat("a", 101),
}

// TestMigration000084_Consistency seeds every sample above — plus an empty
// string and, for nullable columns, NULL — into all eight SQLite tables at
// schema version 83, runs migration 000084, and asserts the cleared/
// unchanged outcome for every row matches validator.IsValidRepoName: the
// same oracle the write path enforces, not a hand-written expected list
// (decision 3be90339 / dispatch record F0925-34).
func TestMigration000084_Consistency(t *testing.T) {
	conn, m := openMigratorAt83(t)

	type seeded struct {
		table       string
		id          string
		input       string
		isNullInput bool
		notNull     bool
	}
	var rows []seeded

	// nextID is a single global counter shared by every table, not a
	// per-table "<table>-<i>" string: discipline_events.id is a real
	// INTEGER PRIMARY KEY (rowid alias), which SQLite only accepts a
	// genuine integer literal for — a non-numeric TEXT id (even one that
	// merely LOOKS like it could affinity-convert, e.g. a table-name
	// prefix) fails with "datatype mismatch" (verified empirically). Every
	// other table's id is TEXT and accepts a decimal-string id just as well.
	nextID := 0
	newID := func() string {
		id := strconv.Itoa(nextID)
		nextID++
		return id
	}

	for _, spec := range cleanupTableSpecs {
		for _, sample := range repoNameCleanupSamples {
			id := newID()
			execRepoRow(t, conn, spec.insert, spec.args(id, sample)...)
			rows = append(rows, seeded{table: spec.name, id: id, input: sample, notNull: spec.notNull})
		}

		emptyID := newID()
		execRepoRow(t, conn, spec.insert, spec.args(emptyID, "")...)
		rows = append(rows, seeded{table: spec.name, id: emptyID, input: "", notNull: spec.notNull})

		if !spec.notNull {
			nullID := newID()
			execRepoRow(t, conn, spec.insert, spec.args(nullID, nil)...)
			rows = append(rows, seeded{table: spec.name, id: nullID, isNullInput: true, notNull: spec.notNull})
		}
	}

	if err := m.Migrate(84); err != nil {
		t.Fatalf("migrate to 84: %v", err)
	}

	for _, r := range rows {
		got := readRepoName(t, conn, r.table, r.id)
		if r.isNullInput {
			if got.Valid {
				t.Errorf("%s id=%s: NULL input became %q, want still NULL", r.table, r.id, got.String)
			}
			continue
		}

		if validator.IsValidRepoName(r.input) {
			if !got.Valid || got.String != r.input {
				t.Errorf("%s id=%s: valid input %q was altered (valid=%v value=%q), want unchanged",
					r.table, r.id, r.input, got.Valid, got.String)
			}
			continue
		}

		// invalid and non-empty (empty is always "valid" per IsValidRepoName,
		// handled by the branch above) -> must be cleared.
		if r.notNull {
			if !got.Valid || got.String != "" {
				t.Errorf("%s id=%s: invalid input %q not cleared to '' (valid=%v value=%q)",
					r.table, r.id, r.input, got.Valid, got.String)
			}
		} else if got.Valid {
			t.Errorf("%s id=%s: invalid input %q not cleared to NULL (value=%q)", r.table, r.id, r.input, got.String)
		}
	}
}

// TestMigration000084_EmbeddedNULByteResidualRisk documents, rather than
// hides, a known gap: a repo_name containing an embedded NUL byte
// (validator's "null byte" reject case) is NOT cleared by this migration.
//
// Root cause (實測 against modernc.org/sqlite — the exact driver this repo
// uses, same package this test runs in): SQLite's length() and GLOB
// operate on TEXT values as if they were NUL-terminated C strings, even
// though the full byte sequence is stored and retrievable via a plain read.
// For "repo\x00" (5 bytes), length(v) reports 4 and every GLOB pattern in
// this migration's predicate — including '*[^A-Za-z0-9._/-]*', which
// correctly flags every other disallowed byte this file tests — evaluates
// against the truncated 4-byte view and never sees the trailing NUL, so it
// returns false. length(CAST(v AS BLOB)) does report the full 5 bytes, but
// that comparison also fires for ordinary multi-byte UTF-8 text (e.g.
// 'café': char length 4, blob length 5) which the char-class GLOB already
// catches on its own, so it is not a usable NUL-specific signal without a
// separate carve-out for non-ASCII — out of scope for this migration's
// locked predicate (fix record 修法2).
//
// This is the exact residual class the dispatch record's acceptance
// self-check already names and accepts: "殘留...只剩「樣本沒涵蓋的字元型態」
// 的殘留" — a byte type (embedded NUL) the sample set's other cases don't
// exercise. Not fixed here because: (a) the predicate is locked spec,
// reviewed across 3 spec-verifier rounds; (b) the only rows this can affect
// pre-date REPONAME-01, which has blocked new NUL-byte writes since it
// shipped; (c) a workaround would need a non-ASCII carve-out that changes
// the predicate's shape, which is a larger change than a residual-risk note
// warrants.
func TestMigration000084_EmbeddedNULByteResidualRisk(t *testing.T) {
	conn, m := openMigratorAt83(t)

	const nulRepoName = "repo\x00"
	if validator.IsValidRepoName(nulRepoName) {
		t.Fatal("precondition failed: validator.IsValidRepoName should reject an embedded NUL byte")
	}

	execRepoRow(t, conn,
		`INSERT INTO decisions (id, repo_name, title, context, decision, rationale) VALUES (?, ?, 't','c','d','r')`,
		"nul-residual-1", nulRepoName)

	if err := m.Migrate(84); err != nil {
		t.Fatalf("migrate to 84: %v", err)
	}

	got := readRepoName(t, conn, "decisions", "nul-residual-1")
	if !got.Valid || got.String != nulRepoName {
		t.Fatalf("expected the documented residual gap (embedded-NUL repo_name left uncleared) to still hold, "+
			"got valid=%v value=%q — if this now passes, the predicate started catching this case and this test "+
			"(and its comment) should be deleted, not adjusted",
			got.Valid, got.String)
	}
}

// TestMigration000084_UpDownUp proves down (a documented no-op) and a
// second up don't error. down does NOT attempt to restore cleared values
// (decision 3be90339: no backup, not recoverable) — it only lets
// golang-migrate step schema_migrations back to 83 so a later up can re-run
// against already-cleaned data (every UPDATE then matches zero rows).
func TestMigration000084_UpDownUp(t *testing.T) {
	conn, m := openMigratorAt83(t)
	execRepoRow(t, conn,
		`INSERT INTO decisions (id, repo_name, title, context, decision, rationale) VALUES (?, ?, 't','c','d','r')`,
		"updownup-1", "../bad")

	if err := m.Migrate(84); err != nil {
		t.Fatalf("migrate up to 84: %v", err)
	}
	if got := readRepoName(t, conn, "decisions", "updownup-1"); got.Valid {
		t.Fatalf("precondition: expected repo_name cleared to NULL after first up, got %q", got.String)
	}

	if err := m.Migrate(83); err != nil {
		t.Fatalf("migrate down to 83: %v", err)
	}
	if got := readRepoName(t, conn, "decisions", "updownup-1"); got.Valid {
		t.Errorf("down migration unexpectedly restored a value: %q (down.sql is a documented no-op)", got.String)
	}

	if err := m.Migrate(84); err != nil {
		t.Fatalf("migrate up to 84 a second time: %v", err)
	}
}

// TestMigration000084_WorkSessionsUniqueCollision pins the accepted failure
// mode (decision 3be90339): two in_progress work_sessions rows in the same
// workspace that both have an invalid repo_name collide on
// idx_work_sessions_one_active once both are cleared to ”, and migration
// 000084 MUST fail loudly — not silently de-duplicate or leave one row
// unclean. work_sessions is deliberately the last UPDATE in the migration
// file, so this also proves the whole-file transaction rolled back: an
// earlier UPDATE (decisions) in the same run must be undone too.
func TestMigration000084_WorkSessionsUniqueCollision(t *testing.T) {
	conn, m := openMigratorAt83(t)

	execRepoRow(t, conn,
		`INSERT INTO decisions (id, repo_name, title, context, decision, rationale) VALUES (?, ?, 't','c','d','r')`,
		"collision-decision", "../bad")
	execRepoRow(t, conn,
		`INSERT INTO work_sessions (id, repo_name, workspace_id, title, goal, status, source) VALUES (?, ?, 'ws-collision', 't', 'g', 'in_progress', 'manual')`,
		"collision-ws-1", "../bad1")
	execRepoRow(t, conn,
		`INSERT INTO work_sessions (id, repo_name, workspace_id, title, goal, status, source) VALUES (?, ?, 'ws-collision', 't', 'g', 'in_progress', 'manual')`,
		"collision-ws-2", "../bad2")

	err := m.Migrate(84)
	if err == nil {
		t.Fatal("expected migration 000084 to fail on the work_sessions UNIQUE collision, got nil error")
	}
	t.Logf("migration failed as expected: %v", err)

	version, dirty, verErr := m.Version()
	if verErr != nil {
		t.Fatalf("m.Version(): %v", verErr)
	}
	t.Logf("post-failure m.Version() = (%d, dirty=%v)", version, dirty)
	if !dirty {
		t.Errorf("m.Version() dirty=false after a failed migration, want true")
	}

	// Unlike Postgres, SQLite does not auto-abort a transaction on a
	// mid-statement constraint violation: the migration file's own BEGIN
	// TRANSACTION is left open on the connection that ran it, and that same
	// connection still sees its own uncommitted writes (verified
	// empirically — a raw read here before this ROLLBACK shows the
	// decisions row already cleared, even though the file never reached
	// COMMIT). Production never observes this half-applied state: db.go's
	// Open() always closes the connection on a migration failure
	// (closeAbandonedOpen, db.go:93-96), which discards the pending
	// transaction — confirmed separately against a file-backed DB, where
	// reopening after Close() showed the earlier decisions UPDATE was NOT
	// persisted. :memory: has nothing to reopen, so this test reproduces
	// that same discard explicitly instead.
	if _, rbErr := conn.ExecContext(context.Background(), `ROLLBACK;`); rbErr != nil {
		t.Fatalf("rollback dangling transaction: %v", rbErr)
	}

	got := readRepoName(t, conn, "decisions", "collision-decision")
	if !got.Valid || got.String != "../bad" {
		t.Errorf("decisions row was not rolled back: valid=%v value=%q, want unchanged %q", got.Valid, got.String, "../bad")
	}
}
