package gtd_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// TestTaskArea_ReturnedByEveryReadPathPG is the PG regression test for
// F0925-16: every gtd.StoreIface method that returns a db.Task or
// []db.Task must carry the task's area (migration 000079), not the
// zero-value empty string. Before this fix db.Task already had an Area
// field (sqlc regen), but every PG hand-rolled SELECT/RETURNING column
// list in store.go omitted it, so the field was always "" no matter what
// was actually stored.
func TestTaskArea_ReturnedByEveryReadPathPG(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)

	const wantArea = "wbt"

	fatalIfErr := func(step string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	checkArea := func(step, gotArea string) {
		t.Helper()
		if gotArea != wantArea {
			t.Errorf("%s: Area = %q, want %q", step, gotArea, wantArea)
		}
	}

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "area-test-project-pg", Title: "area test project PG"})
	fatalIfErr("CreateProject", err)

	base, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "area base PG", Area: wantArea})
	fatalIfErr("CreateTask base", err)
	checkArea("CreateTask", base.Area)

	withProj, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title: "area proj task PG", ProjectID: &proj.ID, Area: wantArea, Assignee: "claude",
	})
	fatalIfErr("CreateTask withProj", err)

	due := time.Now().Add(2 * time.Hour)
	dueTask, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "area due PG", Area: wantArea, DueDate: &due})
	fatalIfErr("CreateTask dueTask", err)

	imp := int16(1)
	pullTask, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "area pull PG", Area: wantArea, Importance: &imp})
	fatalIfErr("CreateTask pullTask", err)

	// --- read paths that don't mutate state, exercised first ---

	allPending, err := store.Tasks(ctx, nil)
	fatalIfErr("Tasks(nil)", err)
	found := false
	for _, tk := range allPending {
		if tk.ID == base.ID {
			found = true
			checkArea("Tasks(nil)", tk.Area)
		}
	}
	if !found {
		t.Errorf("Tasks(nil): base task %s not found", base.ID)
	}

	projTasks, err := store.Tasks(ctx, &proj.ID)
	fatalIfErr("Tasks(project)", err)
	if len(projTasks) == 0 {
		t.Fatalf("Tasks(project): got 0 rows for project %s", proj.ID)
	}
	checkArea("Tasks(project)", projTasks[0].Area)

	filtered, err := store.TasksFiltered(ctx, gtd.TaskFilter{Area: wantArea, Limit: 100})
	fatalIfErr("TasksFiltered", err)
	if len(filtered) == 0 {
		t.Fatalf("TasksFiltered: got 0 rows for area %q", wantArea)
	}
	for _, tk := range filtered {
		checkArea("TasksFiltered/"+tk.ID.String(), tk.Area)
	}

	allStatuses, err := store.TasksByProjectAllStatuses(ctx, proj.ID)
	fatalIfErr("TasksByProjectAllStatuses", err)
	if len(allStatuses) == 0 {
		t.Fatalf("TasksByProjectAllStatuses: got 0 rows for project %s", proj.ID)
	}
	checkArea("TasksByProjectAllStatuses", allStatuses[0].Area)

	dueRange, err := store.TasksByDueDateRange(ctx, time.Now(), due.Add(time.Hour))
	fatalIfErr("TasksByDueDateRange", err)
	dueFound := false
	for _, tk := range dueRange {
		if tk.ID == dueTask.ID {
			dueFound = true
			checkArea("TasksByDueDateRange", tk.Area)
		}
	}
	if !dueFound {
		t.Errorf("TasksByDueDateRange: dueTask %s not found", dueTask.ID)
	}

	upcoming, err := store.UpcomingTasks(ctx, time.Now(), 7, 50)
	fatalIfErr("UpcomingTasks", err)
	upcomingFound := false
	for _, tk := range upcoming {
		if tk.ID == dueTask.ID {
			upcomingFound = true
			checkArea("UpcomingTasks", tk.Area)
		}
	}
	if !upcomingFound {
		t.Errorf("UpcomingTasks: dueTask %s not found", dueTask.ID)
	}

	timeline, err := store.TasksForTimeline(ctx, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	fatalIfErr("TasksForTimeline", err)
	timelineFound := false
	for _, tk := range timeline {
		if tk.ID == base.ID {
			timelineFound = true
			checkArea("TasksForTimeline", tk.Area)
		}
	}
	if !timelineFound {
		t.Errorf("TasksForTimeline: base task %s not found", base.ID)
	}

	pulled, err := store.PullForwardTasks(ctx, time.Now())
	fatalIfErr("PullForwardTasks", err)
	pullFound := false
	for _, tk := range pulled {
		if tk.ID == pullTask.ID {
			pullFound = true
			checkArea("PullForwardTasks", tk.Area)
		}
	}
	if !pullFound {
		t.Errorf("PullForwardTasks: pullTask %s not found", pullTask.ID)
	}

	top, err := store.TopPendingTask(ctx)
	fatalIfErr("TopPendingTask", err)
	if top == nil {
		t.Fatalf("TopPendingTask: got nil, want a pending task")
	}
	checkArea("TopPendingTask", top.Area)

	got, err := store.GetTaskByID(ctx, base.ID)
	fatalIfErr("GetTaskByID", err)
	checkArea("GetTaskByID", got.Area)

	// --- read paths that mutate state, exercised last ---

	newTitle := "area updated title PG"
	updated, err := store.UpdateTask(ctx, pullTask.ID, gtd.UpdateTaskParams{Title: &newTitle})
	fatalIfErr("UpdateTask", err)
	checkArea("UpdateTask", updated.Area)

	statusUpdated, err := store.UpdateTaskStatus(ctx, dueTask.ID, gtd.TaskStatusCancelled)
	fatalIfErr("UpdateTaskStatus", err)
	checkArea("UpdateTaskStatus", statusUpdated.Area)

	begun, err := store.BeginTask(ctx, withProj.ID)
	fatalIfErr("BeginTask", err)
	checkArea("BeginTask", begun.Area)

	completed, err := store.CompleteTask(ctx, base.ID, nil)
	fatalIfErr("CompleteTask", err)
	checkArea("CompleteTask", completed.Area)

	if _, err := store.CompleteTask(ctx, withProj.ID, nil); err != nil {
		t.Fatalf("CompleteTask withProj: %v", err)
	}
	recent, err := store.RecentCompletedTasks(ctx, proj.ID, 10)
	fatalIfErr("RecentCompletedTasks", err)
	recentFound := false
	for _, tk := range recent {
		if tk.ID == withProj.ID {
			recentFound = true
			checkArea("RecentCompletedTasks", tk.Area)
		}
	}
	if !recentFound {
		t.Errorf("RecentCompletedTasks: withProj task %s not found", withProj.ID)
	}
}
