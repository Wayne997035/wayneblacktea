package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// Texts this file asserts on more than twice. Named so the package-wide
// three-occurrence goconst line is not crossed by tests that already repeat them.
const (
	projectNotFoundText = "project not found"
	taskNotFoundText    = "task not found"
	rejectedTitle       = "should not land"
	schemaTypeString    = "string"
)

// update_task project_id. Semantics (decision ea7e6826): key absent = unchanged,
// "" = clear to NULL, otherwise the UUID of a project that exists in the
// caller's workspace. The MCP layer's own job is to hand the store the right one
// of three states (TestUpdateTask_ProjectIDParse), to map the store's
// gtd.ErrProjectNotFound onto a static string
// (TestUpdateTask_ProjectNotFoundMapping), and to advertise the argument
// (TestUpdateTask_ProjectIDSchema). Whether the store then writes the column is
// the store's job; the tests that read the column back (attach, clear, unknown
// project) are the ones that go red on a store that ignores gtd.UpdateTaskParams.Project.

// probeProject creates a project straight through the store. Fixtures never go
// through update_task: a test about update_task's project_id cannot also rely on
// it to build its own starting state.
func probeProject(t *testing.T, s *Server) uuid.UUID {
	t.Helper()
	p, err := s.gtd.CreateProject(context.Background(), gtd.CreateProjectParams{
		Name:  "s1011-proj-" + uuid.NewString(),
		Title: "project_id probe project",
		Area:  "engineering",
	})
	if err != nil {
		t.Fatalf("CreateProject fixture: %v", err)
	}
	return p.ID
}

// probeTask creates a task straight through the store, in projectID (nil =
// no project) and area ("" = the store default).
func probeTask(t *testing.T, s *Server, projectID *uuid.UUID, area string) uuid.UUID {
	t.Helper()
	task, err := s.gtd.CreateTask(context.Background(), gtd.CreateTaskParams{
		ProjectID: projectID,
		Title:     "project_id probe " + uuid.NewString(),
		Area:      area,
	})
	if err != nil {
		t.Fatalf("CreateTask fixture: %v", err)
	}
	return task.ID
}

// storedProjectID reads tasks.project_id straight from the database, as
// storedArea does for area. A handler that reported success without writing
// cannot pass a check built on this.
func storedProjectID(t *testing.T, d *wbtsqlite.DB, id uuid.UUID) *uuid.UUID {
	t.Helper()
	var raw sql.NullString
	if err := d.QueryRowContext(context.Background(),
		`SELECT project_id FROM tasks WHERE id = ?1`, id.String()).Scan(&raw); err != nil {
		t.Fatalf("read project_id fixture query: %v", err)
	}
	if !raw.Valid {
		return nil
	}
	pid, err := uuid.Parse(raw.String)
	if err != nil {
		t.Fatalf("stored project_id %q fixture is not a UUID: %v", raw.String, err)
	}
	return &pid
}

func projectIDText(id *uuid.UUID) string {
	if id == nil {
		return "NULL"
	}
	return id.String()
}

func sameProjectID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// wantStoredProject asserts the stored project_id of task id. when names the
// step so a failure says which update left the row wrong.
func wantStoredProject(t *testing.T, d *wbtsqlite.DB, id uuid.UUID, want *uuid.UUID, when string) {
	t.Helper()
	if got := storedProjectID(t, d, id); !sameProjectID(got, want) {
		t.Errorf("%s: stored project_id = %s, want %s", when, projectIDText(got), projectIDText(want))
	}
}

// ackProjectID decodes update_task's ack and returns its project_id (nil = JSON
// null). Callers check IsError first; a non-JSON error text is not an ack.
func ackProjectID(t *testing.T, r *mcpmsg.CallToolResult) *uuid.UUID {
	t.Helper()
	var ack struct {
		ProjectID *string `json:"project_id"`
	}
	if err := json.Unmarshal([]byte(resultText(r)), &ack); err != nil {
		t.Fatalf("ack is not a JSON object (%v), want a task ack; raw=%s", err, resultText(r))
	}
	if ack.ProjectID == nil {
		return nil
	}
	pid, err := uuid.Parse(*ack.ProjectID)
	if err != nil {
		t.Fatalf("ack project_id %q is not a UUID (%v), want a UUID or null", *ack.ProjectID, err)
	}
	return &pid
}

// listedUnder reports whether list_tasks{project_id: project} returns task.
func listedUnder(t *testing.T, s *Server, project, task uuid.UUID) bool {
	t.Helper()
	r := callListTasks(t, s, map[string]any{"project_id": project.String()})
	if r.IsError {
		t.Errorf("list_tasks(project %s): IsError = true (%s), want a task page", project, resultText(r))
		return false
	}
	type row struct {
		ID string `json:"id"`
	}
	var page struct {
		Tasks []row `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(resultText(r)), &page); err != nil {
		t.Errorf("list_tasks(project %s): response is not JSON (%v), want {\"tasks\":[...]}", project, err)
		return false
	}
	return slices.ContainsFunc(page.Tasks, func(x row) bool { return x.ID == task.String() })
}

// projectRefState names which of the three states a ProjectRef is in.
func projectRefState(r *gtd.ProjectRef) string {
	switch {
	case r == nil:
		return "omitted"
	case r.ID == nil:
		return "clear"
	default:
		return "set " + r.ID.String()
	}
}

// TestUpdateTask_ProjectIDParse is the MCP layer's own contract with the store,
// asserted without a store: which of the three gtd.UpdateTaskParams.Project
// states each project_id input becomes. [F1011-14]
//
// It calls parseUpdateTaskArgs, the function handleUpdateTask calls, and not the
// helper it delegates to: a test of the helper alone leaves the line that
// assigns the helper's result to p.Project unguarded, and a store that ignores
// Project (as this branch's SQLite store did) cannot show that line is wrong.
func TestUpdateTask_ProjectIDParse(t *testing.T) {
	t.Parallel()
	valid := uuid.New()
	str := func(s string) *string { return &s }

	cases := []struct {
		name      string
		in        *string
		wantState string
		wantMsg   string
	}{
		{"omitted leaves project alone", nil, "omitted", ""},
		{"empty string clears", str(""), "clear", ""},
		{"valid uuid sets that project", str(valid.String()), "set " + valid.String(), ""},
		{"nil uuid is a set, not a clear", str(uuid.Nil.String()), "set " + uuid.Nil.String(), ""},
		{"malformed text is rejected", str("not-a-uuid"), "omitted", errMsgInvalidProjectIDUUID},
		{"whitespace only is rejected, not a clear", str("  "), "omitted", errMsgInvalidProjectIDUUID},
		{"8-char prefix is rejected, no prefix lookup for projects", str(valid.String()[:8]), "omitted", errMsgInvalidProjectIDUUID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p, msg := parseUpdateTaskArgs(UpdateTaskArgs{ProjectID: tc.in})
			if msg != tc.wantMsg {
				t.Errorf("parseUpdateTaskArgs message = %q, want %q", msg, tc.wantMsg)
			}
			if got := projectRefState(p.Project); got != tc.wantState {
				t.Errorf("parseUpdateTaskArgs Project state = %q, want %q", got, tc.wantState)
			}
		})
	}

	// updateTaskParamsIsEmpty must count a project-only call as a field, or
	// `project_id:""` alone (the clear) is refused as "at least one field is required".
	t.Run("project alone is a field", func(t *testing.T) {
		t.Parallel()
		for _, in := range []*string{str(""), str(valid.String())} {
			p, msg := parseUpdateTaskArgs(UpdateTaskArgs{ProjectID: in})
			if msg != "" {
				t.Fatalf("parseUpdateTaskArgs(%q) message = %q, want none", *in, msg)
			}
			if updateTaskParamsIsEmpty(p) {
				t.Errorf("updateTaskParamsIsEmpty(project_id=%q alone) = true, want false", *in)
			}
		}
	})
}

// TestUpdateTask_ProjectIDAttachThenListed: the happy path end to end. The
// failure this guards is the silent one measured before the change: update_task
// answered success with `"project_id":null` and wrote nothing. The task must be
// listed under P afterwards, and not under another project Q (a move, not a
// copy).
func TestUpdateTask_ProjectIDAttachThenListed(t *testing.T) {
	t.Parallel()
	s, d := newTestWorkSessionServerWithDB(t)
	p, q := probeProject(t, s), probeProject(t, s)
	task := probeTask(t, s, nil, "")

	r := callUpdateTask(t, s, map[string]any{"task_id": task.String(), "project_id": p.String()})
	if r.IsError {
		t.Fatalf("update_task{project_id only}: IsError = true (%s), want success", resultText(r))
	}
	if got := ackProjectID(t, r); !sameProjectID(got, &p) {
		t.Errorf("ack project_id = %s, want %s", projectIDText(got), p)
	}
	wantStoredProject(t, d, task, &p, "after attach")
	if !listedUnder(t, s, p, task) {
		t.Errorf("list_tasks(project %s) lists the task = false, want true", p)
	}
	if listedUnder(t, s, q, task) {
		t.Errorf("list_tasks(project %s, a different project) lists the task = true, want false", q)
	}
}

// TestUpdateTask_OmittingProjectIDPreservesIt: an update that does not mention
// project_id must leave the link alone.
//
// This branch's store never touches project_id, so a pass here is not evidence
// about the MCP layer; it only becomes a check once a store that writes the
// column is under it.
func TestUpdateTask_OmittingProjectIDPreservesIt(t *testing.T) {
	t.Parallel()
	s, d := newTestWorkSessionServerWithDB(t)
	p := probeProject(t, s)
	task := probeTask(t, s, &p, "")

	r := callUpdateTask(t, s, map[string]any{"task_id": task.String(), "title": "renamed"})
	if r.IsError {
		t.Fatalf("title-only update: IsError = true (%s), want success", resultText(r))
	}
	wantStoredProject(t, d, task, &p, "after an update that did not mention project_id")
}

// TestUpdateTask_EmptyProjectIDClears: "" is a clear, and a clear on its own is
// a valid call. Three ways to get this wrong, each a different failure here:
// "" read as omitted leaves P; "" sent to uuid.Parse is refused as invalid;
// updateTaskParamsIsEmpty ignoring the field refuses it as "at least one field
// is required".
func TestUpdateTask_EmptyProjectIDClears(t *testing.T) {
	t.Parallel()
	s, d := newTestWorkSessionServerWithDB(t)
	p := probeProject(t, s)
	task := probeTask(t, s, &p, "")

	r := callUpdateTask(t, s, map[string]any{"task_id": task.String(), "project_id": ""})
	if r.IsError {
		t.Fatalf("update_task{project_id:\"\" only}: IsError = true (%s), want success", resultText(r))
	}
	if got := ackProjectID(t, r); got != nil {
		t.Errorf("ack project_id = %s, want null", got)
	}
	wantStoredProject(t, d, task, nil, "after clear")
	if listedUnder(t, s, p, task) {
		t.Errorf("list_tasks(project %s) lists the task = true after clear, want false", p)
	}
}

// TestUpdateTask_ClearOnNullIsNoop: clearing a task that has no project is
// idempotent. The way to fail it is to look "" up as uuid.Nil and answer
// "project not found".
func TestUpdateTask_ClearOnNullIsNoop(t *testing.T) {
	t.Parallel()
	s, d := newTestWorkSessionServerWithDB(t)
	task := probeTask(t, s, nil, "")

	r := callUpdateTask(t, s, map[string]any{"task_id": task.String(), "project_id": ""})
	if r.IsError {
		t.Fatalf("clear on a task with no project: IsError = true (%s), want success", resultText(r))
	}
	if got := ackProjectID(t, r); got != nil {
		t.Errorf("ack project_id = %s, want null", got)
	}
	wantStoredProject(t, d, task, nil, "after clearing an already empty project_id")
}

// TestUpdateTask_UnknownProjectIsRejectedAndNothingChanges: no foreign key backs
// tasks.project_id (red line #9), so the existence check is the only thing
// between a typo and a task linked to a project nothing will ever list. The
// refusal is one static string, never the caller's UUID, and the call writes
// nothing — the title sent alongside must not land either.
func TestUpdateTask_UnknownProjectIsRejectedAndNothingChanges(t *testing.T) {
	t.Parallel()
	s, d := newTestWorkSessionServerWithDB(t)
	p := probeProject(t, s)
	task := probeTask(t, s, &p, "")
	unknown := uuid.New()

	r := callUpdateTask(t, s, map[string]any{
		"task_id": task.String(), "project_id": unknown.String(), "title": rejectedTitle,
	})
	if !r.IsError {
		t.Errorf("update_task(unknown project): IsError = false, want true")
	} else {
		if text := resultText(r); text != projectNotFoundText {
			t.Errorf("update_task(unknown project) text = %q, want exactly %q", text, projectNotFoundText)
		}
		if strings.Contains(resultText(r), unknown.String()) {
			t.Errorf("update_task(unknown project) echoed the caller's UUID %s, want a static string", unknown)
		}
	}
	wantStoredProject(t, d, task, &p, "after a rejected update")
	got, err := s.gtd.GetTaskByID(context.Background(), task)
	if err != nil {
		t.Fatalf("GetTaskByID fixture read: %v", err)
	}
	if got.Title == rejectedTitle {
		t.Errorf("title = %q after a rejected update, want it unchanged", got.Title)
	}
}

// TestUpdateTask_MalformedProjectID: a value that is not a UUID is refused
// before the store is reached, with the same text goal_id uses. Whitespace-only
// is malformed, not a clear: treating it as one would unlink a task over a
// stray space. Every case also sends a title, which must not land.
func TestUpdateTask_MalformedProjectID(t *testing.T) {
	t.Parallel()
	const wantText = "invalid project_id UUID"
	for _, bad := range []string{"not-a-uuid", "  ", "8a7fd166"} {
		t.Run(fmt.Sprintf("%q", bad), func(t *testing.T) {
			t.Parallel()
			s, d := newTestWorkSessionServerWithDB(t)
			p := probeProject(t, s)
			task := probeTask(t, s, &p, "")

			r := callUpdateTask(t, s, map[string]any{
				"task_id": task.String(), "project_id": bad, "title": rejectedTitle,
			})
			if !r.IsError {
				t.Errorf("update_task(project_id=%q): IsError = false, want true", bad)
			} else if text := resultText(r); text != wantText {
				t.Errorf("update_task(project_id=%q) text = %q, want exactly %q", bad, text, wantText)
			}
			wantStoredProject(t, d, task, &p, "after a malformed project_id")
			got, err := s.gtd.GetTaskByID(context.Background(), task)
			if err != nil {
				t.Fatalf("GetTaskByID fixture read: %v", err)
			}
			if got.Title == rejectedTitle {
				t.Errorf("title = %q after a malformed project_id, want it unchanged", got.Title)
			}
		})
	}
}

// TestUpdateTask_ProjectMovePreservesArea: moving a task between projects must
// not reclassify it. tasks.area is NOT NULL DEFAULT 'unsorted', so an UPDATE
// that rewrote it with a zero value would park every moved task in 'unsorted'
// without any error.
//
// Like the omission test, this cannot fail against a store that ignores
// project_id; it guards the store that writes it.
func TestUpdateTask_ProjectMovePreservesArea(t *testing.T) {
	t.Parallel()
	s, d := newTestWorkSessionServerWithDB(t)
	p := probeProject(t, s)
	task := probeTask(t, s, nil, "ai-arch")

	r := callUpdateTask(t, s, map[string]any{"task_id": task.String(), "project_id": p.String()})
	if r.IsError {
		t.Fatalf("update_task{project_id only}: IsError = true (%s), want success", resultText(r))
	}
	if got := storedArea(t, d, task); got != "ai-arch" {
		t.Errorf("area = %q after a project move, want ai-arch", got)
	}
}

// expectedUpdateTaskMaxBytes is spec 290c4bf0 Target 6's measured size of the
// update_task tool after project_id, and the figure A11 holds the schema to.
// update_task is a core tool, so every byte is paid by every session.
const expectedUpdateTaskMaxBytes = 1880

// TestUpdateTask_ProjectIDSchema: the argument is advertised, string-typed and
// optional, the tool stays inside its byte budget, and the per-field semantics
// stay in the appendix rather than leaking into the always-paid description. [F1011-14]
func TestUpdateTask_ProjectIDSchema(t *testing.T) {
	t.Parallel()
	_, ms := newTestMCPServer(t)
	entry, ok := ms.ListTools()["update_task"]
	if !ok {
		t.Fatal("update_task is not registered")
	}
	schema := entry.Tool.InputSchema

	prop, ok := schema.Properties["project_id"].(map[string]any)
	if !ok {
		t.Fatalf("update_task has no project_id property (properties: %v), want one", schema.Properties)
	}
	if prop["type"] != schemaTypeString {
		t.Errorf("project_id type = %v, want string", prop["type"])
	}
	if prop["description"] != "Project UUID to move the task into, empty clears" {
		t.Errorf("project_id description = %v, want %q", prop["description"], "Project UUID to move the task into, empty clears")
	}
	if !slices.Equal(schema.Required, []string{"task_id"}) {
		t.Errorf("update_task required = %v, want [task_id] only (omitting project_id must stay legal)", schema.Required)
	}
	if strings.Contains(entry.Tool.Description, "project_id") {
		t.Errorf("update_task description mentions project_id, want the detail in the appendix only: %q", entry.Tool.Description)
	}

	raw, err := json.Marshal(entry.Tool)
	if err != nil {
		t.Fatalf("marshal update_task fixture: %v", err)
	}
	t.Logf("update_task serialises to %d bytes (budget %d)", len(raw), expectedUpdateTaskMaxBytes)
	if len(raw) > expectedUpdateTaskMaxBytes {
		t.Errorf("update_task serialises to %d bytes, want <= %d", len(raw), expectedUpdateTaskMaxBytes)
	}
}

// updateTaskStubStore stands in for the store behind handleUpdateTask: it
// records what the handler handed it and answers with a fixed error. It embeds
// noopGTDStore, so it needs no real database and no workspace.
type updateTaskStubStore struct {
	noopGTDStore
	err   error
	calls int
	got   gtd.UpdateTaskParams
}

func (st *updateTaskStubStore) UpdateTask(_ context.Context, _ uuid.UUID, p gtd.UpdateTaskParams) (*db.Task, error) {
	st.calls++
	st.got = p
	return nil, st.err
}

// TestUpdateTask_ProjectNotFoundMapping pins the handler's reading of the
// store's errors, with no SQLite store involved: ErrProjectNotFound becomes the
// static "project not found" (and not "task not found", which is what an
// ErrNotFound-shaped answer would say), ErrNotFound stays "task not found", and
// anything else stays redacted. [F1011-14]
//
// The handler is called directly rather than through seam(): the seam looks the
// tool's spec up in a registry that only a built Server fills, so a bare
// &Server{gtd: stub} would panic there whenever this test runs on its own.
func TestUpdateTask_ProjectNotFoundMapping(t *testing.T) {
	t.Parallel()
	pid := uuid.New()
	pidText := pid.String()

	cases := []struct {
		name     string
		storeErr error
		wantText string
		absent   []string // substrings the client must never see
	}{
		{
			"unknown or foreign project is project not found, never task not found",
			fmt.Errorf("x: %w", gtd.ErrProjectNotFound), projectNotFoundText,
			[]string{pidText, taskNotFoundText},
		},
		{
			"unknown task keeps its own text",
			fmt.Errorf("x: %w", gtd.ErrNotFound), taskNotFoundText,
			[]string{pidText, projectNotFoundText},
		},
		{
			"other store error stays redacted",
			errors.New("SQLSTATE 23505 db.internal.example"), "updating task failed",
			[]string{pidText, "SQLSTATE", "db.internal.example", projectNotFoundText, taskNotFoundText},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, stub := updateTaskViaStub(t, tc.storeErr, pidText)
			if !res.IsError {
				t.Errorf("IsError = false, want true")
			}
			text := resultText(res)
			if text != tc.wantText {
				t.Errorf("text = %q, want exactly %q", text, tc.wantText)
			}
			for _, bad := range tc.absent {
				if strings.Contains(text, bad) {
					t.Errorf("text %q contains %q, want a static string without it", text, bad)
				}
			}
			if stub.calls != 1 {
				t.Fatalf("store UpdateTask calls = %d, want 1", stub.calls)
			}
			if stub.got.Project == nil || stub.got.Project.ID == nil || *stub.got.Project.ID != pid {
				t.Errorf("store received Project = %s, want set %s", projectRefState(stub.got.Project), pid)
			}
		})
	}
}

// updateTaskViaStub runs handleUpdateTask against a stub store that fails with
// storeErr, and returns the tool result with the stub (for what it received).
func updateTaskViaStub(t *testing.T, storeErr error, projectID string) (*mcpmsg.CallToolResult, *updateTaskStubStore) {
	t.Helper()
	stub := &updateTaskStubStore{err: storeErr}
	s := &Server{gtd: stub}
	res, err := s.handleUpdateTask(context.Background(), UpdateTaskArgs{TaskID: uuid.New(), ProjectID: &projectID})
	if err != nil {
		t.Fatalf("handleUpdateTask returned a transport error: %v, want a tool error result", err)
	}
	return res, stub
}
