package gtd_test

import (
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// makeTask is a helper to produce a db.Task with due_date set to t.
// pass nil for t → due_date NULL.
func makeTaskWithDue(title string, due *time.Time, importance *int16) db.Task {
	task := db.Task{
		ID:     uuid.New(),
		Title:  title,
		Status: statusPending,
	}
	if due != nil {
		task.DueDate = pgtype.Timestamptz{Time: *due, Valid: true}
	}
	if importance != nil {
		task.Importance = pgtype.Int2{Int16: *importance, Valid: true}
	}
	return task
}

func ptr16(v int16) *int16 { return &v }

func TestGroupUpcomingTasks_Bucketing(t *testing.T) {
	tz := time.UTC
	// Use a fixed reference date to make the test deterministic.
	ref := time.Date(2026, 5, 15, 10, 0, 0, 0, tz)

	yesterday := ref.AddDate(0, 0, -1)
	today := ref
	tomorrow := ref.AddDate(0, 0, 1)
	dayAfter := ref.AddDate(0, 0, 2)
	in5Days := ref.AddDate(0, 0, 5)

	imp1 := int16(1)
	imp2 := int16(2)

	cases := []struct {
		name            string
		tasks           []db.Task
		days            int
		limit           int
		wantToday       int
		wantTomrow      int
		wantDayAftr     int
		wantUpcoming    int
		wantUnscheduled int
	}{
		{
			name: "happy path — all five buckets filled",
			tasks: []db.Task{
				makeTaskWithDue("yesterday-task", &yesterday, nil),
				makeTaskWithDue("today-task", &today, nil),
				makeTaskWithDue("tomorrow-task", &tomorrow, nil),
				makeTaskWithDue("day-after-task", &dayAfter, nil),
				makeTaskWithDue("upcoming-task", &in5Days, nil),
				makeTaskWithDue("unscheduled-imp", nil, &imp1),
			},
			days:            7,
			limit:           50,
			wantToday:       2, // yesterday (past-due) + today
			wantTomrow:      1,
			wantDayAftr:     1,
			wantUpcoming:    1,
			wantUnscheduled: 1,
		},
		{
			name: "past-due task goes into today bucket",
			tasks: []db.Task{
				makeTaskWithDue("old-task", &yesterday, nil),
			},
			days:            7,
			limit:           50,
			wantToday:       1,
			wantTomrow:      0,
			wantDayAftr:     0,
			wantUpcoming:    0,
			wantUnscheduled: 0,
		},
		{
			name: "importance=2 task without due_date → unscheduled_important (all no-date go in)",
			tasks: []db.Task{
				makeTaskWithDue("imp2-no-due", nil, &imp2),
			},
			days:            7,
			limit:           50,
			wantUnscheduled: 1,
		},
		{
			name: "importance=1 task WITHOUT due_date → unscheduled_important",
			tasks: []db.Task{
				makeTaskWithDue("imp1-no-due", nil, &imp1),
			},
			days:            7,
			limit:           50,
			wantUnscheduled: 1,
		},
		{
			name: "limit=1 caps total across all buckets",
			tasks: []db.Task{
				makeTaskWithDue("today-task", &today, nil),
				makeTaskWithDue("tomorrow-task", &tomorrow, nil),
				makeTaskWithDue("unscheduled", nil, &imp1),
			},
			days:  7,
			limit: 1,
			// Only first task consumed (today).
			wantToday:       1,
			wantTomrow:      0,
			wantUnscheduled: 0,
		},
		{
			name:            "empty tasks → all buckets empty",
			tasks:           []db.Task{},
			days:            7,
			limit:           50,
			wantToday:       0,
			wantTomrow:      0,
			wantDayAftr:     0,
			wantUpcoming:    0,
			wantUnscheduled: 0,
		},
		{
			name: "task due beyond window not included",
			tasks: []db.Task{
				makeTaskWithDue("far-future", ptrTime(ref.AddDate(0, 0, 30)), nil),
			},
			days:         7,
			limit:        50,
			wantUpcoming: 0, // 30 days ahead > window of 7
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			groups := gtd.GroupUpcomingTasks(tc.tasks, ref, tz, tc.days, tc.limit)

			if len(groups.Today) != tc.wantToday {
				t.Errorf("today: got %d, want %d", len(groups.Today), tc.wantToday)
			}
			if tc.wantTomrow > 0 || len(groups.Tomorrow) != 0 {
				if len(groups.Tomorrow) != tc.wantTomrow {
					t.Errorf("tomorrow: got %d, want %d", len(groups.Tomorrow), tc.wantTomrow)
				}
			}
			if tc.wantDayAftr > 0 || len(groups.DayAfter) != 0 {
				if len(groups.DayAfter) != tc.wantDayAftr {
					t.Errorf("day_after: got %d, want %d", len(groups.DayAfter), tc.wantDayAftr)
				}
			}
			if tc.wantUpcoming > 0 || len(groups.Upcoming) != 0 {
				if len(groups.Upcoming) != tc.wantUpcoming {
					t.Errorf("upcoming: got %d, want %d", len(groups.Upcoming), tc.wantUpcoming)
				}
			}
			if len(groups.UnscheduledImportant) != tc.wantUnscheduled {
				t.Errorf("unscheduled_important: got %d, want %d", len(groups.UnscheduledImportant), tc.wantUnscheduled)
			}
		})
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestGroupUpcomingTasks_CompletedTasksExcluded(t *testing.T) {
	tz := time.UTC
	ref := time.Date(2026, 5, 15, 10, 0, 0, 0, tz)
	due := ref.Add(time.Hour)

	completed := db.Task{
		ID:      uuid.New(),
		Title:   "completed-task",
		Status:  statusCompleted,
		DueDate: pgtype.Timestamptz{Time: due, Valid: true},
	}

	// GroupUpcomingTasks itself does NOT filter by status — the store query does.
	// This test verifies that if a completed task somehow slips through,
	// it still gets assigned to a bucket (grouping is status-agnostic by design).
	groups := gtd.GroupUpcomingTasks([]db.Task{completed}, ref, tz, 7, 50)
	total := len(groups.Today) + len(groups.Tomorrow) + len(groups.DayAfter) + len(groups.Upcoming) + len(groups.UnscheduledImportant)
	if total != 1 {
		t.Errorf("expected completed task to still be bucketed (store handles filtering), got total=%d", total)
	}
}

func TestGroupUpcomingTasks_WorkspaceScoping(t *testing.T) {
	// This test documents that GroupUpcomingTasks does NOT do workspace
	// filtering itself — that responsibility belongs to the store query.
	// If two tasks from different workspaces are passed in, both get bucketed.
	tz := time.UTC
	ref := time.Date(2026, 5, 15, 10, 0, 0, 0, tz)
	due := ref.Add(time.Hour)

	t1 := makeTaskWithDue("ws-a-task", &due, nil)
	t1.WorkspaceID = pgtype.UUID{Bytes: [16]byte{0x01}, Valid: true}
	t2 := makeTaskWithDue("ws-b-task", &due, nil)
	t2.WorkspaceID = pgtype.UUID{Bytes: [16]byte{0x02}, Valid: true}

	groups := gtd.GroupUpcomingTasks([]db.Task{t1, t2}, ref, tz, 7, 50)
	if len(groups.Today) != 2 {
		t.Errorf("expected both tasks in today bucket, got %d", len(groups.Today))
	}
}

func TestGroupUpcomingTasks_TimezoneShift(t *testing.T) {
	// Tasks near midnight can flip buckets when the timezone changes.
	taipei, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("LoadLocation Asia/Taipei: %v", err)
	}

	// UTC midnight of 2026-05-15 is 2026-05-15 08:00 Taipei time.
	// A task due at UTC 2026-05-15 23:59 is "today" in UTC but also "today" in Taipei.
	refUTC := time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)
	dueUTC := time.Date(2026, 5, 15, 23, 59, 0, 0, time.UTC) // today in UTC

	task := makeTaskWithDue("near-midnight", &dueUTC, nil)

	groupsUTC := gtd.GroupUpcomingTasks([]db.Task{task}, refUTC, time.UTC, 7, 50)
	if len(groupsUTC.Today) != 1 {
		t.Errorf("UTC: expected task in today bucket, got today=%d", len(groupsUTC.Today))
	}

	// Same task viewed in Taipei: 2026-05-16 07:59 +08:00 → tomorrow.
	refTaipei := refUTC.In(taipei)
	groupsTaipei := gtd.GroupUpcomingTasks([]db.Task{task}, refTaipei, taipei, 7, 50)
	if len(groupsTaipei.Tomorrow) != 1 {
		t.Errorf("Taipei: expected task in tomorrow bucket, got today=%d tomorrow=%d",
			len(groupsTaipei.Today), len(groupsTaipei.Tomorrow))
	}
}

// TestGTDStore_UpcomingTasks_Postgres is in store_postgres_test.go (integration, requires Docker).
// TestGTDStore_UpcomingTasks_SQLite is in internal/storage/sqlite/gtd_test.go.
// This file covers the pure-Go grouping logic only.
var _ = ptr16 // used in test cases
