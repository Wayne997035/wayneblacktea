package handler_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// TypeTask HTTP accept goes through proposal.AcceptOrchestration on both
// backends. Like the TypeConcept suite (proposal_handler_concept_helpers_test.go)
// every case is written ONCE against taskBackend and run by a thin per-backend
// Test function (SQLite in proposal_handler_task_sqlite_test.go, Postgres in
// proposal_handler_task_pg_test.go). Each backend drives the real stores and the
// real accept adapter — no mocks (PG = testcontainers, SQLite = real :memory:
// DB). Every test sets WBT_STRICT_VAGUENESS explicitly (the variable is
// process-global) and none uses t.Parallel.

const (
	// taskWarnVague is the description whose two warnings taskVagueWarnings lists.
	taskWarnVague = "TBD"
	// taskBogusKind raises exactly one warning (taskBogusKindWarning) next to a clean description.
	taskBogusKind        = "bogus"
	taskStatusPending    = "pending"
	taskKindGeneral      = "general"
	taskAreaUnsorted     = "unsorted"
	taskAlreadyDone      = "proposal already resolved"
	taskVaguenessFailed  = "vagueness check failed"
	taskMissingTitleText = "task payload missing title"
	taskTitleCapText     = "task title exceeds 500 characters"
	taskDecodeFailedText = "decoding task payload"
	taskStrictOn         = "true"
	taskStrictOff        = ""
	// taskChecklistEmpty is base64("[]"): db.Task.Checklist is []byte, so JSON carries it base64-encoded.
	taskChecklistEmpty = "W10="
)

// taskVagueWarnings are the warnings validator.CheckTaskInput raises for taskWarnVague (kind general).
var taskVagueWarnings = []string{
	"description: contains vague marker TBD",
	"description: too short (< 30 chars) and no file:line reference",
}

// taskBogusKindWarning is what validator.ResolveTaskKind raises for taskBogusKind.
var taskBogusKindWarning = []string{`kind "bogus" is not a valid task kind; falling back to general`}

// taskJSONKeys are the 21 keys of a serialized db.Task. Both backends must
// return all of them: a map that only carries the id and title (the shape the
// SQLite goal/project adapters return) would silently thin the response.
var taskJSONKeys = []string{
	"id", "project_id", "title", "description", "status", "priority", "assignee", "due_date",
	"artifact", "created_at", "updated_at", "workspace_id", "importance", "context", "checklist",
	"kind", "branch_name", "pr_url", "commit_shas", "vision_item_id", "area",
}

// taskBackend is one real DB stack behind a real ProposalHandler.
type taskBackend struct {
	name string
	// rawJSONPayload reports whether the proposals table accepts non-JSON
	// payload bytes (SQLite TEXT does; Postgres jsonb rejects them at INSERT).
	rawJSONPayload bool
	// newAdapter builds one fresh real proposal.AcceptAdapter for a proposal id.
	newAdapter func(id uuid.UUID) proposal.AcceptAdapter
	// newHandler builds the handler over newAdapter. wrap, when non-nil, wraps
	// every per-accept adapter (used to inject a ResolveAccepted failure after
	// the real Materialize already ran inside the open transaction).
	newHandler func(wrap func(proposal.AcceptAdapter) proposal.AcceptAdapter) *handler.ProposalHandler
	// create inserts a pending proposal and returns its id.
	create func(t *testing.T, typ proposal.Type, payload []byte) uuid.UUID
	// status returns the proposal row's current status.
	status func(t *testing.T, id uuid.UUID) string
	// taskCount counts tasks rows by exact title; taskID returns the id of the single row with that title.
	taskCount func(t *testing.T, title string) int
	taskID    func(t *testing.T, title string) string
	// trackTitle registers cleanup of tasks rows by title. A no-op on SQLite
	// (fresh :memory: DB per test); the PG container is shared.
	trackTitle func(t *testing.T, title string)
}

func (be taskBackend) uniqueTitle(t *testing.T, prefix string) string {
	t.Helper()
	title := prefix + "-" + be.name + "-" + uuid.NewString()
	be.trackTitle(t, title)
	return title
}

// titleOfBytes returns a unique title padded with ASCII to exactly n bytes.
func (be taskBackend) titleOfBytes(t *testing.T, prefix string, n int) string {
	t.Helper()
	title := prefix + "-" + be.name + "-" + uuid.NewString()
	if len(title) > n {
		t.Fatalf("titleOfBytes: prefix title %q already longer than %d bytes", title, n)
	}
	title += strings.Repeat("p", n-len(title))
	be.trackTitle(t, title)
	return title
}

// taskPayloadJSON builds a TypeTask payload; an empty kind omits suggested_kind.
func taskPayloadJSON(t *testing.T, title, description, kind string) []byte {
	t.Helper()
	m := map[string]any{"title": title, "description": description}
	if kind != "" {
		m["suggested_kind"] = kind
	}
	return mustMarshal(t, m)
}

// taskWant is what the singular accept response must carry for a task.
type taskWant struct {
	ID          string
	Title       string
	Description string
	Kind        string
}

// checkTaskAcceptResponse is the ONE helper that asserts the singular accept
// response on BOTH backends (the cbbbf686 parity comparison): `task` is present
// and complete (all 21 db.Task keys, id equals the tasks row id, title /
// description / kind equal the payload, status pending, priority 3, area
// unsorted, empty checklist, no project, no assignee) and the proposal is
// accepted. It returns the first problem instead of failing the test so a
// self-test can prove it rejects the id/title-only shape.
func checkTaskAcceptResponse(body []byte, want taskWant) error {
	var resp struct {
		Proposal struct {
			Status string `json:"status"`
		} `json:"proposal"`
		Task map[string]any `json:"task"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("response is not valid JSON: %w (body: %s)", err, body)
	}
	if resp.Proposal.Status != proposalAccepted {
		return fmt.Errorf("proposal.status = %q, want %q", resp.Proposal.Status, proposalAccepted)
	}
	task := resp.Task
	if task == nil {
		return fmt.Errorf("response has no non-null task object: %s", body)
	}
	for _, k := range taskJSONKeys {
		if _, ok := task[k]; !ok {
			return fmt.Errorf("task is missing key %q (an id/title-only task object?): %v", k, task)
		}
	}
	if len(task) != len(taskJSONKeys) {
		return fmt.Errorf("task has %d keys, want exactly %d: %v", len(task), len(taskJSONKeys), task)
	}
	id, _ := task["id"].(string)
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("task.id %q is not a UUID: %w", id, err)
	}
	if want.ID != "" && id != want.ID {
		return fmt.Errorf("task.id = %q, want the tasks row id %q", id, want.ID)
	}
	return checkTaskStableFields(task, want)
}

// checkTaskStableFields compares the task fields that must be equal across
// backends (ignored: id, created_at, updated_at, whose format differs per dialect).
func checkTaskStableFields(task map[string]any, want taskWant) error {
	if got := task["title"]; got != want.Title {
		return fmt.Errorf("task.title = %v, want %q", got, want.Title)
	}
	if got := task["description"]; got != want.Description {
		return fmt.Errorf("task.description = %v, want %q", got, want.Description)
	}
	if got := task["kind"]; got != want.Kind {
		return fmt.Errorf("task.kind = %v, want %q", got, want.Kind)
	}
	if got := task["status"]; got != taskStatusPending {
		return fmt.Errorf("task.status = %v, want %q", got, taskStatusPending)
	}
	if got := task["priority"]; got != float64(3) {
		return fmt.Errorf("task.priority = %v, want 3", got)
	}
	if got := task["area"]; got != taskAreaUnsorted {
		return fmt.Errorf("task.area = %v, want %q", got, taskAreaUnsorted)
	}
	if got := task["checklist"]; got != taskChecklistEmpty {
		return fmt.Errorf("task.checklist = %v, want %q", got, taskChecklistEmpty)
	}
	if task["project_id"] != nil || task["assignee"] != nil {
		return fmt.Errorf("task.project_id/assignee = %v/%v, want both null", task["project_id"], task["assignee"])
	}
	// commit_shas is null on SQLite and [] on PG (a pre-existing divergence, out of scope here).
	if shas, ok := task["commit_shas"].([]any); task["commit_shas"] != nil && (!ok || len(shas) != 0) {
		return fmt.Errorf("task.commit_shas = %v, want null or []", task["commit_shas"])
	}
	return nil
}

func requireTaskResponse(t *testing.T, rec *httptest.ResponseRecorder, want taskWant) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := checkTaskAcceptResponse(rec.Body.Bytes(), want); err != nil {
		t.Fatal(err)
	}
}

// requireNoWarningsKey fails when the response body carries a top-level `warnings` key.
func requireNoWarningsKey(t *testing.T, body []byte) {
	t.Helper()
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v (body: %s)", err, body)
	}
	if _, has := resp["warnings"]; has {
		t.Errorf("response has a warnings key, want none: %s", body)
	}
}

// requireResponseWarnings fails unless the response's top-level `warnings` equals want exactly.
func requireResponseWarnings(t *testing.T, body []byte, want []string) {
	t.Helper()
	var resp struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v (body: %s)", err, body)
	}
	if !reflect.DeepEqual(resp.Warnings, want) {
		t.Errorf("warnings = %q, want %q", resp.Warnings, want)
	}
}

// TestConfirmProposal_TypeTask_ResponseHelper_RejectsIDTitleOnly proves the
// shared helper is not vacuous: an id/title-only task object (the shape the
// SQLite goal/project adapters return, and what a thin SQLite task adapter
// would surface) MUST be refused, as must a response with no task at all, while
// a complete one is accepted.
func TestConfirmProposal_TypeTask_ResponseHelper_RejectsIDTitleOnly(t *testing.T) {
	id := uuid.NewString()
	want := taskWant{ID: id, Title: "t", Description: "d", Kind: taskKindGeneral}
	full := map[string]any{
		"id": id, "project_id": nil, "title": "t", "description": "d", "status": taskStatusPending, "priority": 3,
		"assignee": nil, "due_date": nil, "artifact": nil, "created_at": "2026-10-14T00:00:00Z",
		"updated_at": "2026-10-14T00:00:00Z", "workspace_id": nil, "importance": nil, "context": nil,
		"checklist": taskChecklistEmpty, "kind": taskKindGeneral, "branch_name": nil, "pr_url": nil,
		"commit_shas": nil, "vision_item_id": nil, "area": taskAreaUnsorted,
	}
	body := func(task any) string {
		return string(mustMarshal(t, map[string]any{"proposal": map[string]any{"status": "accepted"}, "task": task}))
	}
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "complete task passes (positive control)", body: body(full)},
		{name: "id/title-only task is rejected", body: body(map[string]any{"id": id, "title": "t"}), wantErr: true},
		{name: "missing task is rejected", body: `{"proposal":{"status":"accepted"}}`, wantErr: true},
		{name: "null task is rejected", body: body(nil), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkTaskAcceptResponse([]byte(tc.body), want)
			if tc.wantErr && err == nil {
				t.Error("helper accepted a response it must reject")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("helper rejected a complete response: %v", err)
			}
		})
	}
}

func requireTaskProposalStatus(t *testing.T, be taskBackend, id uuid.UUID, want string) {
	t.Helper()
	if got := be.status(t, id); got != want {
		t.Errorf("proposal %s status = %q, want %q", id, got, want)
	}
}

// requireTaskErrorBody fails unless rec is a 400 whose `error` contains wantMsg.
func requireTaskBadRequest(t *testing.T, rec *httptest.ResponseRecorder, wantMsg string) {
	t.Helper()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), wantMsg) {
		t.Errorf("body = %s, want substring %q", rec.Body.String(), wantMsg)
	}
}

// errTaskInjectedBoom is the arbitrary store error whose text must never reach the client.
var errTaskInjectedBoom = errors.New(errBoomText)
