package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/mark3labs/mcp-go/mcp"
)

// --- D3: area is mechanically required, not required-by-prose ---

// TestAddTaskSchema_AreaRequired is the guard for the decision that area
// carries mcp.Required() rather than the word "Required" in its description.
//
// The counter-example is one line above it in tools_gtd.go: due_date's
// description reads "Required. RFC3339, ..." and carries no mcp.Required(),
// and 14% of open rows have it empty with nothing ever complaining. Prose
// requirements drift silently; schema requirements are refused by the client.
//
// Mutation check for this test: delete mcp.Required() from the area line in
// tools_gtd.go's add_task registration and this test goes red. Nothing else
// in the suite does — the handler itself never sees the difference, because a
// conforming client stops the call before it arrives.
func TestAddTaskSchema_AreaRequired(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	tool := s.MCPServer().GetTool("add_task")
	if tool == nil {
		t.Fatal("add_task not registered on MCPServer()")
	}

	if !slices.Contains(tool.Tool.InputSchema.Required, "area") {
		t.Fatalf("add_task InputSchema.Required = %v, want it to contain %q",
			tool.Tool.InputSchema.Required, "area")
	}

	// Positive control for the assertion itself: if the Required list were
	// read from the wrong place (or were empty), the check above would pass
	// vacuously for any field. title has been required since the tool was
	// written, so its absence here means the probe is broken, not the code.
	if !slices.Contains(tool.Tool.InputSchema.Required, "title") {
		t.Fatalf("probe broken: add_task Required = %v, expected the long-standing %q entry",
			tool.Tool.InputSchema.Required, "title")
	}
}

// TestListTasksSchema_AreaNotRequired pins the other half: filtering is
// optional. A required area on list_tasks would break every existing caller
// and force a value on the one query — "everything still open" — that has no
// single area.
func TestListTasksSchema_AreaNotRequired(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	tool := s.MCPServer().GetTool("list_tasks")
	if tool == nil {
		t.Fatal("list_tasks not registered on MCPServer()")
	}
	if slices.Contains(tool.Tool.InputSchema.Required, "area") {
		t.Fatalf("list_tasks InputSchema.Required = %v, want %q absent",
			tool.Tool.InputSchema.Required, "area")
	}
	if _, ok := tool.Tool.InputSchema.Properties["area"]; !ok {
		t.Fatal("list_tasks InputSchema has no area property at all")
	}
}

// --- the one-line summary that rides on get_today_context ---

func TestAreasSummaryLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		areas []gtd.AreaCount
		err   error
		want  string
	}{
		{
			name: "renders in the order given, zero-count areas omitted",
			areas: []gtd.AreaCount{
				{Area: "wbt", Open: 166},
				{Area: "ai-arch", Open: 113},
				{Area: "coverones", Open: 0},
				{Area: "unsorted", Open: 103},
			},
			want: "wbt 166 / ai-arch 113 / unsorted 103",
		},
		{
			// An error must not look like an empty board. These are different
			// facts and the caller acts differently on each.
			name: "lookup error is visible, not blank",
			err:  errors.New("boom"),
			want: "unavailable",
		},
		{
			name:  "genuinely nothing open says so",
			areas: []gtd.AreaCount{{Area: "wbt", Open: 0}},
			want:  "none open",
		},
		{
			name:  "no areas configured at all",
			areas: nil,
			want:  "none open",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := areasSummaryLine(tc.areas, tc.err)
			if got != tc.want {
				t.Fatalf("areasSummaryLine() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTodayContextCarriesAreasSummary proves the line actually reaches the
// get_today_context payload. The resource alone is not enough: a resource
// nobody reads answers nothing, and get_today_context is the call the
// protocol makes every session start with.
func TestTodayContextCarriesAreasSummary(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	res, err := s.handleGetTodayContext(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleGetTodayContext: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(resultText(res)), &payload); err != nil {
		t.Fatalf("unmarshal today context: %v", err)
	}
	v, ok := payload["areas_summary"]
	if !ok {
		t.Fatal("get_today_context payload has no areas_summary key")
	}
	if _, ok := v.(string); !ok {
		t.Fatalf("areas_summary = %#v, want a flat string (nested objects cost every session)", v)
	}
}

// --- unknown areas are refused, not silently empty ---

// TestListTasksRejectsUnknownArea is the anti-silence guard. Passing an
// unknown area straight through to the query returns an empty list, and an
// empty list caused by a typo reads exactly like an area that is finished —
// the class of wrong answer this whole column exists to end.
func TestListTasksRejectsUnknownArea(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	res, err := s.handleListTasks(context.Background(), ListTasksArgs{Area: "no-such-area"})
	if err != nil {
		t.Fatalf("handleListTasks returned a transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("list_tasks(area=\"no-such-area\") returned a normal result, want a tool error; body=%q",
			resultText(res))
	}
	if body := resultText(res); !strings.Contains(body, "unknown area") {
		t.Fatalf("error body = %q, want it to mention %q", body, "unknown area")
	}

	// Positive control: a seeded area from migration 000079 must NOT be
	// rejected. Without this, a handler that rejected every area — including
	// the real ones — would still pass the assertion above.
	okRes, err := s.handleListTasks(context.Background(), ListTasksArgs{Area: "wbt"})
	if err != nil {
		t.Fatalf("handleListTasks(area=wbt): %v", err)
	}
	if okRes.IsError {
		t.Fatalf("list_tasks(area=%q) was rejected; body=%q", "wbt", resultText(okRes))
	}
}

func TestAddTaskRejectsUnknownArea(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	res, err := s.handleAddTask(context.Background(), AddTaskArgs{
		Title: "area probe",
		Area:  "no-such-area",
	})
	if err != nil {
		t.Fatalf("handleAddTask returned a transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("add_task with an unknown area succeeded; body=%q", resultText(res))
	}
}

// --- counts ---

// TestTaskAreaCountsKeepsEmptyAreas pins the LEFT JOIN. Moving the status or
// workspace predicate from the ON clause into WHERE turns it back into an
// inner join, and areas holding nothing disappear — including 'unsorted'
// reaching zero, which is the single most useful thing the breakdown can say.
func TestTaskAreaCountsKeepsEmptyAreas(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	counts, err := s.gtd.TaskAreaCounts(context.Background())
	if err != nil {
		t.Fatalf("TaskAreaCounts: %v", err)
	}
	if len(counts) == 0 {
		t.Fatal("TaskAreaCounts returned nothing; migration 000079 seeds seven rows")
	}

	seen := map[string]bool{}
	for _, c := range counts {
		seen[c.Area] = true
		if c.Open != c.Pending+c.InProgress {
			t.Fatalf("area %q: Open=%d but Pending+InProgress=%d", c.Area, c.Open, c.Pending+c.InProgress)
		}
	}
	// A fresh test database has no tasks at all, so every one of these is a
	// zero row — exactly the case an inner join would drop.
	for _, want := range []string{"wbt", "ai-arch", "wbt3", "unsorted"} {
		if !seen[want] {
			t.Fatalf("TaskAreaCounts omitted seeded area %q (got %v) — empty areas must still be listed",
				want, seen)
		}
	}
}

// TestGTDAreasResourceIsSmall guards the reason this is a resource and not a
// page of rows: one limit=60 list_tasks page measured 21,580 bytes against a
// few hundred here. If the payload ever grows past a kilobyte, the tradeoff
// that justified the design has stopped holding.
func TestGTDAreasResourceIsSmall(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

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
	if n := len(tc.Text); n > 1000 {
		t.Fatalf("gtd/areas payload is %d bytes, want <= 1000", n)
	}

	var payload gtdAreasResource
	if err := json.Unmarshal([]byte(tc.Text), &payload); err != nil {
		t.Fatalf("unmarshal gtd/areas: %v", err)
	}
	var sum int
	for _, a := range payload.Areas {
		sum += a.Open
	}
	if payload.TotalOpen != sum {
		t.Fatalf("total_open=%d but the per-area numbers sum to %d — the parts must add up to the whole",
			payload.TotalOpen, sum)
	}
}
