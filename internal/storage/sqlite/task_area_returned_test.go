package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// TestTaskArea_ReturnedByEveryReadPath is the SQLite regression test for
// F0925-17: every gtd.StoreIface method that returns a db.Task or
// []db.Task must carry the task's area (migration 000079), not the
// zero-value empty string. Before this fix, tasksSelectCols (gtd.go) and
// scanTask omitted the area column even though db.Task already had an
// Area field (sqlc regen), so every read path returned "" regardless of
// what was actually stored.
func TestTaskArea_ReturnedByEveryReadPath(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openMem(t, "")
	ctx := context.Background()

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

	proj, err := s.CreateProject(ctx, gtd.CreateProjectParams{Name: "area-test-project", Title: "area test project"})
	fatalIfErr("CreateProject", err)

	base, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "area base", Area: wantArea})
	fatalIfErr("CreateTask base", err)
	checkArea("CreateTask", base.Area)

	withProj, err := s.CreateTask(ctx, gtd.CreateTaskParams{
		Title: "area proj task", ProjectID: &proj.ID, Area: wantArea, Assignee: "claude",
	})
	fatalIfErr("CreateTask withProj", err)

	due := time.Now().Add(2 * time.Hour)
	dueTask, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "area due", Area: wantArea, DueDate: &due})
	fatalIfErr("CreateTask dueTask", err)

	imp := int16(1)
	pullTask, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "area pull", Area: wantArea, Importance: &imp})
	fatalIfErr("CreateTask pullTask", err)

	// --- read paths that don't mutate state, exercised first ---

	allPending, err := s.Tasks(ctx, nil)
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

	projTasks, err := s.Tasks(ctx, &proj.ID)
	fatalIfErr("Tasks(project)", err)
	if len(projTasks) == 0 {
		t.Fatalf("Tasks(project): got 0 rows for project %s", proj.ID)
	}
	checkArea("Tasks(project)", projTasks[0].Area)

	filtered, err := s.TasksFiltered(ctx, gtd.TaskFilter{Area: wantArea, Limit: 100})
	fatalIfErr("TasksFiltered", err)
	if len(filtered) == 0 {
		t.Fatalf("TasksFiltered: got 0 rows for area %q", wantArea)
	}
	for _, tk := range filtered {
		checkArea("TasksFiltered/"+tk.ID.String(), tk.Area)
	}

	allStatuses, err := s.TasksByProjectAllStatuses(ctx, proj.ID)
	fatalIfErr("TasksByProjectAllStatuses", err)
	if len(allStatuses) == 0 {
		t.Fatalf("TasksByProjectAllStatuses: got 0 rows for project %s", proj.ID)
	}
	checkArea("TasksByProjectAllStatuses", allStatuses[0].Area)

	dueRange, err := s.TasksByDueDateRange(ctx, time.Now(), due.Add(time.Hour))
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

	upcoming, err := s.UpcomingTasks(ctx, time.Now(), 7, 50)
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

	timeline, err := s.TasksForTimeline(ctx, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
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

	pulled, err := s.PullForwardTasks(ctx, time.Now())
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

	top, err := s.TopPendingTask(ctx)
	fatalIfErr("TopPendingTask", err)
	if top == nil {
		t.Fatalf("TopPendingTask: got nil, want a pending task")
	}
	checkArea("TopPendingTask", top.Area)

	got, err := s.GetTaskByID(ctx, base.ID)
	fatalIfErr("GetTaskByID", err)
	checkArea("GetTaskByID", got.Area)

	// --- read paths that mutate state, exercised last ---

	newTitle := "area updated title"
	updated, err := s.UpdateTask(ctx, pullTask.ID, gtd.UpdateTaskParams{Title: &newTitle})
	fatalIfErr("UpdateTask", err)
	checkArea("UpdateTask", updated.Area)

	statusUpdated, err := s.UpdateTaskStatus(ctx, dueTask.ID, gtd.TaskStatusCancelled)
	fatalIfErr("UpdateTaskStatus", err)
	checkArea("UpdateTaskStatus", statusUpdated.Area)

	begun, err := s.BeginTask(ctx, withProj.ID)
	fatalIfErr("BeginTask", err)
	checkArea("BeginTask", begun.Area)

	completed, err := s.CompleteTask(ctx, base.ID, nil)
	fatalIfErr("CompleteTask", err)
	checkArea("CompleteTask", completed.Area)

	if _, err := s.CompleteTask(ctx, withProj.ID, nil); err != nil {
		t.Fatalf("CompleteTask withProj: %v", err)
	}
	recent, err := s.RecentCompletedTasks(ctx, proj.ID, 10)
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

	// ImportTask writes area verbatim; GetTaskByID must read the same value
	// back (F0925-17's second acceptance clause).
	imported := db.Task{
		ID: uuid.New(), Title: "area imported", Status: "pending", Priority: 3,
		Area:      wantArea,
		CreatedAt: pgTimeVal(time.Now()), UpdatedAt: pgTimeVal(time.Now()),
	}
	fatalIfErr("ImportTask", s.ImportTask(ctx, imported))
	reimported, err := s.GetTaskByID(ctx, imported.ID)
	fatalIfErr("GetTaskByID(imported)", err)
	checkArea("ImportTask round-trip", reimported.Area)
}
