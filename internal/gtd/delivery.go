package gtd

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
)

// DeliveryGoal is a single row in the doctor JSON's goals_due array: an
// active goal with a valid due date, reduced to title + deadline only. No
// goal UUID, description, or area is exposed — data minimisation
// (backend-security-design.md §3.2); `wbt doctor` output is a local
// operator-facing JSON blob and MUST NOT leak internal identifiers.
type DeliveryGoal struct {
	Title    string    `json:"title"`
	DueDate  time.Time `json:"due_date"`
	DaysLeft int       `json:"days_left"`
}

// DeliveryTask is the doctor JSON's top_pending object: the single
// highest-priority pending task, reduced to title + optional deadline.
// DueDate is nil (renders JSON null) when the task has no due date — the
// task is still emitted (title-only) so "what's next" is always visible,
// per the target contract ("top pending no due still emit, due=null").
type DeliveryTask struct {
	Title   string     `json:"title"`
	DueDate *time.Time `json:"due_date"`
}

// BuildGoalsDue returns the goals_due delivery-visibility list for the
// store's configured workspace: active goals with a valid due date, ordered
// by due instant ASC, title ASC, goal UUID ASC. The tie-break order is
// applied here because the backing query (ListActiveGoals / SQLite
// GTDStore.ActiveGoals) only orders by due_date — this keeps ordering
// deterministic and identical across both backends. Goals without a due
// date are omitted; they have nothing actionable to show on a
// delivery-visibility line. now is the reference instant for days_left
// (Asia/Taipei calendar-date difference, see DaysLeftTaipei). Always
// returns a non-nil slice (possibly empty) on success so JSON callers can
// rely on "[]" rather than null.
func BuildGoalsDue(ctx context.Context, store StoreIface, now time.Time) ([]DeliveryGoal, error) {
	goals, err := store.ActiveGoals(ctx)
	if err != nil {
		return nil, fmt.Errorf("building goals_due: %w", err)
	}
	return goalsDueFromRows(goals, now)
}

// goalsDueFromRows is the pure (no I/O) core of BuildGoalsDue, split out so
// the date-arithmetic and ordering logic can be unit tested without a
// store/DB fixture.
func goalsDueFromRows(goals []db.Goal, now time.Time) ([]DeliveryGoal, error) {
	type withDays struct {
		goal db.Goal
		days int
	}
	dated := make([]withDays, 0, len(goals))
	for _, g := range goals {
		if !g.DueDate.Valid {
			continue // no due date → nothing actionable to show
		}
		days, err := DaysLeftTaipei(g.DueDate.Time, now)
		if err != nil {
			return nil, fmt.Errorf("goals_due days_left for goal %q: %w", g.Title, err)
		}
		dated = append(dated, withDays{goal: g, days: days})
	}
	sort.Slice(dated, func(i, j int) bool {
		a, b := dated[i].goal, dated[j].goal
		if !a.DueDate.Time.Equal(b.DueDate.Time) {
			return a.DueDate.Time.Before(b.DueDate.Time)
		}
		if a.Title != b.Title {
			return a.Title < b.Title
		}
		// Goal UUID ASC final tie-break for full determinism. The UUID is
		// used only for comparison here — DeliveryGoal never carries it.
		return a.ID.String() < b.ID.String()
	})
	out := make([]DeliveryGoal, 0, len(dated))
	for _, d := range dated {
		out = append(out, DeliveryGoal{
			Title:    d.goal.Title,
			DueDate:  d.goal.DueDate.Time,
			DaysLeft: d.days,
		})
	}
	return out, nil
}

// BuildTopPending returns the top_pending delivery-visibility object for the
// store's configured workspace: the existing highest-priority pending task
// (StoreIface.TopPendingTask order: priority ASC, importance ASC NULLS
// LAST, created_at ASC — unchanged, reused as-is), reduced to title +
// optional deadline. Returns nil, nil when no pending task exists — callers
// render top_pending as JSON null.
func BuildTopPending(ctx context.Context, store StoreIface) (*DeliveryTask, error) {
	t, err := store.TopPendingTask(ctx)
	if err != nil {
		return nil, fmt.Errorf("building top_pending: %w", err)
	}
	return topPendingFromRow(t), nil
}

// topPendingFromRow is the pure (no I/O) core of BuildTopPending.
func topPendingFromRow(t *db.Task) *DeliveryTask {
	if t == nil {
		return nil
	}
	dt := &DeliveryTask{Title: t.Title}
	if t.DueDate.Valid {
		due := t.DueDate.Time
		dt.DueDate = &due
	}
	return dt
}

// DaysLeftTaipei returns the Asia/Taipei calendar-date difference between
// due and now: overdue → negative, due today → 0, due tomorrow → 1.
// Mirrors PullForwardTomorrowStart's use of time.Date on the
// Location-converted instant (NOT Time.Truncate, which rounds against the
// UTC epoch and lands on the wrong wall-clock boundary — see that
// function's doc comment for the 2026-07-20 production bug this pattern
// exists to avoid). Taiwan has no DST, so this is a plain 24h-multiple
// offset, not a DST edge case.
func DaysLeftTaipei(due, now time.Time) (int, error) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return 0, fmt.Errorf("DaysLeftTaipei: loading Asia/Taipei timezone: %w", err)
	}
	dueLocal := due.In(loc)
	nowLocal := now.In(loc)
	dueDay := time.Date(dueLocal.Year(), dueLocal.Month(), dueLocal.Day(), 0, 0, 0, 0, loc)
	nowDay := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, loc)
	return int(dueDay.Sub(nowDay).Hours() / 24), nil
}
