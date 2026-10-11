package sqlite

import (
	"context"
	"database/sql"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"testing"

	sqlitemigrations "github.com/Wayne997035/wayneblacktea/migrations/sqlite"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "modernc.org/sqlite"
)

// [F1011-01] SQLite half of the 000088 coverage — see
// migrations/sqlite/000088_task_area_changye.up.sql. The Postgres half is
// internal/storage/migration_000088_task_area_changye_integration_test.go.
// package sqlite (not sqlite_test): mirrors migration_000087_index_test.go.

// openMigratorAt87 mirrors openMigratorAt86: steps to the version
// immediately before the migration under test.
func openMigratorAt87(t *testing.T) (*sql.DB, *migrate.Migrate) {
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
	if err := m.Migrate(87); err != nil {
		t.Fatalf("migrate to version 87: %v", err)
	}
	return conn, m
}

// taskAreaRow is one task_areas row as the tests compare it.
type taskAreaRow struct {
	Area      string
	Label     string
	SortOrder int
	Archived  int
}

const changyeSlug = "changye"

// wantChangyeAreaRow is the exact row 000088 must add.
var wantChangyeAreaRow = taskAreaRow{Area: changyeSlug, Label: "Changye Nightmart", SortOrder: 60, Archived: 0}

// readTaskAreas returns every task_areas row ordered by sort_order, area. It
// closes its cursor before returning so the next statement on the in-memory
// connection never races an open one.
func readTaskAreas(t *testing.T, conn *sql.DB) []taskAreaRow {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(),
		`SELECT area, label, sort_order, archived FROM task_areas ORDER BY sort_order, area`)
	if err != nil {
		t.Fatalf("query task_areas: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []taskAreaRow
	for rows.Next() {
		var r taskAreaRow
		if err := rows.Scan(&r.Area, &r.Label, &r.SortOrder, &r.Archived); err != nil {
			t.Fatalf("scan task_areas row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate task_areas rows: %v", err)
	}
	return out
}

// withoutChangye returns rows minus the changye row, so "the other seven are
// untouched" is a direct comparison.
func withoutChangye(rows []taskAreaRow) []taskAreaRow {
	return slices.DeleteFunc(slices.Clone(rows), func(r taskAreaRow) bool { return r.Area == changyeSlug })
}

// TestMigration000088_ChangyeAreaSeeded proves 000088 is what adds the row:
// absent (7 rows) at v87, exactly one correct row (8 rows) at v88, and the
// seven 000079 rows are identical before and after.
func TestMigration000088_ChangyeAreaSeeded(t *testing.T) {
	t.Parallel() // [F1011-01]
	conn, m := openMigratorAt87(t)

	before := readTaskAreas(t, conn)
	if len(before) != 7 {
		t.Fatalf("task_areas has %d rows at v87, want the 7 seeded by 000079: %+v", len(before), before)
	}
	if got := withoutChangye(before); len(got) != len(before) {
		t.Fatalf("changye already present at v87, before 000088 ran: %+v", before)
	}

	if err := m.Migrate(88); err != nil {
		t.Fatalf("migrate to 88: %v", err)
	}
	after := readTaskAreas(t, conn)
	if len(after) != 8 {
		t.Fatalf("task_areas has %d rows at v88, want 8: %+v", len(after), after)
	}
	var changye []taskAreaRow
	for _, r := range after {
		if r.Area == changyeSlug {
			changye = append(changye, r)
		}
	}
	if !slices.Equal(changye, []taskAreaRow{wantChangyeAreaRow}) {
		t.Errorf("changye rows = %+v, want exactly [%+v]", changye, wantChangyeAreaRow)
	}
	if !slices.Equal(withoutChangye(after), before) {
		t.Errorf("the 000079 rows changed across 000088:\n before: %+v\n after:  %+v", before, withoutChangye(after))
	}
}

// TestMigration000088_DownKeepsTasksAndReupRestoresRow proves the down
// migration removes only the area row: a task already carrying
// area = 'changye' survives untouched, the tasks.area column and the
// task_areas table stay, and re-applying 000088 restores the identical row
// with no data change.
func TestMigration000088_DownKeepsTasksAndReupRestoresRow(t *testing.T) {
	t.Parallel() // [F1011-01]
	ctx := context.Background()
	conn, m := openMigratorAt87(t)
	if err := m.Migrate(88); err != nil {
		t.Fatalf("migrate to 88: %v", err)
	}
	upRows := readTaskAreas(t, conn)
	// Positive control: without the changye row at v88 the down/re-up checks
	// below would pass vacuously.
	if len(upRows) != 8 || len(withoutChangye(upRows)) != 7 {
		t.Fatalf("task_areas at v88 = %+v, want the 7 old rows plus changye", upRows)
	}

	const taskID = "t-changye-probe"
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, priority, area) VALUES (?1, 'changye probe', 'pending', 3, 'changye')`,
		taskID); err != nil {
		t.Fatalf("seed task with area=changye: %v", err)
	}

	if err := m.Migrate(87); err != nil {
		t.Fatalf("migrate down to 87: %v", err)
	}
	down := readTaskAreas(t, conn)
	if len(down) != 7 || len(withoutChangye(down)) != 7 {
		t.Errorf("after down: %d rows (%+v), want the 7 pre-000088 rows and no changye", len(down), down)
	}
	if !slices.Equal(down, withoutChangye(upRows)) {
		t.Errorf("down touched rows other than changye:\n up (minus changye): %+v\n down: %+v", withoutChangye(upRows), down)
	}
	var area string
	if err := conn.QueryRowContext(ctx, `SELECT area FROM tasks WHERE id = ?1`, taskID).Scan(&area); err != nil {
		t.Fatalf("seeded task gone or unreadable after down: %v", err)
	}
	if area != changyeSlug {
		t.Errorf("seeded task area = %q after down, want it left as changye (no cascade, no rewrite)", area)
	}
	probe, err := conn.QueryContext(ctx, `SELECT area FROM tasks LIMIT 0`)
	if err != nil {
		t.Fatalf("tasks.area column unreadable after down: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("close tasks.area probe: %v", err)
	}

	if err := m.Migrate(88); err != nil {
		t.Fatalf("re-migrate to 88 after down: %v", err)
	}
	if reup := readTaskAreas(t, conn); !slices.Equal(reup, upRows) {
		t.Errorf("task_areas after re-up differs from the first up:\n first: %+v\n re-up: %+v", upRows, reup)
	}
}

// [F1011-02] latestSQLiteSchemaVersion is hand-bumped on every migration, and
// OpenReadOnly (readonly.go) rejects any DB whose version differs from it, so
// a stale constant breaks the SessionStart hook. This pins the relationship
// "constant == newest embedded up migration" without hard-coding 88; the
// absolute value is pinned by m.Migrate(88) in the tests above.
func TestLatestSQLiteSchemaVersion_MatchesNewestEmbeddedMigration(t *testing.T) {
	t.Parallel()
	newest := newestEmbeddedSQLiteUpVersion(t)
	if latestSQLiteSchemaVersion != newest {
		t.Fatalf("latestSQLiteSchemaVersion = %d, but the newest embedded up migration is %06d: bump the constant "+
			"in migrate.go (and add its history comment) together with the new migration",
			latestSQLiteSchemaVersion, newest)
	}
}

// newestEmbeddedSQLiteUpVersion returns the highest NNNNNN prefix among the
// embedded *.up.sql files. A file that matches *.up.sql but has no numeric
// prefix fails the test rather than being skipped, and so does an empty set,
// so the helper cannot go vacuously quiet.
func newestEmbeddedSQLiteUpVersion(t *testing.T) int {
	t.Helper()
	entries, err := fs.ReadDir(sqlitemigrations.FS, ".")
	if err != nil {
		t.Fatalf("read embedded sqlite migrations: %v", err)
	}
	newest, seen := 0, 0
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".up.sql")
		if !ok {
			continue
		}
		prefix, _, _ := strings.Cut(name, "_")
		v, err := strconv.Atoi(prefix)
		if err != nil {
			t.Fatalf("embedded migration %q has no numeric prefix: %v", e.Name(), err)
		}
		seen++
		newest = max(newest, v)
	}
	if seen == 0 {
		t.Fatal("no *.up.sql found in the embedded sqlite migrations")
	}
	return newest
}
