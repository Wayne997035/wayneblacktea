package gtd

import (
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
)

// BucketName identifies which upcoming-tasks bucket a task belongs to.
type BucketName string

const (
	BucketToday                BucketName = "today"
	BucketTomorrow             BucketName = "tomorrow"
	BucketDayAfter             BucketName = "day_after"
	BucketUpcoming             BucketName = "upcoming"
	BucketUnscheduledImportant BucketName = "unscheduled_important"
)

// UpcomingGroups is the result of grouping tasks into the 5 upcoming buckets.
type UpcomingGroups struct {
	Today                []db.Task `json:"today"`
	Tomorrow             []db.Task `json:"tomorrow"`
	DayAfter             []db.Task `json:"day_after"`
	Upcoming             []db.Task `json:"upcoming"`
	UnscheduledImportant []db.Task `json:"unscheduled_important"`
}

// GroupUpcomingTasks assigns each task to one of the 5 buckets.
// refDate is the current date in the caller's timezone; the day boundaries are
// computed from it. Tasks are assigned at most once (first matching bucket wins).
// Total tasks returned across all buckets is capped at limit.
func GroupUpcomingTasks(tasks []db.Task, refDate time.Time, tz *time.Location, days, limit int) UpcomingGroups {
	// Compute day boundaries in the target timezone.
	refLocal := refDate.In(tz)
	todayStart := time.Date(refLocal.Year(), refLocal.Month(), refLocal.Day(), 0, 0, 0, 0, tz)
	tomorrowStart := todayStart.AddDate(0, 0, 1)
	dayAfterStart := todayStart.AddDate(0, 0, 2)
	// "upcoming" extends up to days from today; tasks on day N where N >= 3.
	upcomingEnd := todayStart.AddDate(0, 0, days)

	var g UpcomingGroups
	total := 0

	for i := range tasks {
		if total >= limit {
			break
		}
		t := tasks[i]

		// Unscheduled important: importance=1 and no due_date.
		if !t.DueDate.Valid {
			if t.Importance.Valid && t.Importance.Int16 == 1 {
				g.UnscheduledImportant = append(g.UnscheduledImportant, t)
				total++
			}
			continue
		}

		// Convert due_date to the target timezone for day-boundary comparison.
		due := t.DueDate.Time.In(tz)

		switch {
		case due.Before(tomorrowStart):
			// today bucket: also includes past-due tasks (due < todayStart).
			g.Today = append(g.Today, t)
			total++
		case due.Before(dayAfterStart):
			g.Tomorrow = append(g.Tomorrow, t)
			total++
		case due.Before(upcomingEnd):
			// day_after is the slot for tasks due exactly on todayStart+2.
			// "upcoming" covers days 3..days-1.
			dayAfterEnd := todayStart.AddDate(0, 0, 3)
			if due.Before(dayAfterEnd) {
				g.DayAfter = append(g.DayAfter, t)
			} else {
				g.Upcoming = append(g.Upcoming, t)
			}
			total++
		}
	}

	return g
}
