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
	// taskCount counts tasks rows by exact title; taskCountByDescription the same
	// by exact description (for payloads that have no usable title); taskID
	// returns the id of the single row with that title.
	taskCount              func(t *testing.T, title string) int
	taskCountByDescription func(t *testing.T, description string) int
	taskID                 func(t *testing.T, title string) string
	// taskKind returns the kind column of the single tasks row with that title.
	taskKind func(t *testing.T, title string) string
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

// requireTaskBadRequest fails unless rec is a 400 whose body contains wantMsg.
func requireTaskBadRequest(t *testing.T, rec *httptest.ResponseRecorder, wantMsg string) {
	t.Helper()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), wantMsg) {
		t.Errorf("body = %s, want substring %q", rec.Body.String(), wantMsg)
	}
}

// ---------------------------------------------------------------------------
// Single route: POST /api/proposals/:id/confirm
// ---------------------------------------------------------------------------

// runTaskMaterialises: a clean accept writes exactly one tasks row, resolves
// the proposal and returns the complete task with no warnings key — identically
// on both backends.
func runTaskMaterialises(t *testing.T, be taskBackend) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
	h := be.newHandler(nil)
	title := be.uniqueTitle(t, "task")
	id := be.create(t, proposal.TypeTask, taskPayloadJSON(t, title, cleanKindTestDescription, taskKindGeneral))

	rec := postConfirm(h, id, acceptBody)

	requireCount(t, "tasks rows", be.taskCount(t, title), 1)
	requireTaskResponse(t, rec, taskWant{
		ID: be.taskID(t, title), Title: title, Description: cleanKindTestDescription, Kind: taskKindGeneral,
	})
	requireNoWarningsKey(t, rec.Body.Bytes())
	requireTaskProposalStatus(t, be, id, proposalAccepted)
}

// runTaskBadPayload: a payload the gate refuses is a 400 with the decoder's
// text, never opens the transaction, writes no row and leaves the proposal
// pending. The tasks-row count is by description because these payloads have no
// usable title; description is unique per call.
func runTaskBadPayload(t *testing.T, be taskBackend, payload func(description string) []byte, wantMsg string) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
	h := be.newHandler(nil)
	description := cleanKindTestDescription + " " + uuid.NewString()
	id := be.create(t, proposal.TypeTask, payload(description))

	rec := postConfirm(h, id, acceptBody)

	requireTaskBadRequest(t, rec, wantMsg)
	requireCount(t, "tasks rows", be.taskCountByDescription(t, description), 0)
	requireTaskProposalStatus(t, be, id, proposalPending)
}

// runTaskWarnMode: in warn mode a payload that raises warnings still creates
// the task and surfaces the warnings next to it. For the vague description the
// real adapters return the {"task","warnings"} map, so a handler that only
// recognised *db.Task would drop `task` here.
func runTaskWarnMode(t *testing.T, be taskBackend, description, kind string, wantWarnings []string) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
	h := be.newHandler(nil)
	title := be.uniqueTitle(t, "warn")
	id := be.create(t, proposal.TypeTask, taskPayloadJSON(t, title, description, kind))

	rec := postConfirm(h, id, acceptBody)

	requireCount(t, "tasks rows", be.taskCount(t, title), 1)
	requireTaskResponse(t, rec, taskWant{ID: be.taskID(t, title), Title: title, Description: description, Kind: taskKindGeneral})
	requireResponseWarnings(t, rec.Body.Bytes(), wantWarnings)
	requireTaskProposalStatus(t, be, id, proposalAccepted)
}

// runTaskStrictMode: under WBT_STRICT_VAGUENESS=true the same payloads are a
// 400 whose body is exactly {"error":"vagueness check failed","warnings":[...]},
// decided BEFORE the transaction: no row, proposal still pending.
func runTaskStrictMode(t *testing.T, be taskBackend, description, kind string, wantWarnings []string) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOn)
	h := be.newHandler(nil)
	title := be.uniqueTitle(t, "strict")
	id := be.create(t, proposal.TypeTask, taskPayloadJSON(t, title, description, kind))

	rec := postConfirm(h, id, acceptBody)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not valid JSON: %v (body: %s)", err, rec.Body.String())
	}
	warnings := make([]any, len(wantWarnings))
	for i, w := range wantWarnings {
		warnings[i] = w
	}
	if want := map[string]any{"error": taskVaguenessFailed, "warnings": warnings}; !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v, want %v", got, want)
	}
	requireCount(t, "tasks rows", be.taskCount(t, title), 0)
	requireTaskProposalStatus(t, be, id, proposalPending)
}

// runTaskStrictCleanStillCreates is the negative control of runTaskStrictMode:
// the strict 400 is not blanket — a clean payload is still accepted under strict.
func runTaskStrictCleanStillCreates(t *testing.T, be taskBackend) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOn)
	h := be.newHandler(nil)
	title := be.uniqueTitle(t, "strict-clean")
	id := be.create(t, proposal.TypeTask, taskPayloadJSON(t, title, cleanKindTestDescription, taskKindGeneral))

	rec := postConfirm(h, id, acceptBody)

	requireCount(t, "tasks rows", be.taskCount(t, title), 1)
	requireTaskResponse(t, rec, taskWant{
		ID: be.taskID(t, title), Title: title, Description: cleanKindTestDescription, Kind: taskKindGeneral,
	})
	requireTaskProposalStatus(t, be, id, proposalAccepted)
}

// runTaskResolveFails: when the resolve step fails after the task insert, the
// whole transaction rolls back. The pre-seam design left an orphan tasks row here.
func runTaskResolveFails(t *testing.T, be taskBackend) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
	t.Run("a concurrent resolver (ErrNotFound) gives 409 and no orphan task", func(t *testing.T) {
		h := be.newHandler(injectResolveErr(fmt.Errorf("injected: %w", proposal.ErrNotFound)))
		title := be.uniqueTitle(t, "resolve-race")
		id := be.create(t, proposal.TypeTask, taskPayloadJSON(t, title, cleanKindTestDescription, taskKindGeneral))

		rec := postConfirm(h, id, acceptBody)

		if rec.Code != http.StatusConflict {
			t.Fatalf("want 409, got %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), taskAlreadyDone) {
			t.Errorf("body = %s, want substring %q", rec.Body.String(), taskAlreadyDone)
		}
		requireCount(t, "tasks rows after injected resolve failure", be.taskCount(t, title), 0)
		requireTaskProposalStatus(t, be, id, proposalPending)
	})
	t.Run("an unexpected store error gives 500 without leaking its text", func(t *testing.T) {
		h := be.newHandler(injectResolveErr(errors.New(errBoomText)))
		title := be.uniqueTitle(t, "resolve-boom")
		id := be.create(t, proposal.TypeTask, taskPayloadJSON(t, title, cleanKindTestDescription, taskKindGeneral))

		rec := postConfirm(h, id, acceptBody)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := strings.TrimSpace(rec.Body.String()); got != internalErrorBody {
			t.Errorf("body = %s, want exactly %s (no raw error text)", got, internalErrorBody)
		}
		requireCount(t, "tasks rows after injected resolve failure", be.taskCount(t, title), 0)
		requireTaskProposalStatus(t, be, id, proposalPending)
	})
}

// runTaskDoubleAccept: the second accept of the same proposal is a 409 and
// writes nothing, while an unrelated pending proposal still accepts.
func runTaskDoubleAccept(t *testing.T, be taskBackend) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
	h := be.newHandler(nil)
	title := be.uniqueTitle(t, "double")
	id := be.create(t, proposal.TypeTask, taskPayloadJSON(t, title, cleanKindTestDescription, taskKindGeneral))
	other := be.uniqueTitle(t, "double-other")
	otherID := be.create(t, proposal.TypeTask, taskPayloadJSON(t, other, cleanKindTestDescription, taskKindGeneral))

	first := postConfirm(h, id, acceptBody)
	if first.Code != http.StatusOK {
		t.Fatalf("first accept: want 200, got %d: %s", first.Code, first.Body.String())
	}
	rec := postConfirm(h, id, acceptBody)

	if rec.Code != http.StatusConflict {
		t.Fatalf("second accept: want 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), taskAlreadyDone) {
		t.Errorf("body = %s, want substring %q", rec.Body.String(), taskAlreadyDone)
	}
	requireCount(t, "tasks rows after second accept", be.taskCount(t, title), 1)

	if again := postConfirm(h, otherID, acceptBody); again.Code != http.StatusOK {
		t.Fatalf("unrelated proposal: want 200, got %d: %s", again.Code, again.Body.String())
	}
	requireCount(t, "tasks rows of the unrelated proposal", be.taskCount(t, other), 1)
}

// runTaskPayloadRejected: every payload the decoder refuses is a 400 with the
// decoder's text, no row, and the proposal stays pending. The title caps are in
// BYTES (501 ASCII bytes and 167 CJK characters = 501 bytes both exceed 500).
func runTaskPayloadRejected(t *testing.T, be taskBackend) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
	title501 := strings.Repeat("a", 501)
	titleCJK501 := strings.Repeat("決", 167)
	withTitle := func(title string) func(string) []byte {
		return func(description string) []byte { return taskPayloadJSON(t, title, description, "") }
	}
	cases := []struct {
		name    string
		payload func(description string) []byte
		title   string // non-empty: count rows by this title; empty: by the unique description
		wantMsg string
	}{
		{
			name:    "missing title",
			payload: func(description string) []byte { return mustMarshal(t, map[string]any{"description": description}) },
			wantMsg: taskMissingTitleText,
		},
		{name: "title 501 ASCII bytes", payload: withTitle(title501), title: title501, wantMsg: taskTitleCapText},
		{name: "title 167 CJK chars (501 bytes)", payload: withTitle(titleCJK501), title: titleCJK501, wantMsg: taskTitleCapText},
		{name: "payload is a JSON array", payload: func(string) []byte { return []byte(`[]`) }, wantMsg: taskDecodeFailedText},
	}
	// Raw non-JSON bytes (`{bad json`) cannot be stored for a TypeTask on EITHER
	// backend: Postgres jsonb refuses them at INSERT and SQLite's partial index
	// on type='task' (migration 000085, json_extract) raises "malformed JSON".
	// That case therefore lives in the fake-store test
	// TestConfirmProposal_TypeTask_MalformedPayload.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := be.newHandler(nil)
			description := cleanKindTestDescription + " " + uuid.NewString()
			id := be.create(t, proposal.TypeTask, tc.payload(description))

			rec := postConfirm(h, id, acceptBody)

			requireTaskBadRequest(t, rec, tc.wantMsg)
			if tc.title != "" {
				requireCount(t, "tasks rows by title", be.taskCount(t, tc.title), 0)
			} else {
				requireCount(t, "tasks rows by description", be.taskCountByDescription(t, description), 0)
			}
			requireTaskProposalStatus(t, be, id, proposalPending)
		})
	}
}

// runTaskCapBoundaries is the negative control of runTaskPayloadRejected: the
// 400 is cap-driven, not blanket. A title of exactly 500 bytes, and one of 498
// bytes made of multi-byte characters, are accepted.
func runTaskCapBoundaries(t *testing.T, be taskBackend) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
	cjk498 := strings.Repeat("決", 166)
	be.trackTitle(t, cjk498)
	cases := []struct{ name, title string }{
		{"title exactly 500 bytes", be.titleOfBytes(t, "cap", 500)},
		{"title 166 CJK chars (498 bytes)", cjk498},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := be.newHandler(nil)
			id := be.create(t, proposal.TypeTask, taskPayloadJSON(t, tc.title, cleanKindTestDescription, ""))

			rec := postConfirm(h, id, acceptBody)

			requireCount(t, "tasks rows", be.taskCount(t, tc.title), 1)
			requireTaskResponse(t, rec, taskWant{
				ID: be.taskID(t, tc.title), Title: tc.title, Description: cleanKindTestDescription, Kind: taskKindGeneral,
			})
			requireTaskProposalStatus(t, be, id, proposalAccepted)
		})
	}
}

// ---------------------------------------------------------------------------
// Batch route: POST /api/proposals/confirm-batch
// ---------------------------------------------------------------------------

const (
	batchActionReject    = "reject"
	batchSkippedText     = "not found or already resolved"
	proposalRejectedText = string(proposal.StatusRejected)
)

// taskBatchEntry mirrors the handler's batch result entry including the
// additive warnings field (the shared batchResultEntry does not carry it).
type taskBatchEntry struct {
	ID       string   `json:"id"`
	OK       bool     `json:"ok"`
	Skipped  bool     `json:"skipped"`
	Error    string   `json:"error"`
	Warnings []string `json:"warnings"`
}

func decodeTaskBatchEntry(t *testing.T, body []byte, wantID string) taskBatchEntry {
	t.Helper()
	var resp struct {
		Results []taskBatchEntry `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response not valid JSON: %v (body: %s)", err, body)
	}
	for _, r := range resp.Results {
		if r.ID == wantID {
			return r
		}
	}
	t.Fatalf("no result entry for id %s in response: %s", wantID, body)
	return taskBatchEntry{}
}

// requireTaskBatchOK fails unless the entry for id is ok:true with no error, and returns it.
func requireTaskBatchOK(t *testing.T, rec *httptest.ResponseRecorder, id uuid.UUID) taskBatchEntry {
	t.Helper()
	entry := decodeTaskBatchEntry(t, rec.Body.Bytes(), id.String())
	if !entry.OK || entry.Error != "" || entry.Skipped {
		t.Errorf("entry %s = %+v, want ok:true with no error", id, entry)
	}
	return entry
}

// requireTaskBatchRefused fails unless the entry for id is ok:false (not skipped) with an error containing every want.
func requireTaskBatchRefused(t *testing.T, rec *httptest.ResponseRecorder, id uuid.UUID, wants ...string) {
	t.Helper()
	entry := decodeTaskBatchEntry(t, rec.Body.Bytes(), id.String())
	if entry.OK || entry.Skipped {
		t.Errorf("entry %s = %+v, want ok:false skipped:false", id, entry)
	}
	for _, want := range wants {
		if !strings.Contains(entry.Error, want) {
			t.Errorf("entry %s error = %q, want substring %q", id, entry.Error, want)
		}
	}
}

// runBatchTaskMaterialises: every valid TypeTask id in the batch is ok:true,
// writes its own tasks row and is accepted.
func runBatchTaskMaterialises(t *testing.T, be taskBackend) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
	h := be.newHandler(nil)
	titles := []string{be.uniqueTitle(t, "batch-1"), be.uniqueTitle(t, "batch-2")}
	ids := []uuid.UUID{
		be.create(t, proposal.TypeTask, taskPayloadJSON(t, titles[0], cleanKindTestDescription, taskKindGeneral)),
		be.create(t, proposal.TypeTask, taskPayloadJSON(t, titles[1], cleanKindTestDescription, taskKindGeneral)),
	}

	rec := postConfirmBatch(t, h, batchActionAccept, ids...)

	for i, id := range ids {
		entry := requireTaskBatchOK(t, rec, id)
		if len(entry.Warnings) != 0 {
			t.Errorf("entry %s warnings = %q, want none for a clean payload", id, entry.Warnings)
		}
		requireCount(t, "tasks rows for "+titles[i], be.taskCount(t, titles[i]), 1)
		requireTaskProposalStatus(t, be, id, proposalAccepted)
	}
}

// runBatchTaskMixedValidAndInvalid: a type-error payload and an over-cap title
// are per-row errors with the decoder's text, do not block the valid id, and
// stay pending.
func runBatchTaskMixedValidAndInvalid(t *testing.T, be taskBackend) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
	h := be.newHandler(nil)
	validTitle := be.uniqueTitle(t, "mixed-valid")
	bigTitle := strings.Repeat("a", 501)
	validID := be.create(t, proposal.TypeTask, taskPayloadJSON(t, validTitle, cleanKindTestDescription, taskKindGeneral))
	typeErrID := be.create(t, proposal.TypeTask, []byte(`{"title":123}`))
	bigID := be.create(t, proposal.TypeTask, taskPayloadJSON(t, bigTitle, cleanKindTestDescription, ""))

	rec := postConfirmBatch(t, h, batchActionAccept, validID, typeErrID, bigID)

	requireTaskBatchOK(t, rec, validID)
	requireTaskBatchRefused(t, rec, typeErrID, taskDecodeFailedText)
	requireTaskBatchRefused(t, rec, bigID, taskTitleCapText)
	requireTaskProposalStatus(t, be, typeErrID, proposalPending)
	requireTaskProposalStatus(t, be, bigID, proposalPending)
	requireCount(t, "tasks rows of the valid id", be.taskCount(t, validTitle), 1)
	requireCount(t, "tasks rows of the over-cap id", be.taskCount(t, bigTitle), 0)
}

// runBatchTaskInvalidKindWarnMode: in warn mode a bogus suggested_kind still
// materialises the task (kind general) and the entry carries the one warning.
func runBatchTaskInvalidKindWarnMode(t *testing.T, be taskBackend) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
	h := be.newHandler(nil)
	title := be.uniqueTitle(t, "batch-bogus")
	id := be.create(t, proposal.TypeTask, taskPayloadJSON(t, title, cleanKindTestDescription, taskBogusKind))

	rec := postConfirmBatch(t, h, batchActionAccept, id)

	entry := requireTaskBatchOK(t, rec, id)
	if len(entry.Warnings) != 1 || !strings.Contains(entry.Warnings[0], taskBogusKind) {
		t.Errorf("entry warnings = %q, want exactly one mentioning %q", entry.Warnings, taskBogusKind)
	}
	requireCount(t, "tasks rows", be.taskCount(t, title), 1)
	if got := be.taskKind(t, title); got != taskKindGeneral {
		t.Errorf("task kind = %q, want %q (an invalid kind falls back)", got, taskKindGeneral)
	}
	requireTaskProposalStatus(t, be, id, proposalAccepted)
}

// runBatchTaskStrictPerRow: under strict mode the same bogus-kind payload is a
// per-row "vagueness check failed" error with no row and the proposal still
// pending, while a valid sibling in the same request is accepted.
func runBatchTaskStrictPerRow(t *testing.T, be taskBackend) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOn)
	h := be.newHandler(nil)
	badTitle := be.uniqueTitle(t, "strict-bad")
	okTitle := be.uniqueTitle(t, "strict-ok")
	badID := be.create(t, proposal.TypeTask, taskPayloadJSON(t, badTitle, cleanKindTestDescription, taskBogusKind))
	okID := be.create(t, proposal.TypeTask, taskPayloadJSON(t, okTitle, cleanKindTestDescription, taskKindGeneral))

	rec := postConfirmBatch(t, h, batchActionAccept, badID, okID)

	requireTaskBatchRefused(t, rec, badID, taskVaguenessFailed, taskBogusKind)
	requireTaskBatchOK(t, rec, okID)
	requireCount(t, "tasks rows of the strict-rejected id", be.taskCount(t, badTitle), 0)
	requireCount(t, "tasks rows of the valid sibling", be.taskCount(t, okTitle), 1)
	requireTaskProposalStatus(t, be, badID, proposalPending)
	requireTaskProposalStatus(t, be, okID, proposalAccepted)
}

// runBatchTaskRejectNoGate: the gate is accept-only. Rejecting a vague payload
// under strict mode is ok:true, rejects the proposal and writes no row.
func runBatchTaskRejectNoGate(t *testing.T, be taskBackend) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOn)
	h := be.newHandler(nil)
	title := be.uniqueTitle(t, "batch-reject")
	id := be.create(t, proposal.TypeTask, taskPayloadJSON(t, title, taskWarnVague, taskKindGeneral))

	rec := postConfirmBatch(t, h, batchActionReject, id)

	requireTaskBatchOK(t, rec, id)
	requireCount(t, "tasks rows", be.taskCount(t, title), 0)
	requireTaskProposalStatus(t, be, id, proposalRejectedText)
}

// runBatchTaskAlreadyAccepted: re-accepting an already accepted proposal is a
// per-row skip that leaves the rows untouched, while a fresh id in the same
// request still succeeds.
func runBatchTaskAlreadyAccepted(t *testing.T, be taskBackend) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
	h := be.newHandler(nil)
	doneTitle := be.uniqueTitle(t, "already")
	freshTitle := be.uniqueTitle(t, "fresh")
	doneID := be.create(t, proposal.TypeTask, taskPayloadJSON(t, doneTitle, cleanKindTestDescription, taskKindGeneral))
	freshID := be.create(t, proposal.TypeTask, taskPayloadJSON(t, freshTitle, cleanKindTestDescription, taskKindGeneral))
	if first := postConfirm(h, doneID, acceptBody); first.Code != http.StatusOK {
		t.Fatalf("first accept: want 200, got %d: %s", first.Code, first.Body.String())
	}

	rec := postConfirmBatch(t, h, batchActionAccept, doneID, freshID)

	entry := decodeTaskBatchEntry(t, rec.Body.Bytes(), doneID.String())
	if entry.OK || !entry.Skipped || entry.Error != batchSkippedText {
		t.Errorf("already-accepted entry = %+v, want ok:false skipped:true error:%q", entry, batchSkippedText)
	}
	requireTaskBatchOK(t, rec, freshID)
	requireCount(t, "tasks rows of the already accepted id", be.taskCount(t, doneTitle), 1)
	requireCount(t, "tasks rows of the fresh id", be.taskCount(t, freshTitle), 1)
}

// runBatchTaskResolveFails: an injected resolve failure maps to skipped:true,
// rolls the task insert back and leaves the proposal pending.
func runBatchTaskResolveFails(t *testing.T, be taskBackend) {
	t.Setenv("WBT_STRICT_VAGUENESS", taskStrictOff)
	h := be.newHandler(injectResolveErr(fmt.Errorf("injected: %w", proposal.ErrNotFound)))
	title := be.uniqueTitle(t, "batch-resolve-fails")
	id := be.create(t, proposal.TypeTask, taskPayloadJSON(t, title, cleanKindTestDescription, taskKindGeneral))

	rec := postConfirmBatch(t, h, batchActionAccept, id)

	entry := decodeTaskBatchEntry(t, rec.Body.Bytes(), id.String())
	if entry.OK || !entry.Skipped || entry.Error != batchSkippedText {
		t.Errorf("entry = %+v, want ok:false skipped:true error:%q", entry, batchSkippedText)
	}
	requireCount(t, "tasks rows after injected resolve failure", be.taskCount(t, title), 0)
	requireTaskProposalStatus(t, be, id, proposalPending)
}
