package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// areaFixtureSQLite holds the store handle and every seeded task/project
// TestTaskArea_ReturnedByEveryReadPath's subtests read from. Each subtest
// body lives in its own top-level function (areaCaseSQLite*) rather than a
// closure literal here, so gocyclo scores it separately from the test
// function that just seeds fixtures and dispatches — a closure nested
// inside the test function would instead add its branches to the test
// function's own complexity count.
type areaFixtureSQLite struct {
	store    *sqlite.GTDStore
	ctx      context.Context
	wantArea string
	proj     *db.Project
	base     *db.Task
	withProj *db.Task
	dueTask  *db.Task
	pullTask *db.Task
	due      time.Time
}

func checkTaskArea(t *testing.T, step, gotArea, wantArea string) {
	t.Helper()
	if gotArea != wantArea {
		t.Errorf("%s: Area = %q, want %q", step, gotArea, wantArea)
	}
}

func findTaskArea(t *testing.T, tasks []db.Task, id uuid.UUID, step string) string {
	t.Helper()
	for _, tk := range tasks {
		if tk.ID == id {
			return tk.Area
		}
	}
	t.Fatalf("%s: task %s not found", step, id)
	return ""
}

func areaCaseSQLiteCreateTask(t *testing.T, fx areaFixtureSQLite) {
	checkTaskArea(t, "CreateTask", fx.base.Area, fx.wantArea)
}

func areaCaseSQLiteTasksNil(t *testing.T, fx areaFixtureSQLite) {
	got, err := fx.store.Tasks(fx.ctx, nil)
	if err != nil {
		t.Fatalf("Tasks(nil): %v", err)
	}
	checkTaskArea(t, "Tasks(nil)", findTaskArea(t, got, fx.base.ID, "Tasks(nil)"), fx.wantArea)
}

func areaCaseSQLiteTasksProject(t *testing.T, fx areaFixtureSQLite) {
	got, err := fx.store.Tasks(fx.ctx, &fx.proj.ID)
	if err != nil {
		t.Fatalf("Tasks(project): %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("Tasks(project): got 0 rows for project %s", fx.proj.ID)
	}
	checkTaskArea(t, "Tasks(project)", got[0].Area, fx.wantArea)
}

func areaCaseSQLiteTasksFiltered(t *testing.T, fx areaFixtureSQLite) {
	got, err := fx.store.TasksFiltered(fx.ctx, gtd.TaskFilter{Area: fx.wantArea, Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("TasksFiltered: got 0 rows for area %q", fx.wantArea)
	}
	for _, tk := range got {
		checkTaskArea(t, "TasksFiltered/"+tk.ID.String(), tk.Area, fx.wantArea)
	}
}

func areaCaseSQLiteTasksByProjectAllStatuses(t *testing.T, fx areaFixtureSQLite) {
	got, err := fx.store.TasksByProjectAllStatuses(fx.ctx, fx.proj.ID)
	if err != nil {
		t.Fatalf("TasksByProjectAllStatuses: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("TasksByProjectAllStatuses: got 0 rows for project %s", fx.proj.ID)
	}
	checkTaskArea(t, "TasksByProjectAllStatuses", got[0].Area, fx.wantArea)
}

func areaCaseSQLiteTasksByDueDateRange(t *testing.T, fx areaFixtureSQLite) {
	got, err := fx.store.TasksByDueDateRange(fx.ctx, time.Now(), fx.due.Add(time.Hour))
	if err != nil {
		t.Fatalf("TasksByDueDateRange: %v", err)
	}
	checkTaskArea(t, "TasksByDueDateRange", findTaskArea(t, got, fx.dueTask.ID, "TasksByDueDateRange"), fx.wantArea)
}

func areaCaseSQLiteUpcomingTasks(t *testing.T, fx areaFixtureSQLite) {
	got, err := fx.store.UpcomingTasks(fx.ctx, time.Now(), 7, 50)
	if err != nil {
		t.Fatalf("UpcomingTasks: %v", err)
	}
	checkTaskArea(t, "UpcomingTasks", findTaskArea(t, got, fx.dueTask.ID, "UpcomingTasks"), fx.wantArea)
}

func areaCaseSQLiteTasksForTimeline(t *testing.T, fx areaFixtureSQLite) {
	got, err := fx.store.TasksForTimeline(fx.ctx, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("TasksForTimeline: %v", err)
	}
	checkTaskArea(t, "TasksForTimeline", findTaskArea(t, got, fx.base.ID, "TasksForTimeline"), fx.wantArea)
}

func areaCaseSQLitePullForwardTasks(t *testing.T, fx areaFixtureSQLite) {
	got, err := fx.store.PullForwardTasks(fx.ctx, time.Now())
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	checkTaskArea(t, "PullForwardTasks", findTaskArea(t, got, fx.pullTask.ID, "PullForwardTasks"), fx.wantArea)
}

func areaCaseSQLiteTopPendingTask(t *testing.T, fx areaFixtureSQLite) {
	got, err := fx.store.TopPendingTask(fx.ctx)
	if err != nil {
		t.Fatalf("TopPendingTask: %v", err)
	}
	if got == nil {
		t.Fatalf("TopPendingTask: got nil, want a pending task")
	}
	checkTaskArea(t, "TopPendingTask", got.Area, fx.wantArea)
}

func areaCaseSQLiteGetTaskByID(t *testing.T, fx areaFixtureSQLite) {
	got, err := fx.store.GetTaskByID(fx.ctx, fx.base.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	checkTaskArea(t, "GetTaskByID", got.Area, fx.wantArea)
}

func areaCaseSQLiteUpdateTask(t *testing.T, fx areaFixtureSQLite) {
	newTitle := "area updated title"
	got, err := fx.store.UpdateTask(fx.ctx, fx.pullTask.ID, gtd.UpdateTaskParams{Title: &newTitle})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	checkTaskArea(t, "UpdateTask", got.Area, fx.wantArea)
}

func areaCaseSQLiteUpdateTaskStatus(t *testing.T, fx areaFixtureSQLite) {
	got, err := fx.store.UpdateTaskStatus(fx.ctx, fx.dueTask.ID, gtd.TaskStatusCancelled)
	if err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	checkTaskArea(t, "UpdateTaskStatus", got.Area, fx.wantArea)
}

func areaCaseSQLiteBeginTask(t *testing.T, fx areaFixtureSQLite) {
	got, err := fx.store.BeginTask(fx.ctx, fx.withProj.ID)
	if err != nil {
		t.Fatalf("BeginTask: %v", err)
	}
	checkTaskArea(t, "BeginTask", got.Area, fx.wantArea)
}

func areaCaseSQLiteCompleteTask(t *testing.T, fx areaFixtureSQLite) {
	got, err := fx.store.CompleteTask(fx.ctx, fx.base.ID, nil)
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	checkTaskArea(t, "CompleteTask", got.Area, fx.wantArea)
}

func areaCaseSQLiteRecentCompletedTasks(t *testing.T, fx areaFixtureSQLite) {
	if _, err := fx.store.CompleteTask(fx.ctx, fx.withProj.ID, nil); err != nil {
		t.Fatalf("CompleteTask withProj: %v", err)
	}
	got, err := fx.store.RecentCompletedTasks(fx.ctx, fx.proj.ID, 10)
	if err != nil {
		t.Fatalf("RecentCompletedTasks: %v", err)
	}
	checkTaskArea(t, "RecentCompletedTasks", findTaskArea(t, got, fx.withProj.ID, "RecentCompletedTasks"), fx.wantArea)
}

func areaCaseSQLiteImportTaskRoundTrip(t *testing.T, fx areaFixtureSQLite) {
	// ImportTask writes area verbatim; GetTaskByID must read the same value
	// back (F0925-17's second acceptance clause).
	imported := db.Task{
		ID: uuid.New(), Title: "area imported", Status: "pending", Priority: 3,
		Area:      fx.wantArea,
		CreatedAt: pgTimeVal(time.Now()), UpdatedAt: pgTimeVal(time.Now()),
	}
	if err := fx.store.ImportTask(fx.ctx, imported); err != nil {
		t.Fatalf("ImportTask: %v", err)
	}
	reimported, err := fx.store.GetTaskByID(fx.ctx, imported.ID)
	if err != nil {
		t.Fatalf("GetTaskByID(imported): %v", err)
	}
	checkTaskArea(t, "ImportTask round-trip", reimported.Area, fx.wantArea)
}

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

	proj, err := s.CreateProject(ctx, gtd.CreateProjectParams{Name: "area-test-project", Title: "area test project"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	base, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "area base", Area: wantArea})
	if err != nil {
		t.Fatalf("CreateTask base: %v", err)
	}
	withProj, err := s.CreateTask(ctx, gtd.CreateTaskParams{
		Title: "area proj task", ProjectID: &proj.ID, Area: wantArea, Assignee: "claude",
	})
	if err != nil {
		t.Fatalf("CreateTask withProj: %v", err)
	}
	due := time.Now().Add(2 * time.Hour)
	dueTask, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "area due", Area: wantArea, DueDate: &due})
	if err != nil {
		t.Fatalf("CreateTask dueTask: %v", err)
	}
	imp := int16(1)
	pullTask, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "area pull", Area: wantArea, Importance: &imp})
	if err != nil {
		t.Fatalf("CreateTask pullTask: %v", err)
	}

	fx := areaFixtureSQLite{
		store: s, ctx: ctx, wantArea: wantArea,
		proj: proj, base: base, withProj: withProj, dueTask: dueTask, pullTask: pullTask, due: due,
	}

	// Each case is one gtd.StoreIface method (iface.go's 15 db.Task-returning
	// methods) plus ImportTask; read-only cases first, state-mutating ones
	// last, in the same order the original sequential version exercised
	// them — subtests run sequentially (none call t.Parallel()), so that
	// ordering is preserved.
	cases := []struct {
		name string
		run  func(t *testing.T, fx areaFixtureSQLite)
	}{
		{"CreateTask", areaCaseSQLiteCreateTask},
		{"Tasks(nil)", areaCaseSQLiteTasksNil},
		{"Tasks(project)", areaCaseSQLiteTasksProject},
		{"TasksFiltered", areaCaseSQLiteTasksFiltered},
		{"TasksByProjectAllStatuses", areaCaseSQLiteTasksByProjectAllStatuses},
		{"TasksByDueDateRange", areaCaseSQLiteTasksByDueDateRange},
		{"UpcomingTasks", areaCaseSQLiteUpcomingTasks},
		{"TasksForTimeline", areaCaseSQLiteTasksForTimeline},
		{"PullForwardTasks", areaCaseSQLitePullForwardTasks},
		{"TopPendingTask", areaCaseSQLiteTopPendingTask},
		{"GetTaskByID", areaCaseSQLiteGetTaskByID},
		{"UpdateTask", areaCaseSQLiteUpdateTask},
		{"UpdateTaskStatus", areaCaseSQLiteUpdateTaskStatus},
		{"BeginTask", areaCaseSQLiteBeginTask},
		{"CompleteTask", areaCaseSQLiteCompleteTask},
		{"RecentCompletedTasks", areaCaseSQLiteRecentCompletedTasks},
		{"ImportTask round-trip", areaCaseSQLiteImportTaskRoundTrip},
	}

	for _, tc := range cases {
		run := tc.run
		t.Run(tc.name, func(t *testing.T) { run(t, fx) })
	}
}
