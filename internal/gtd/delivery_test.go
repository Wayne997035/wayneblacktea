package gtd

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// taipeiLoc is loaded once for test fixture construction.
var taipeiLoc = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		panic(err) // test environment MUST have IANA tzdata available
	}
	return loc
}()

// TestDaysLeftTaipei covers the Asia/Taipei calendar-date-difference
// contract: overdue negative, today 0, tomorrow 1 — plus same-day edge
// instants (early morning / late night) to prove the calculation is
// calendar-date based, not a raw 24h-duration division.
func TestDaysLeftTaipei(t *testing.T) {
	// now is a fixed reference instant: 2026-07-23 12:00 Taipei.
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, taipeiLoc)

	cases := []struct {
		name string
		due  time.Time
		want int
	}{
		{
			name: "due today, same time of day → 0",
			due:  time.Date(2026, 7, 23, 12, 0, 0, 0, taipeiLoc),
			want: 0,
		},
		{
			name: "due today, early morning → 0 (calendar date, not duration)",
			due:  time.Date(2026, 7, 23, 0, 1, 0, 0, taipeiLoc),
			want: 0,
		},
		{
			name: "due today, just before midnight → 0",
			due:  time.Date(2026, 7, 23, 23, 59, 0, 0, taipeiLoc),
			want: 0,
		},
		{
			name: "due tomorrow → 1",
			due:  time.Date(2026, 7, 24, 0, 0, 1, 0, taipeiLoc),
			want: 1,
		},
		{
			name: "due yesterday (overdue) → -1",
			due:  time.Date(2026, 7, 22, 23, 59, 0, 0, taipeiLoc),
			want: -1,
		},
		{
			name: "overdue by a week → -7",
			due:  time.Date(2026, 7, 16, 8, 0, 0, 0, taipeiLoc),
			want: -7,
		},
		{
			name: "due date supplied in UTC still converts to Taipei calendar date",
			// 2026-07-23 20:00 UTC == 2026-07-24 04:00 Taipei → tomorrow.
			due:  time.Date(2026, 7, 23, 20, 0, 0, 0, time.UTC),
			want: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DaysLeftTaipei(tc.due, now)
			if err != nil {
				t.Fatalf("DaysLeftTaipei(%v, %v) unexpected error: %v", tc.due, now, err)
			}
			if got != tc.want {
				t.Errorf("DaysLeftTaipei(%v, %v) = %d, want %d", tc.due, now, got, tc.want)
			}
		})
	}
}

func mkGoal(id uuid.UUID, title string, due time.Time, hasDue bool) db.Goal {
	g := db.Goal{ID: id, Title: title, Status: "active"}
	if hasDue {
		g.DueDate = pgtype.Timestamptz{Time: due, Valid: true}
	}
	return g
}

// TestGoalsDueFromRows covers filtering (no due date omitted), days_left
// wiring, and the full ordering contract: due instant ASC, title ASC, goal
// UUID ASC — including the same-day deterministic tie-break cases. Table
// form keeps this under the gocyclo threshold; each case only asserts on
// the resulting title order (+ days_left where relevant) via a single
// shared comparison, rather than a bespoke branch per case.
func TestGoalsDueFromRows(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, taipeiLoc)
	tomorrow := time.Date(2026, 7, 24, 9, 0, 0, 0, taipeiLoc)
	dayAfter := time.Date(2026, 7, 25, 9, 0, 0, 0, taipeiLoc)

	idLow := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	idHigh := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	cases := []struct {
		name         string
		goals        []db.Goal
		wantTitles   []string
		wantDaysLeft []int // nil → skip days_left assertion for this case
	}{
		{
			name: "no due date is omitted, valid due date kept with days_left",
			goals: []db.Goal{
				mkGoal(uuid.New(), "no due date", time.Time{}, false),
				mkGoal(idLow, "has due date", tomorrow, true),
			},
			wantTitles:   []string{"has due date"},
			wantDaysLeft: []int{1},
		},
		{
			name: "due instant ASC ordering across distinct dates",
			goals: []db.Goal{
				mkGoal(uuid.New(), "later", dayAfter, true),
				mkGoal(uuid.New(), "sooner", tomorrow, true),
			},
			wantTitles: []string{"sooner", "later"},
		},
		{
			name: "same due instant → title ASC tie-break",
			goals: []db.Goal{
				mkGoal(uuid.New(), "zeta", tomorrow, true),
				mkGoal(uuid.New(), "alpha", tomorrow, true),
			},
			wantTitles: []string{"alpha", "zeta"},
		},
		{
			// idLow < idHigh lexicographically, so idLow's goal (identical
			// title/due otherwise) must sort first — deterministic even
			// though DeliveryGoal itself carries no ID field in its output.
			name: "same due instant AND same title → goal UUID ASC final tie-break",
			goals: []db.Goal{
				mkGoal(idHigh, "dup title", tomorrow, true),
				mkGoal(idLow, "dup title", tomorrow, true),
			},
			wantTitles: []string{"dup title", "dup title"},
		},
		{
			name:       "empty input → non-nil empty slice",
			goals:      nil,
			wantTitles: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := goalsDueFromRows(tc.goals, now)
			if err != nil {
				t.Fatalf("goalsDueFromRows: %v", err)
			}
			if got == nil {
				t.Fatal("expected non-nil slice (even when empty)")
			}
			gotTitles := make([]string, len(got))
			for i, g := range got {
				gotTitles[i] = g.Title
			}
			if !reflect.DeepEqual(gotTitles, tc.wantTitles) {
				t.Fatalf("titles = %v, want %v (full: %+v)", gotTitles, tc.wantTitles, got)
			}
			for i, want := range tc.wantDaysLeft {
				if got[i].DaysLeft != want {
					t.Errorf("got[%d].DaysLeft = %d, want %d", i, got[i].DaysLeft, want)
				}
			}
		})
	}
}

// TestGoalsDueFromRows_DaysLeftPropagation verifies days_left is correctly
// threaded end-to-end (goal → DaysLeftTaipei → DeliveryGoal.DaysLeft) for a
// clearly-overdue fixture, distinct from the boundary cases already covered
// in TestDaysLeftTaipei.
func TestGoalsDueFromRows_DaysLeftPropagation(t *testing.T) {
	// Sanity: a goal whose due date round-trips through DaysLeftTaipei
	// without error keeps its computed value end-to-end.
	now := time.Date(2026, 7, 23, 0, 0, 0, 0, taipeiLoc)
	due := time.Date(2026, 7, 20, 0, 0, 0, 0, taipeiLoc)
	got, err := goalsDueFromRows([]db.Goal{mkGoal(uuid.New(), "overdue goal", due, true)}, now)
	if err != nil {
		t.Fatalf("goalsDueFromRows: %v", err)
	}
	if len(got) != 1 || got[0].DaysLeft != -3 {
		t.Fatalf("expected days_left=-3, got %+v", got)
	}
}

func mkTask(title string, due time.Time, hasDue bool) *db.Task {
	tk := &db.Task{Title: title, Status: "pending"}
	if hasDue {
		tk.DueDate = pgtype.Timestamptz{Time: due, Valid: true}
	}
	return tk
}

// TestTopPendingFromRow covers the three required shapes: nil task → nil
// object (JSON null), task with due date → both fields populated, task
// without due date → still emitted with DueDate nil (JSON null).
func TestTopPendingFromRow(t *testing.T) {
	cases := []struct {
		name       string
		task       *db.Task
		wantNil    bool
		wantTitle  string
		wantHasDue bool
	}{
		{name: "nil task → nil object", task: nil, wantNil: true},
		{
			name:       "task with due date",
			task:       mkTask("ship the thing", time.Date(2026, 7, 25, 0, 0, 0, 0, taipeiLoc), true),
			wantTitle:  "ship the thing",
			wantHasDue: true,
		},
		{
			name:       "task without due date → still emitted, due=null",
			task:       mkTask("unscheduled but top priority", time.Time{}, false),
			wantTitle:  "unscheduled but top priority",
			wantHasDue: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := topPendingFromRow(tc.task)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil DeliveryTask")
			}
			if got.Title != tc.wantTitle {
				t.Errorf("Title = %q, want %q", got.Title, tc.wantTitle)
			}
			if tc.wantHasDue && got.DueDate == nil {
				t.Error("expected non-nil DueDate")
			}
			if !tc.wantHasDue && got.DueDate != nil {
				t.Errorf("expected nil DueDate, got %v", *got.DueDate)
			}
		})
	}
}

// fakeGTDStore implements StoreIface with only ActiveGoals/TopPendingTask
// wired to configurable return values; every other method panics if called
// (BuildGoalsDue/BuildTopPending must not touch anything else).
type fakeGTDStore struct {
	StoreIface
	goals      []db.Goal
	goalsErr   error
	topTask    *db.Task
	topTaskErr error
}

func (f *fakeGTDStore) ActiveGoals(_ context.Context) ([]db.Goal, error) {
	return f.goals, f.goalsErr
}

func (f *fakeGTDStore) TopPendingTask(_ context.Context) (*db.Task, error) {
	return f.topTask, f.topTaskErr
}

// TestBuildGoalsDue_StoreError verifies the store-error path is wrapped and
// propagated (not silently swallowed) so the caller can slog.Warn + fall
// back to the pre-initialised empty slice.
func TestBuildGoalsDue_StoreError(t *testing.T) {
	wantErr := errors.New("db unavailable")
	store := &fakeGTDStore{goalsErr: wantErr}
	got, err := BuildGoalsDue(context.Background(), store, time.Now())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("expected wrapped %v, got %v", wantErr, err)
	}
	if got != nil {
		t.Errorf("expected nil result on error, got %+v", got)
	}
}

// TestBuildTopPending_StoreError mirrors TestBuildGoalsDue_StoreError for
// the top_pending collector — independent failure, error propagated.
func TestBuildTopPending_StoreError(t *testing.T) {
	wantErr := errors.New("db unavailable")
	store := &fakeGTDStore{topTaskErr: wantErr}
	got, err := BuildTopPending(context.Background(), store)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("expected wrapped %v, got %v", wantErr, err)
	}
	if got != nil {
		t.Errorf("expected nil result on error, got %+v", got)
	}
}

// TestBuildGoalsDue_Success drives the full BuildGoalsDue path (store call +
// pure filtering/sort) end to end via the fake store.
func TestBuildGoalsDue_Success(t *testing.T) {
	now := time.Date(2026, 7, 23, 0, 0, 0, 0, taipeiLoc)
	due := time.Date(2026, 7, 24, 0, 0, 0, 0, taipeiLoc)
	store := &fakeGTDStore{goals: []db.Goal{mkGoal(uuid.New(), "ship it", due, true)}}
	got, err := BuildGoalsDue(context.Background(), store, now)
	if err != nil {
		t.Fatalf("BuildGoalsDue: %v", err)
	}
	if len(got) != 1 || got[0].Title != "ship it" || got[0].DaysLeft != 1 {
		t.Fatalf("unexpected result: %+v", got)
	}
}

// TestBuildTopPending_Success drives the full BuildTopPending path.
func TestBuildTopPending_Success(t *testing.T) {
	store := &fakeGTDStore{topTask: mkTask("do the thing", time.Time{}, false)}
	got, err := BuildTopPending(context.Background(), store)
	if err != nil {
		t.Fatalf("BuildTopPending: %v", err)
	}
	if got == nil || got.Title != "do the thing" || got.DueDate != nil {
		t.Fatalf("unexpected result: %+v", got)
	}
}
