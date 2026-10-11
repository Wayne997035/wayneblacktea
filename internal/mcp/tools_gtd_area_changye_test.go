package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/mark3labs/mcp-go/mcp"
)

// [F1011-01] The tool-level view of migration 000088: the new 'changye' area
// is accepted by add_task / update_task and listed by wayneblacktea://gtd/areas,
// while near-miss spellings are still refused at the write boundary. The
// SQLite-backed server here is migrated through the real embedded migrations,
// so deleting the INSERT from migrations/sqlite/000088_task_area_changye.up.sql
// turns every positive case below red.

const (
	areaChangye = "changye"
	areaMisc    = "misc"
)

// taskRowCount counts every tasks row, so a rejected write is provable as
// "no row was added" and not merely "an error was returned".
func taskRowCount(t *testing.T, d *wbtsqlite.DB) int {
	t.Helper()
	var n int
	if err := d.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM tasks`).Scan(&n); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	return n
}

// changyeAddTaskArgs builds add_task arguments that get past the vagueness
// wrapper (due_date + kind=chore, see validation_parity_test.go:524-541), so
// the only variable under test is area.
func changyeAddTaskArgs(area string) map[string]any {
	return map[string]any{
		"title":    "changye area probe",
		"due_date": "2026-12-31T00:00:00Z",
		"kind":     "chore",
		"area":     area,
	}
}

func TestAddTask_ChangyeAreaAccepted(t *testing.T) {
	t.Parallel()
	s, d := newTestWorkSessionServerWithDB(t)

	r := callTool(t, s, "add_task", changyeAddTaskArgs(areaChangye), s.handleAddTask)
	if r.IsError {
		t.Fatalf("add_task(area=changye) was rejected: %s", resultText(r))
	}
	id := idOnly(t, resultText(r))
	if got := storedArea(t, d, id); got != areaChangye {
		t.Errorf("stored area = %q, want changye", got)
	}
}

func TestAddTask_ChangyeNearMissAreasRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		area     string
		wantBody string
	}{
		{name: "typo", area: "changey", wantBody: "unknown area"},
		{name: "capitalised", area: "Changye", wantBody: "unknown area"},
		{name: "empty", area: "", wantBody: "area is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, d := newTestWorkSessionServerWithDB(t)
			before := taskRowCount(t, d)

			r := callTool(t, s, "add_task", changyeAddTaskArgs(tt.area), s.handleAddTask)
			if !r.IsError {
				t.Fatalf("add_task(area=%q) succeeded, want a rejection; body=%q", tt.area, resultText(r))
			}
			if body := resultText(r); !strings.Contains(body, tt.wantBody) {
				t.Errorf("add_task(area=%q) body = %q, want it to contain %q", tt.area, body, tt.wantBody)
			}
			if after := taskRowCount(t, d); after != before {
				t.Errorf("tasks rows %d -> %d after a rejected add_task, want unchanged", before, after)
			}
		})
	}
}

// TestUpdateTask_ChangyeAreaMovesFromMisc is the production move this
// migration exists for: a task parked in misc is reclassified into changye.
// The typo case runs first on the same task and must leave it in misc.
func TestUpdateTask_ChangyeAreaMovesFromMisc(t *testing.T) {
	t.Parallel()
	s, d := newTestWorkSessionServerWithDB(t)
	id := seedTaskInArea(t, s, areaMisc)

	out := updateTaskText(t, s, map[string]any{"task_id": id.String(), "area": "changey"})
	if !strings.HasPrefix(out, "ERROR:") || !strings.Contains(out, "unknown area") {
		t.Fatalf("update_task(area=changey) = %q, want an ERROR containing %q", out, "unknown area")
	}
	if got := storedArea(t, d, id); got != areaMisc {
		t.Fatalf("area = %q after a rejected update, want misc", got)
	}

	if out := updateTaskText(t, s, map[string]any{"task_id": id.String(), "area": areaChangye}); strings.HasPrefix(out, "ERROR") {
		t.Fatalf("update_task(area=changye) was rejected: %s", out)
	}
	if got := storedArea(t, d, id); got != areaChangye {
		t.Errorf("area = %q after the move, want changye", got)
	}
}

// readGTDAreasResource returns the raw gtd/areas text and its decoded form.
func readGTDAreasResource(t *testing.T, s *Server) (string, gtdAreasResource) {
	t.Helper()
	contents, err := s.handleResourceGTDAreas(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceGTDAreas: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("got %d resource contents, want 1", len(contents))
	}
	tc, ok := contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("resource content is %T, want mcp.TextResourceContents", contents[0])
	}
	var payload gtdAreasResource
	if err := json.Unmarshal([]byte(tc.Text), &payload); err != nil {
		t.Fatalf("unmarshal gtd/areas: %v", err)
	}
	return tc.Text, payload
}

// TestGTDAreasResource_ChangyeListed pins the resource side: one changye
// element, between intel-flow and misc, still inside the size budget that
// TestGTDAreasResourceIsSmall guards, with total_open equal to the sum of the
// per-area numbers.
func TestGTDAreasResource_ChangyeListed(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	text, payload := readGTDAreasResource(t, s)

	if n := len(text); n > 1000 {
		t.Errorf("gtd/areas payload is %d bytes, want <= 1000", n)
	}

	indexOf := func(area string) int {
		return slices.IndexFunc(payload.Areas, func(a gtd.AreaCount) bool { return a.Area == area })
	}
	var sum, changyeEntries int
	for _, a := range payload.Areas {
		sum += a.Open
		if a.Area == areaChangye {
			changyeEntries++
		}
	}
	if changyeEntries != 1 {
		t.Fatalf("gtd/areas lists changye %d times, want exactly once: %+v", changyeEntries, payload.Areas)
	}

	intel, changye, misc := indexOf("intel-flow"), indexOf(areaChangye), indexOf(areaMisc)
	if intel < 0 || misc < 0 || intel >= changye || changye >= misc {
		t.Errorf("order: intel-flow=%d changye=%d misc=%d, want 0 <= intel-flow < changye < misc", intel, changye, misc)
	}
	want := gtd.AreaCount{Area: areaChangye, Label: "Changye Nightmart"}
	if got := payload.Areas[changye]; got != want {
		t.Errorf("changye entry = %+v, want %+v (zero counts on a fresh database)", got, want)
	}
	if payload.TotalOpen != sum {
		t.Errorf("total_open=%d but the per-area numbers sum to %d", payload.TotalOpen, sum)
	}
}
