package sqlite

import (
	"context"
	"database/sql"
	"testing"

	sqlitemigrations "github.com/Wayne997035/wayneblacktea/migrations/sqlite"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"

	_ "modernc.org/sqlite"
)

// [F170-21] Migration 000077 normalises goals.due_date / tasks.due_date to the
// fixed-width layout. Two properties matter and neither is provable by
// eyeballing the SQL:
//
//  1. The WHERE clause is NARROW — it must touch only rows whose stored text
//     is not already in the target layout, and must never overwrite a value it
//     cannot parse (strftime returns NULL for those, and a looser condition
//     would write that NULL straight into the column, destroying the value).
//  2. The down migration is a TRUE inverse. Normalisation is many-to-one
//     ("...00Z" and "...00.000Z" both map to "...00.000Z") and strftime('%f')
//     truncates sub-millisecond digits, so the original text cannot be
//     recomputed — it has to have been saved. The up migration saves it.

// openMigratorAt76 returns a raw *sql.DB migrated to schema version 76 (the
// state immediately before 000077) plus the migrate handle, so a test can seed
// pre-migration rows and then step to 77. Same low-level wiring, and the same
// reason for it, as openMigratorAt72 (decision_source_backfill_test.go):
// DB.Open would apply the FULL set including 000077 before the fixtures exist.
func openMigratorAt76(t *testing.T) (*sql.DB, *migrate.Migrate) {
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
	if err := m.Migrate(76); err != nil {
		t.Fatalf("migrate to version 76: %v", err)
	}
	return conn, m
}

// dueDateFixture is one pre-migration row plus what 000077 must do to it.
type dueDateFixture struct {
	name string
	// stored is the raw due_date TEXT written before the migration runs.
	// nil means SQL NULL.
	stored *string
	// wantAfterUp is the raw TEXT expected once 000077 has run.
	wantAfterUp *string
	// wantChanged says whether the migration should have touched this row at
	// all — which is what "only the stripped rows, never the correct ones"
	// actually means, and the thing a loose WHERE would get wrong.
	wantChanged bool
}

func strptr(s string) *string { return &s }

// dueDateFixtures covers every shape the two write paths and the wild can
// produce. The already-correct and unparseable rows are the important ones:
// they are what distinguishes a narrow condition from a loose one.
func dueDateFixtures() []dueDateFixture {
	return []dueDateFixture{
		{
			name:        "stripped by RFC3339Nano",
			stored:      strptr("2026-09-01T09:00:00Z"),
			wantAfterUp: strptr("2026-09-01T09:00:00.000Z"),
			wantChanged: true,
		},
		{
			name:        "already in the target layout",
			stored:      strptr("2026-09-01T09:00:00.000Z"),
			wantAfterUp: strptr("2026-09-01T09:00:00.000Z"),
			wantChanged: false,
		},
		{
			name:        "single fractional digit",
			stored:      strptr("2026-09-01T09:00:00.5Z"),
			wantAfterUp: strptr("2026-09-01T09:00:00.500Z"),
			wantChanged: true,
		},
		{
			name:        "sub-millisecond precision is truncated",
			stored:      strptr("2026-09-01T09:00:00.123456Z"),
			wantAfterUp: strptr("2026-09-01T09:00:00.123Z"),
			wantChanged: true,
		},
		{
			name:        "unparseable text is left alone, NOT nulled",
			stored:      strptr("not a timestamp"),
			wantAfterUp: strptr("not a timestamp"),
			wantChanged: false,
		},
		{
			name:        "NULL stays NULL",
			stored:      nil,
			wantAfterUp: nil,
			wantChanged: false,
		},
	}
}

func insertGoalRaw(t *testing.T, conn *sql.DB, id string, due *string) {
	t.Helper()
	_, err := conn.ExecContext(context.Background(),
		`INSERT INTO goals (id, title, status, due_date, created_at, updated_at)
		 VALUES (?, ?, 'active', ?, '2026-08-01T00:00:00.000Z', '2026-08-01T00:00:00.000Z')`,
		id, "goal "+id, due)
	if err != nil {
		t.Fatalf("insert goal %s: %v", id, err)
	}
}

func insertTaskRaw(t *testing.T, conn *sql.DB, id string, due *string) {
	t.Helper()
	_, err := conn.ExecContext(context.Background(),
		`INSERT INTO tasks (id, title, status, priority, due_date, created_at, updated_at)
		 VALUES (?, ?, 'pending', 3, ?, '2026-08-01T00:00:00.000Z', '2026-08-01T00:00:00.000Z')`,
		id, "task "+id, due)
	if err != nil {
		t.Fatalf("insert task %s: %v", id, err)
	}
}

func readDueDate(t *testing.T, conn *sql.DB, table, id string) *string {
	t.Helper()
	var got sql.NullString
	// Whitelist, not interpolation. An identifier cannot be bound as a parameter,
	// so the only way to keep this query free of concatenation is to enumerate the
	// tables the test actually uses. A new table added here fails loudly at
	// t.Fatalf rather than silently building SQL from whatever string arrived.
	var q string
	switch table {
	case "goals":
		q = `SELECT due_date FROM goals WHERE id = ?`
	case "tasks":
		q = `SELECT due_date FROM tasks WHERE id = ?`
	default:
		t.Fatalf("readDueDate: unsupported table %q — add it to the whitelist above", table)
	}
	err := conn.QueryRowContext(context.Background(), q, id).Scan(&got)
	if err != nil {
		t.Fatalf("read %s.due_date for %s: %v", table, id, err)
	}
	if !got.Valid {
		return nil
	}
	return &got.String
}

func eqPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func show(p *string) string {
	if p == nil {
		return "NULL"
	}
	return `"` + *p + `"`
}

// TestMigration000077_NormalisesOnlyStrippedRows is property (1): the narrow
// WHERE clause. Every fixture is checked for the value it should hold after
// the migration AND for whether it should have been touched at all.
func TestMigration000077_NormalisesOnlyStrippedRows(t *testing.T) {
	conn, m := openMigratorAt76(t)

	fixtures := dueDateFixtures()
	goalIDs := make([]string, len(fixtures))
	taskIDs := make([]string, len(fixtures))
	for i, f := range fixtures {
		goalIDs[i] = uuid.New().String()
		taskIDs[i] = uuid.New().String()
		insertGoalRaw(t, conn, goalIDs[i], f.stored)
		insertTaskRaw(t, conn, taskIDs[i], f.stored)
	}

	if err := m.Migrate(77); err != nil {
		t.Fatalf("migrate to 77: %v", err)
	}

	for i, f := range fixtures {
		t.Run("goals/"+f.name, func(t *testing.T) {
			got := readDueDate(t, conn, "goals", goalIDs[i])
			if !eqPtr(got, f.wantAfterUp) {
				t.Errorf("due_date = %s, want %s", show(got), show(f.wantAfterUp))
			}
		})
		t.Run("tasks/"+f.name, func(t *testing.T) {
			got := readDueDate(t, conn, "tasks", taskIDs[i])
			if !eqPtr(got, f.wantAfterUp) {
				t.Errorf("due_date = %s, want %s", show(got), show(f.wantAfterUp))
			}
		})
	}

	// The backup table is the migration's own record of which rows it decided
	// to touch, so asserting on it is a direct check of the WHERE clause
	// rather than an inference from the resulting values.
	for i, f := range fixtures {
		var backedUp int
		if err := conn.QueryRowContext(context.Background(),
			`SELECT count(*) FROM f170_21_due_date_backup WHERE table_name = 'goals' AND row_id = ?`,
			goalIDs[i]).Scan(&backedUp); err != nil {
			t.Fatalf("count backup rows: %v", err)
		}
		want := 0
		if f.wantChanged {
			want = 1
		}
		if backedUp != want {
			t.Errorf("%s: backup rows = %d, want %d — the WHERE clause touched the wrong set "+
				"(a row that was already correct must not be rewritten)", f.name, backedUp, want)
		}
	}
}

// TestMigration000077_DownRestoresExactOriginals is property (2): down is a
// true inverse, including for the sub-millisecond fixture whose original text
// is NOT recomputable from the normalised value.
func TestMigration000077_DownRestoresExactOriginals(t *testing.T) {
	conn, m := openMigratorAt76(t)

	fixtures := dueDateFixtures()
	goalIDs := make([]string, len(fixtures))
	taskIDs := make([]string, len(fixtures))
	for i, f := range fixtures {
		goalIDs[i] = uuid.New().String()
		taskIDs[i] = uuid.New().String()
		insertGoalRaw(t, conn, goalIDs[i], f.stored)
		insertTaskRaw(t, conn, taskIDs[i], f.stored)
	}

	if err := m.Migrate(77); err != nil {
		t.Fatalf("migrate to 77: %v", err)
	}
	if err := m.Migrate(76); err != nil {
		t.Fatalf("migrate down to 76: %v", err)
	}

	for i, f := range fixtures {
		if got := readDueDate(t, conn, "goals", goalIDs[i]); !eqPtr(got, f.stored) {
			t.Errorf("goals/%s: after down, due_date = %s, want the original %s",
				f.name, show(got), show(f.stored))
		}
		if got := readDueDate(t, conn, "tasks", taskIDs[i]); !eqPtr(got, f.stored) {
			t.Errorf("tasks/%s: after down, due_date = %s, want the original %s",
				f.name, show(got), show(f.stored))
		}
	}

	// down must also clean up after itself.
	var tableCount int
	if err := conn.QueryRowContext(
		context.Background(),
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'f170_21_due_date_backup'`,
	).Scan(&tableCount); err != nil {
		t.Fatalf("check backup table: %v", err)
	}
	if tableCount != 0 {
		t.Errorf("f170_21_due_date_backup still exists after down migration")
	}
}

// TestMigration000077_IsIdempotent guards the property the WHERE clause
// depends on: running the normalisation twice must be a no-op the second time.
// If it were not, the `due_date <> strftime(...)` condition could not be used
// to mean "not already normalised".
func TestMigration000077_IsIdempotent(t *testing.T) {
	conn, m := openMigratorAt76(t)

	id := uuid.New().String()
	insertGoalRaw(t, conn, id, strptr("2026-09-01T09:00:00Z"))

	if err := m.Migrate(77); err != nil {
		t.Fatalf("migrate to 77: %v", err)
	}
	first := readDueDate(t, conn, "goals", id)

	// Step back down and up again: the second up sees the already-normalised
	// value that the first up produced (down restored the original, so this
	// also re-exercises the full cycle).
	if err := m.Migrate(76); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if err := m.Migrate(77); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
	second := readDueDate(t, conn, "goals", id)

	if !eqPtr(first, second) {
		t.Errorf("re-running the migration changed the value again: %s then %s", show(first), show(second))
	}
	if want := strptr("2026-09-01T09:00:00.000Z"); !eqPtr(second, want) {
		t.Errorf("due_date = %s, want %s", show(second), show(want))
	}
}
