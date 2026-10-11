//go:build integration

package storage

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [F1011-01] Postgres coverage for migration 000088 — see
// migrations/000088_task_area_changye.up.sql, and
// internal/storage/sqlite/migration_000088_task_area_changye_test.go for the
// SQLite twin. package storage (not storage_test), same build-tag
// `integration` file group as repo_name_cleanup_migration_integration_test.go
// — reuses its TestMain / testAdminDSN / newIsolatedTestDB /
// migrateToVersion; there is deliberately no second TestMain here.

// changyeAreaRow is one task_areas row as these tests compare it.
type changyeAreaRow struct {
	Area      string
	Label     string
	SortOrder int
	Archived  bool
}

const (
	changyeSlug       = "changye"
	changyeProbeTitle = "changye probe"
)

// wantChangyeAreaRowPG is the exact row 000088 must add.
var wantChangyeAreaRowPG = changyeAreaRow{Area: changyeSlug, Label: "Changye Nightmart", SortOrder: 60, Archived: false}

// readChangyeTaskAreas returns every task_areas row ordered by sort_order, area.
func readChangyeTaskAreas(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []changyeAreaRow {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT area, label, sort_order, archived FROM task_areas ORDER BY sort_order, area`)
	if err != nil {
		t.Fatalf("query task_areas: %v", err)
	}
	defer rows.Close()
	var out []changyeAreaRow
	for rows.Next() {
		var r changyeAreaRow
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

// withoutChangyeRow returns rows minus the changye row, so "the other seven
// are untouched" is a direct comparison.
func withoutChangyeRow(rows []changyeAreaRow) []changyeAreaRow {
	return slices.DeleteFunc(slices.Clone(rows), func(r changyeAreaRow) bool { return r.Area == changyeSlug })
}

// onlyChangyeRows returns just the changye rows.
func onlyChangyeRows(rows []changyeAreaRow) []changyeAreaRow {
	return slices.DeleteFunc(slices.Clone(rows), func(r changyeAreaRow) bool { return r.Area != changyeSlug })
}

// TestMigration000088_ChangyeAreaUpDownReup proves 000088 is what adds the
// row on Postgres: absent (7 rows) at v87, one correct row (8 rows) after
// up, absent again after down, back after re-up — with the seven 000079
// rows identical at every step.
func TestMigration000088_ChangyeAreaUpDownReup(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := context.Background()
	dsn := newIsolatedTestDB(t, testAdminDSN)
	migrateToVersion(t, dsn, 87)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	before := readChangyeTaskAreas(t, ctx, pool)
	if len(before) != 7 || len(onlyChangyeRows(before)) != 0 {
		t.Fatalf("pre-088: want the 7 rows 000079 seeded and no changye, got %d: %+v", len(before), before)
	}

	t.Setenv("WBT_AUTO_MIGRATE", "")
	if err := RunMigrations(ctx, dsn); err != nil {
		t.Fatalf("RunMigrations to 88: %v", err)
	}
	up := readChangyeTaskAreas(t, ctx, pool)
	if len(up) != 8 {
		t.Fatalf("post-088: want 8 rows, got %d: %+v", len(up), up)
	}
	if got := onlyChangyeRows(up); !slices.Equal(got, []changyeAreaRow{wantChangyeAreaRowPG}) {
		t.Errorf("post-088 changye rows = %+v, want exactly [%+v]", got, wantChangyeAreaRowPG)
	}
	if got := withoutChangyeRow(up); !slices.Equal(got, before) {
		t.Errorf("the 000079 rows changed across 000088:\n before: %+v\n after:  %+v", before, got)
	}

	migrateToVersion(t, dsn, 87)
	down := readChangyeTaskAreas(t, ctx, pool)
	if !slices.Equal(down, before) {
		t.Errorf("post-down task_areas differs from pre-088:\n pre-088: %+v\n down:    %+v", before, down)
	}

	if err := RunMigrations(ctx, dsn); err != nil {
		t.Fatalf("RunMigrations to 88 after down: %v", err)
	}
	if reup := readChangyeTaskAreas(t, ctx, pool); !slices.Equal(reup, up) {
		t.Errorf("post-re-up task_areas differs from the first up:\n first: %+v\n re-up: %+v", up, reup)
	}
}

// TestMigration000088_DownKeepsTasks proves the down migration removes only
// the area row: a task already carrying area = 'changye' survives with its
// area unchanged (no cascade, no rewrite to misc), and re-applying 000088
// leaves it counted again with no data change.
func TestMigration000088_DownKeepsTasks(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := context.Background()
	dsn := newIsolatedTestDB(t, testAdminDSN)
	migrateToVersion(t, dsn, 87)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	t.Setenv("WBT_AUTO_MIGRATE", "")
	if err := RunMigrations(ctx, dsn); err != nil {
		t.Fatalf("RunMigrations to 88: %v", err)
	}
	// Positive control: without the changye row at v88 the down/re-up checks
	// below would pass vacuously.
	if got := onlyChangyeRows(readChangyeTaskAreas(t, ctx, pool)); !slices.Equal(got, []changyeAreaRow{wantChangyeAreaRowPG}) {
		t.Fatalf("changye rows at v88 = %+v, want exactly [%+v]", got, wantChangyeAreaRowPG)
	}
	taskID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO tasks (id, title, area) VALUES ($1, $2, $3)`, taskID, changyeProbeTitle, changyeSlug); err != nil {
		t.Fatalf("seed task with area=changye: %v", err)
	}

	taskArea := func() (title, area string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT title, area FROM tasks WHERE id = $1`, taskID).Scan(&title, &area); err != nil {
			t.Fatalf("seeded task gone or unreadable: %v", err)
		}
		return title, area
	}

	migrateToVersion(t, dsn, 87)
	if got := onlyChangyeRows(readChangyeTaskAreas(t, ctx, pool)); len(got) != 0 {
		t.Errorf("changye row still present after down: %+v", got)
	}
	if title, area := taskArea(); title != changyeProbeTitle || area != changyeSlug {
		t.Errorf("seeded task = (%q, %q) after down, want (%q, %q) untouched", title, area, changyeProbeTitle, changyeSlug)
	}

	if err := RunMigrations(ctx, dsn); err != nil {
		t.Fatalf("RunMigrations to 88 after down: %v", err)
	}
	if got := onlyChangyeRows(readChangyeTaskAreas(t, ctx, pool)); !slices.Equal(got, []changyeAreaRow{wantChangyeAreaRowPG}) {
		t.Errorf("changye rows after re-up = %+v, want exactly [%+v]", got, wantChangyeAreaRowPG)
	}
	if title, area := taskArea(); title != changyeProbeTitle || area != changyeSlug {
		t.Errorf("seeded task = (%q, %q) after re-up, want (%q, %q) untouched", title, area, changyeProbeTitle, changyeSlug)
	}
}
