package sqlite_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// [F170-21] goals.due_date / tasks.due_date must be written in ONE layout by
// every SQLite write path.
//
// The bug these guard: Create*/Update* used time.RFC3339Nano, which strips
// trailing fractional zeros, while Import* used the fixed 3-digit layout. The
// same instant therefore became two different strings and SQLite compares
// TEXT byte-wise, with '.' (0x2E) sorting before 'Z' (0x5A). Postgres stores
// the column as TIMESTAMPTZ and sees one instant, so the two backends
// disagreed about ordering — and once ActiveGoalsPage grew a LIMIT, ordering
// decides page membership rather than merely display order.
//
// Why the pre-existing TestSQLiteStore_ActiveGoalsPage_CapsAndPages
// (rowcap_test.go) could not catch it: it seeds through CreateGoal ONLY and
// its fixtures have no due_date at all, so every stored value shares one
// layout and the comparison never mixes shapes. Seeding through BOTH paths is
// the whole point of these tests.

// dueDateProbeInstant is the single instant every fixture below shares. It has
// zero sub-second component precisely because that is the case RFC3339Nano
// renders differently from the fixed layout ("...T09:00:00Z" vs
// "...T09:00:00.000Z") — a fixture with non-zero milliseconds would round-trip
// identically under both layouts and prove nothing.
var dueDateProbeInstant = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

// seedGoalViaCreate writes a goal through the CreateGoal path.
func seedGoalViaCreate(t *testing.T, s goalSeedStore, title string, due time.Time) uuid.UUID {
	t.Helper()
	g, err := s.CreateGoal(context.Background(), gtd.CreateGoalParams{
		Title:   title,
		Area:    "career",
		DueDate: &due,
	})
	if err != nil {
		t.Fatalf("CreateGoal(%s): %v", title, err)
	}
	return g.ID
}

// seedGoalViaImport writes a goal through the ImportGoal path — the one that
// already used the fixed layout, and therefore the one whose rows the
// Create path used to be incomparable with.
func seedGoalViaImport(t *testing.T, s goalSeedStore, id uuid.UUID, title string, due time.Time) uuid.UUID {
	t.Helper()
	now := time.Now().UTC()
	err := s.ImportGoal(context.Background(), db.Goal{
		ID:        id,
		Title:     title,
		Status:    "active",
		Area:      pgtype.Text{String: "career", Valid: true},
		DueDate:   pgtype.Timestamptz{Time: due, Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		t.Fatalf("ImportGoal(%s): %v", title, err)
	}
	return id
}

// goalSeedStore is the slice of *sqlite.GTDStore these tests use.
type goalSeedStore interface {
	CreateGoal(context.Context, gtd.CreateGoalParams) (*db.Goal, error)
	ImportGoal(context.Context, db.Goal) error
	ActiveGoalsPage(context.Context, int32, int32) ([]db.Goal, error)
}

// TestSQLiteStore_ActiveGoalsPage_MixedWritePathOrdering is [F170-21]'s
// primary probe: goals at the IDENTICAL instant written through BOTH paths
// must come back in the order Postgres would return them.
//
// Postgres compares equal TIMESTAMPTZ values, falls through to the `id ASC`
// tiebreaker, and returns them in id order. Under the bug the CreateGoal row
// stores "...00Z" and the ImportGoal rows store "...00.000Z"; '.' (0x2E)
// sorts before 'Z' (0x5A), so SQLite returns BOTH imports before the created
// row no matter what their ids are.
//
// The fixture is built so that outcome cannot coincide with the correct one.
// CreateGoal allocates its own random id, so the two import ids bracket it:
// the all-zero id is below any possible v4 UUID and the all-f id is above any
// possible v4 UUID (v4 pins the version and variant nibbles, so neither
// extreme is reachable by uuid.New). The correct order is therefore
// [low, created, high] while the buggy order is [low, high, created] —
// different for every possible value of the random id, which is what makes
// this a real guard rather than one that passes ~75% of the time.
func TestSQLiteStore_ActiveGoalsPage_MixedWritePathOrdering(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	lowImportID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	highImportID := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")

	seedGoalViaImport(t, s, lowImportID, "ImportGoal low id", dueDateProbeInstant)
	seedGoalViaImport(t, s, highImportID, "ImportGoal high id", dueDateProbeInstant)
	createdID := seedGoalViaCreate(t, s, "CreateGoal", dueDateProbeInstant)

	page, err := s.ActiveGoalsPage(ctx, 50, 0)
	if err != nil {
		t.Fatalf("ActiveGoalsPage: %v", err)
	}
	if len(page) != 3 {
		t.Fatalf("seeded 3 goals, page returned %d", len(page))
	}

	// Every row must carry the SAME due_date instant. Under the bug the two
	// paths stored different text; here they must agree once parsed AND the
	// ordering must be a pure id sort, because all three instants are equal.
	for _, g := range page {
		if !g.DueDate.Valid {
			t.Fatalf("goal %s came back with a NULL due_date", g.ID)
		}
		if !g.DueDate.Time.UTC().Equal(dueDateProbeInstant) {
			t.Errorf("goal %s due_date = %s, want %s",
				g.ID, g.DueDate.Time.UTC().Format(time.RFC3339Nano), dueDateProbeInstant.Format(time.RFC3339Nano))
		}
	}

	gotOrder := make([]uuid.UUID, len(page))
	for i, g := range page {
		gotOrder[i] = g.ID
	}
	wantOrder := []uuid.UUID{lowImportID, highImportID, createdID}
	sort.Slice(wantOrder, func(i, j int) bool { return wantOrder[i].String() < wantOrder[j].String() })

	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Errorf("page order[%d] = %s, want %s — SQLite is not ordering by instant then id "+
				"the way Postgres does (mixed due_date TEXT layouts sort byte-wise, so '.000Z' "+
				"lands before 'Z' for the same moment)\n got: %v\nwant: %v",
				i, gotOrder[i], wantOrder[i], gotOrder, wantOrder)
			break
		}
	}
}

// TestSQLiteStore_DueDateLayout_IdenticalAcrossWritePaths asserts the stored
// TEXT itself, not just the parsed value — [F170-21]. Parsing hides the bug
// (both shapes parse to the same instant); byte equality is what SQLite's
// ORDER BY actually sees.
func TestSQLiteStore_DueDateLayout_IdenticalAcrossWritePaths(t *testing.T) {
	s, d := openMemWithDB(t, "")
	ctx := context.Background()

	importID := uuid.MustParse("cccccccc-0000-0000-0000-000000000003")
	seedGoalViaImport(t, s, importID, "ImportGoal", dueDateProbeInstant)
	createdID := seedGoalViaCreate(t, s, "CreateGoal", dueDateProbeInstant)

	readRaw := func(id uuid.UUID) string {
		t.Helper()
		var raw string
		row := d.QueryRowContext(ctx, `SELECT due_date FROM goals WHERE id = ?`, id.String())
		if err := row.Scan(&raw); err != nil {
			t.Fatalf("scan due_date for %s: %v", id, err)
		}
		return raw
	}

	createRaw := readRaw(createdID)
	importRaw := readRaw(importID)

	const want = "2026-09-01T09:00:00.000Z"
	if createRaw != want {
		t.Errorf("CreateGoal stored due_date %q, want %q — the Create path is not using nullTimeArg", createRaw, want)
	}
	if importRaw != want {
		t.Errorf("ImportGoal stored due_date %q, want %q", importRaw, want)
	}
	if createRaw != importRaw {
		t.Errorf("the two write paths stored the SAME instant as different text (%q vs %q); "+
			"SQLite compares due_date byte-wise, so these two rows can never sort correctly "+
			"against each other", createRaw, importRaw)
	}
}

// TestSQLiteStore_TaskDueDateLayout_IdenticalAcrossWritePaths is the tasks
// half of [F170-21]. tasks.due_date has the identical two-layout split
// (CreateTask/UpdateTask vs ImportTask) and its due-window queries
// (TasksByDueDateRange, UpcomingTasks, PullForwardTasks) compare the column
// lexicographically, so leaving it unfixed would have left a second instance
// of the same defect behind.
func TestSQLiteStore_TaskDueDateLayout_IdenticalAcrossWritePaths(t *testing.T) {
	s, d := openMemWithDB(t, "")
	ctx := context.Background()

	created, err := s.CreateTask(ctx, gtd.CreateTaskParams{
		Title:   "CreateTask due",
		DueDate: &dueDateProbeInstant,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	importID := uuid.MustParse("dddddddd-0000-0000-0000-000000000004")
	now := time.Now().UTC()
	if impErr := s.ImportTask(ctx, db.Task{
		ID:        importID,
		Title:     "ImportTask due",
		Status:    "pending",
		Priority:  3,
		DueDate:   pgtype.Timestamptz{Time: dueDateProbeInstant, Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}); impErr != nil {
		t.Fatalf("ImportTask: %v", impErr)
	}

	readRaw := func(id uuid.UUID) string {
		t.Helper()
		var raw string
		row := d.QueryRowContext(ctx, `SELECT due_date FROM tasks WHERE id = ?`, id.String())
		if scanErr := row.Scan(&raw); scanErr != nil {
			t.Fatalf("scan due_date for %s: %v", id, scanErr)
		}
		return raw
	}

	createRaw := readRaw(created.ID)
	importRaw := readRaw(importID)
	const want = "2026-09-01T09:00:00.000Z"
	if createRaw != want || importRaw != want {
		t.Errorf("tasks.due_date layouts differ: CreateTask=%q ImportTask=%q, want both %q",
			createRaw, importRaw, want)
	}
}

// TestSQLiteStore_TasksByDueDateRange_IncludesBoundaryRow pins the half of
// [F170-21] that a write-path-only fix would have broken: the query BIND
// parameters must use the same layout as the stored values.
//
// With due_date stored as "...T09:00:00.000Z" and the boundary bound as
// RFC3339Nano "...T09:00:00Z", `due_date >= ?` is FALSE for the row due
// exactly at `from` — '.' (0x2E) sorts before 'Z' (0x5A) — so the task
// silently vanishes from its own due window.
func TestSQLiteStore_TasksByDueDateRange_IncludesBoundaryRow(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	if _, err := s.CreateTask(ctx, gtd.CreateTaskParams{
		Title:   "due exactly at the window start",
		DueDate: &dueDateProbeInstant,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// from == the task's exact due instant; an inclusive >= must return it.
	got, err := s.TasksByDueDateRange(ctx, dueDateProbeInstant, dueDateProbeInstant.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("TasksByDueDateRange: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("boundary task missing from its own due window: got %d rows, want 1 — "+
			"the bound parameter layout does not match the stored layout", len(got))
	}
}
