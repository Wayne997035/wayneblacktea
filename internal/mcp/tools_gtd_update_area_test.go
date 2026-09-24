package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// storedArea reads tasks.area straight from the database. area is not in
// the task response shape (#187 keeps it out of selectCols), so the column is
// the only oracle — and reading it directly means a handler that reported
// success without writing cannot pass.
func storedArea(t *testing.T, d *wbtsqlite.DB, id uuid.UUID) string {
	t.Helper()
	var area string
	if err := d.QueryRowContext(context.Background(),
		`SELECT area FROM tasks WHERE id = ?1`, id.String()).Scan(&area); err != nil {
		t.Fatalf("read area: %v", err)
	}
	return area
}

func seedTaskInArea(t *testing.T, s *Server, area string) uuid.UUID {
	t.Helper()
	task, err := s.gtd.CreateTask(context.Background(), gtd.CreateTaskParams{
		Title: "area probe " + uuid.NewString(), Area: area,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return task.ID
}

func updateTaskText(t *testing.T, s *Server, args map[string]any) string {
	t.Helper()
	r := callTool(t, "update_task", args, s.handleUpdateTask)
	if r.IsError {
		return "ERROR: " + resultText(r)
	}
	return resultText(r)
}

// TestUpdateTask_AreaReclassifies is the feature itself: before it, area was
// write-once, and a mis-classified task could only be fixed in the database.
func TestUpdateTask_AreaReclassifies(t *testing.T) {
	t.Parallel()
	s, d := newTestWorkSessionServerWithDB(t)
	id := seedTaskInArea(t, s, "coverones")

	if out := updateTaskText(t, s, map[string]any{"task_id": id.String(), "area": "wbt"}); strings.HasPrefix(out, "ERROR") {
		t.Fatalf("area-only update must be accepted, got %s", out)
	}
	if got := storedArea(t, d, id); got != "wbt" {
		t.Errorf("area = %q after update, want wbt", got)
	}
}

// TestUpdateTask_OmittingAreaPreservesIt is the guard for the failure this
// change could most easily introduce. tasks.area is NOT NULL DEFAULT
// 'unsorted'; an update path that wrote a zero value instead of the stored one
// would move every task it touched into 'unsorted', and nothing would report
// it — the task is still there, just silently reclassified.
func TestUpdateTask_OmittingAreaPreservesIt(t *testing.T) {
	t.Parallel()
	s, d := newTestWorkSessionServerWithDB(t)
	id := seedTaskInArea(t, s, "ai-arch")

	if out := updateTaskText(t, s, map[string]any{"task_id": id.String(), "title": "renamed"}); strings.HasPrefix(out, "ERROR") {
		t.Fatalf("title update failed: %s", out)
	}
	if got := storedArea(t, d, id); got != "ai-arch" {
		t.Errorf("area = %q after an update that did not mention it, want ai-arch — omission reclassified the task", got)
	}
}

// TestUpdateTask_UnknownAreaIsRejectedAndNothingChanges: no foreign key backs
// tasks.area (red line #9), so the handler's lookup is the only thing between
// a typo and a task parked in a bucket no counts query surfaces.
func TestUpdateTask_UnknownAreaIsRejectedAndNothingChanges(t *testing.T) {
	t.Parallel()
	s, d := newTestWorkSessionServerWithDB(t)
	id := seedTaskInArea(t, s, "wbt")

	out := updateTaskText(t, s, map[string]any{
		"task_id": id.String(), "area": "no-such-area", "title": "should not land",
	})
	if !strings.Contains(out, "unknown area") {
		t.Fatalf("want an unknown-area refusal, got %s", out)
	}
	if strings.Contains(out, "no-such-area") {
		t.Error("the refusal echoed caller-supplied text back")
	}
	if got := storedArea(t, d, id); got != "wbt" {
		t.Errorf("area = %q after a rejected update, want wbt", got)
	}
	task, err := s.gtd.GetTaskByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if task.Title == "should not land" {
		t.Error("a rejected update still wrote its other fields")
	}
}
