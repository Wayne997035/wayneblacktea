package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// spyOutcomeStore is a test-local spy implementing all 7 methods of
// outcome.StoreIface. It counts total calls across every method so a test can
// assert "the outcome layer was never touched" with a single check. Values
// returned are minimal fixtures — callers of this spy only exercise control
// flow, not outcome data itself.
type spyOutcomeStore struct {
	calls int
}

func (sp *spyOutcomeStore) CreateOutcome(_ context.Context, _ outcome.CreateOutcomeParams) (outcome.Outcome, error) {
	sp.calls++
	return outcome.Outcome{ID: uuid.New()}, nil
}

func (sp *spyOutcomeStore) GetOutcomeByID(_ context.Context, id uuid.UUID, _ *uuid.UUID) (outcome.Outcome, error) {
	sp.calls++
	return outcome.Outcome{ID: id}, nil
}

func (sp *spyOutcomeStore) ListRecentOutcomes(_ context.Context, _ *uuid.UUID, _ string, _ int) ([]outcome.Outcome, error) {
	sp.calls++
	return nil, nil
}

func (sp *spyOutcomeStore) CreateEvaluation(_ context.Context, _ outcome.CreateEvaluationParams) (outcome.Evaluation, error) {
	sp.calls++
	return outcome.Evaluation{ID: uuid.New()}, nil
}

func (sp *spyOutcomeStore) ListEvaluationsByOutcomeID(_ context.Context, _ uuid.UUID, _ *uuid.UUID) ([]outcome.Evaluation, error) {
	sp.calls++
	return nil, nil
}

func (sp *spyOutcomeStore) ListFailedOutcomes(_ context.Context, _ *uuid.UUID, _ int) ([]outcome.Outcome, error) {
	sp.calls++
	return nil, nil
}

func (sp *spyOutcomeStore) PruneOlderThan(_ context.Context, _ time.Time) (int64, error) {
	sp.calls++
	return 0, nil
}

// Compile-time guarantee that the spy satisfies the full interface.
var _ outcome.StoreIface = (*spyOutcomeStore)(nil)

// TestStrictVagueness_ParseBool verifies that (*Server).strictVagueness reads
// WBT_STRICT_VAGUENESS through strconv.ParseBool, accepting the canonical
// truthy/falsy set ("1", "t", "true", "True", "TRUE", "T" → true; "0",
// "false", "f", empty, anything not in the truthy set → false).
//
// Round-1 follow-up GTD 6cda1ce2: tightens the previous exact-"true" match
// so operators don't get caught out by `WBT_STRICT_VAGUENESS=1` silently
// running in warn mode.
func TestStrictVagueness_ParseBool(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want bool
	}{
		{"truthy_1", "1", true},
		{"truthy_lower_t", "t", true},
		{"truthy_upper_T", "T", true},
		{"truthy_true", "true", true},
		{"truthy_True", "True", true},
		{"truthy_TRUE", "TRUE", true},
		{"falsy_0", "0", false},
		{"falsy_false", "false", false},
		{"falsy_empty", "", false},
		{"invalid_garbage_falsy", "yes", false},
		{"invalid_two_falsy", "2", false},
	}
	s := &Server{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WBT_STRICT_VAGUENESS", tc.env)
			if got := s.strictVagueness(); got != tc.want {
				t.Errorf("strictVagueness() with env=%q = %v, want %v", tc.env, got, tc.want)
			}
		})
	}
}

// ---- helpers ----

// callListTasks invokes handleListTasks with the given args.
func callListTasks(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleListTasks(context.Background(), req)
	if err != nil {
		t.Fatalf("handleListTasks error: %v", err)
	}
	return res
}

// callGetTask invokes handleGetTask with the given args.
func callGetTask(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleGetTask(context.Background(), req)
	if err != nil {
		t.Fatalf("handleGetTask error: %v", err)
	}
	return res
}

// callSetTaskStatus invokes handleSetTaskStatus with the given args.
func callSetTaskStatus(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleSetTaskStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSetTaskStatus error: %v", err)
	}
	return res
}

// seedTaskWithDueDate creates a task via the store (bypasses MCP handler due_date check).
func seedTaskWithDueDate(t *testing.T, s *Server, status string) uuid.UUID {
	t.Helper()
	due := time.Now().Add(24 * time.Hour)
	p := gtd.CreateTaskParams{
		Title:   "task-" + uuid.NewString(),
		DueDate: &due,
	}
	task, err := s.gtd.CreateTask(context.Background(), p)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if status != "" && status != taskStatusPending {
		_, err = s.gtd.UpdateTaskStatus(context.Background(), task.ID, gtd.TaskStatus(status))
		if err != nil {
			t.Fatalf("UpdateTaskStatus to %q: %v", status, err)
		}
	}
	return task.ID
}

// ---- handleListTasks tests ----

func TestListTasks_InvalidStatus(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callListTasks(t, s, map[string]any{"status": "unknown"})
	if !r.IsError {
		t.Fatalf("invalid status must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "status must be") {
		t.Errorf("error should mention valid statuses, got: %s", resultText(r))
	}
}

func TestListTasks_EmptyDB_ReturnsEmptySlice(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callListTasks(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("empty DB should succeed, got: %s", resultText(r))
	}
	var out struct {
		Tasks    []any `json:"tasks"`
		Returned int   `json:"returned"`
		HasMore  bool  `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(resultText(r)), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Returned != 0 || out.HasMore {
		t.Errorf("empty DB: returned=%d has_more=%v, want 0/false", out.Returned, out.HasMore)
	}
}

func TestListTasks_LimitZeroDefaultsTo50(t *testing.T) {
	s := newTestWorkSessionServer(t)
	// Just verify the call succeeds — limit defaulting is internal.
	r := callListTasks(t, s, map[string]any{"limit": float64(0)})
	if r.IsError {
		t.Fatalf("limit=0 should succeed (defaulted to 50), got: %s", resultText(r))
	}
	var out struct {
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal([]byte(resultText(r)), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Limit != 50 {
		t.Errorf("limit=0 should default to 50, got %d", out.Limit)
	}
}

func TestListTasks_LimitNegativeDefaultsTo50(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callListTasks(t, s, map[string]any{"limit": float64(-5)})
	if r.IsError {
		t.Fatalf("limit=-5 should succeed, got: %s", resultText(r))
	}
	var out struct {
		Limit int `json:"limit"`
	}
	_ = json.Unmarshal([]byte(resultText(r)), &out)
	if out.Limit != 50 {
		t.Errorf("limit<0 should default to 50, got %d", out.Limit)
	}
}

func TestListTasks_LimitClampsTo200(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callListTasks(t, s, map[string]any{"limit": float64(999)})
	if r.IsError {
		t.Fatalf("limit=999 should succeed (clamped), got: %s", resultText(r))
	}
	var out struct {
		Limit int `json:"limit"`
	}
	_ = json.Unmarshal([]byte(resultText(r)), &out)
	if out.Limit != 200 {
		t.Errorf("limit=999 should clamp to 200, got %d", out.Limit)
	}
}

func TestListTasks_OffsetPastEnd_EmptyHasMoreFalse(t *testing.T) {
	s := newTestWorkSessionServer(t)
	seedTaskWithDueDate(t, s, "")
	r := callListTasks(t, s, map[string]any{"offset": float64(1000)})
	if r.IsError {
		t.Fatalf("offset past end should succeed, got: %s", resultText(r))
	}
	var out struct {
		Returned int  `json:"returned"`
		HasMore  bool `json:"has_more"`
	}
	_ = json.Unmarshal([]byte(resultText(r)), &out)
	if out.Returned != 0 || out.HasMore {
		t.Errorf("offset past end: returned=%d has_more=%v, want 0/false", out.Returned, out.HasMore)
	}
}

func TestListTasks_StatusCompleted_ReturnsThem(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTaskWithDueDate(t, s, "completed")
	r := callListTasks(t, s, map[string]any{"status": "completed"})
	if r.IsError {
		t.Fatalf("status=completed should succeed, got: %s", resultText(r))
	}
	var out struct {
		Tasks   []json.RawMessage `json:"tasks"`
		Summary bool              `json:"summary"`
	}
	_ = json.Unmarshal([]byte(resultText(r)), &out)
	if len(out.Tasks) == 0 {
		t.Fatal("expected at least one completed task")
	}
	// Verify the ID appears in the output.
	if !strings.Contains(resultText(r), id.String()) {
		t.Errorf("completed task ID %s not found in response", id)
	}
}

func TestListTasks_StatusCancelled_ReturnsThem(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTaskWithDueDate(t, s, "cancelled")
	r := callListTasks(t, s, map[string]any{"status": "cancelled"})
	if r.IsError {
		t.Fatalf("status=cancelled should succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), id.String()) {
		t.Errorf("cancelled task %s not found in response", id)
	}
}

func TestListTasks_StatusAll_ReturnsOpenAndTerminal(t *testing.T) {
	s := newTestWorkSessionServer(t)
	pendingID := seedTaskWithDueDate(t, s, "")
	completedID := seedTaskWithDueDate(t, s, "completed")
	cancelledID := seedTaskWithDueDate(t, s, "cancelled")
	r := callListTasks(t, s, map[string]any{"status": "all"})
	if r.IsError {
		t.Fatalf("status=all should succeed, got: %s", resultText(r))
	}
	for _, id := range []uuid.UUID{pendingID, completedID, cancelledID} {
		if !strings.Contains(resultText(r), id.String()) {
			t.Errorf("status=all response missing task %s: %s", id, resultText(r))
		}
	}
}

func TestListTasks_SummaryFalse_FullObjects(t *testing.T) {
	s := newTestWorkSessionServer(t)
	seedTaskWithDueDate(t, s, "")
	r := callListTasks(t, s, map[string]any{"summary": false})
	if r.IsError {
		t.Fatalf("summary=false should succeed, got: %s", resultText(r))
	}
	var out struct {
		Summary bool `json:"summary"`
	}
	_ = json.Unmarshal([]byte(resultText(r)), &out)
	if out.Summary {
		t.Error("summary field should be false in response")
	}
	// Full objects have a 'description' field that compact summaries omit.
	if !strings.Contains(resultText(r), `"description"`) {
		t.Errorf("summary=false should include full task with description field, got: %s", resultText(r))
	}
}

func TestListTasks_HasMore_LimitPlusOneDetection(t *testing.T) {
	s := newTestWorkSessionServer(t)
	// Seed 3 tasks; request limit=2 — has_more must be true.
	for i := 0; i < 3; i++ {
		seedTaskWithDueDate(t, s, "")
	}
	r := callListTasks(t, s, map[string]any{"limit": float64(2)})
	if r.IsError {
		t.Fatalf("should succeed, got: %s", resultText(r))
	}
	var out struct {
		Returned int  `json:"returned"`
		HasMore  bool `json:"has_more"`
	}
	_ = json.Unmarshal([]byte(resultText(r)), &out)
	if out.Returned != 2 {
		t.Errorf("returned=%d, want 2", out.Returned)
	}
	if !out.HasMore {
		t.Error("has_more should be true when more rows exist")
	}
}

func TestListTasks_ProjectIDAndStatusCombo(t *testing.T) {
	s := newTestWorkSessionServer(t)
	// Seed a task under a project and one without.
	proj, err := s.gtd.CreateProject(context.Background(), gtd.CreateProjectParams{
		Name:  "proj-" + uuid.NewString()[:8],
		Title: "proj",
		Area:  "eng",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	due := time.Now().Add(24 * time.Hour)
	projTask, err := s.gtd.CreateTask(context.Background(), gtd.CreateTaskParams{
		Title:     "project-task",
		ProjectID: &proj.ID,
		DueDate:   &due,
	})
	if err != nil {
		t.Fatalf("CreateTask (project): %v", err)
	}
	// Seed another task without a project.
	seedTaskWithDueDate(t, s, "")

	r := callListTasks(t, s, map[string]any{
		"project_id": proj.ID.String(),
		"status":     "active",
	})
	if r.IsError {
		t.Fatalf("project_id+status combo should succeed, got: %s", resultText(r))
	}
	// Only the project-scoped task should appear.
	if !strings.Contains(resultText(r), projTask.ID.String()) {
		t.Errorf("project task %s not found", projTask.ID)
	}
}

// assertKeyPresence checks each key in keys against task and reports an error
// through t when its presence doesn't match wantPresent.
func assertKeyPresence(t *testing.T, task map[string]any, keys []string, wantPresent bool, label string) {
	t.Helper()
	for _, key := range keys {
		_, present := task[key]
		if present == wantPresent {
			continue
		}
		if wantPresent {
			t.Errorf("%s: must contain key %q, got: %+v", label, key, task)
		} else {
			t.Errorf("%s: must NOT contain key %q, got value: %v", label, key, task[key])
		}
	}
}

// firstListedTask unmarshals a list_tasks CallToolResult and returns the
// first task as a generic map (for presence/absence field assertions).
func firstListedTask(t *testing.T, r *mcpmsg.CallToolResult, label string) map[string]any {
	t.Helper()
	if r.IsError {
		t.Fatalf("%s: list_tasks should succeed, got: %s", label, resultText(r))
	}
	var out struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(resultText(r)), &out); err != nil {
		t.Fatalf("%s: unmarshal: %v", label, err)
	}
	if len(out.Tasks) == 0 {
		t.Fatalf("%s: expected at least one task in response", label)
	}
	return out.Tasks[0]
}

// TestListTasks_DefaultSummary_OmitsHeavyFields pins the compact default shape
// of handleListTasks: with no "summary" key present, heavy fields (description,
// context, checklist, commit_shas, pr_url, artifact) MUST be entirely absent
// from the wire object (not present-but-null), while compact fields remain.
// A companion "summary": false call on the SAME task proves the fields were
// genuinely populated (i.e. the default-path assertion isn't a false negative
// caused by a seeding bug).
func TestListTasks_DefaultSummary_OmitsHeavyFields(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()
	due := time.Now().Add(24 * time.Hour)
	prURL := "https://github.com/example/repo/pull/42"

	task, err := s.gtd.CreateTask(ctx, gtd.CreateTaskParams{
		Title:       "heavy-field-task",
		Description: "a full description",
		Context:     "some background context",
		PRUrl:       &prURL,
		DueDate:     &due,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if _, err := s.gtd.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{
		CommitSHAs: []string{"abc1234"},
	}); err != nil {
		t.Fatalf("UpdateTask (commit_shas): %v", err)
	}

	wsID := workspaceUUIDFromPgtype(s.gtd.WorkspaceID())
	checklistItem := gtd.ChecklistItem{
		ID:        uuid.New(),
		Title:     "step one",
		CreatedAt: time.Now().UTC(),
	}
	if _, err := s.gtd.AddChecklistItem(ctx, task.ID, wsID, checklistItem); err != nil {
		t.Fatalf("AddChecklistItem: %v", err)
	}

	artifact := prURL
	if _, err := s.gtd.CompleteTask(ctx, task.ID, &artifact); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	heavyFieldKeys := []string{"description", "context", "checklist", "commit_shas", "pr_url", "artifact"}
	compactFieldKeys := []string{"id", "title", "status", "priority", "kind"}

	// Default path: no "summary" key at all. status=all so the now-completed
	// task is still returned (default status filter is active-only).
	r := callListTasks(t, s, map[string]any{"status": "all"})
	defaultTask := firstListedTask(t, r, "default")
	assertKeyPresence(t, defaultTask, heavyFieldKeys, false, "default (summary)")
	assertKeyPresence(t, defaultTask, compactFieldKeys, true, "default (summary)")

	// Companion call: "summary": false on the SAME task must include the heavy
	// fields — proves the absence above reflects real data, not a seeding bug.
	r2 := callListTasks(t, s, map[string]any{"status": "all", "summary": false})
	fullTask := firstListedTask(t, r2, "summary=false")
	assertKeyPresence(t, fullTask, heavyFieldKeys, true, "summary=false")
}

func TestListTasks_InvalidProjectIDUUID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callListTasks(t, s, map[string]any{"project_id": "not-a-uuid"})
	if !r.IsError {
		t.Fatalf("invalid project_id UUID must error, got: %s", resultText(r))
	}
}

// ---- handleGetTask tests ----

func TestGetTask_HappyPath(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTaskWithDueDate(t, s, "")
	r := callGetTask(t, s, map[string]any{"task_id": id.String()})
	if r.IsError {
		t.Fatalf("get_task happy path error: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), id.String()) {
		t.Errorf("task ID %s not found in response", id)
	}
}

func TestGetTask_CompletedTaskRetrievable(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTaskWithDueDate(t, s, "completed")
	r := callGetTask(t, s, map[string]any{"task_id": id.String()})
	if r.IsError {
		t.Fatalf("get_task for completed task must succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), `"completed"`) {
		t.Errorf("task status should be completed, got: %s", resultText(r))
	}
}

func TestGetTask_CancelledTaskRetrievable(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTaskWithDueDate(t, s, "cancelled")
	r := callGetTask(t, s, map[string]any{"task_id": id.String()})
	if r.IsError {
		t.Fatalf("get_task for cancelled task must succeed, got: %s", resultText(r))
	}
}

func TestGetTask_NotFound(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callGetTask(t, s, map[string]any{"task_id": uuid.New().String()})
	if !r.IsError {
		t.Fatalf("non-existent task must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "not found") {
		t.Errorf("error should say 'not found', got: %s", resultText(r))
	}
}

func TestGetTask_MissingTaskID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callGetTask(t, s, map[string]any{})
	if !r.IsError {
		t.Fatalf("missing task_id must error, got: %s", resultText(r))
	}
}

func TestGetTask_InvalidUUID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callGetTask(t, s, map[string]any{"task_id": "bad-uuid"})
	if !r.IsError {
		t.Fatalf("invalid UUID must error, got: %s", resultText(r))
	}
}

// ---- handleSetTaskStatus tests ----

func TestSetTaskStatus_SameToSame_NoOp(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTaskWithDueDate(t, s, "pending")
	r := callSetTaskStatus(t, s, map[string]any{"task_id": id.String(), "status": "pending"})
	if r.IsError {
		t.Fatalf("same→same no-op must succeed, got: %s", resultText(r))
	}
	// Task must still be pending.
	task, err := s.gtd.GetTaskByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if task.Status != "pending" {
		t.Errorf("status changed on no-op: %s", task.Status)
	}
}

func TestSetTaskStatus_PendingToInProgress(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTaskWithDueDate(t, s, "")
	r := callSetTaskStatus(t, s, map[string]any{"task_id": id.String(), "status": "in_progress"})
	if r.IsError {
		t.Fatalf("pending→in_progress must succeed, got: %s", resultText(r))
	}
	task, _ := s.gtd.GetTaskByID(context.Background(), id)
	if task.Status != statusInProgress {
		t.Errorf("status = %q, want in_progress", task.Status)
	}
}

func TestSetTaskStatus_CompletedToCancelled(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTaskWithDueDate(t, s, "completed")
	r := callSetTaskStatus(t, s, map[string]any{"task_id": id.String(), "status": "cancelled"})
	if r.IsError {
		t.Fatalf("completed→cancelled must succeed, got: %s", resultText(r))
	}
}

func TestSetTaskStatus_CancelledToCompleted(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTaskWithDueDate(t, s, "cancelled")
	r := callSetTaskStatus(t, s, map[string]any{"task_id": id.String(), "status": "completed"})
	if r.IsError {
		t.Fatalf("cancelled→completed (reopen) must succeed, got: %s", resultText(r))
	}
}

// TestSetTaskStatus_ReopenAndRedo_NoOutcomeWritten verifies reopen→re-complete
// via set_task_status does NOT touch the outcome store at all — a spy wired
// onto s.outcome must have a zero call count after both transitions. A
// companion control assertion calls the real record_outcome handler with the
// same spy wired, proving the spy actually detects calls when they occur
// (otherwise a spy that never fires isn't a meaningful assertion).
func TestSetTaskStatus_ReopenAndRedo_NoOutcomeWritten(t *testing.T) {
	s := newTestWorkSessionServer(t)
	spy := &spyOutcomeStore{}
	s.outcome = spy

	id := seedTaskWithDueDate(t, s, "completed")
	// Reopen.
	r := callSetTaskStatus(t, s, map[string]any{"task_id": id.String(), "status": "pending"})
	if r.IsError {
		t.Fatalf("reopen must succeed, got: %s", resultText(r))
	}
	// Re-complete via set_task_status (NOT complete_task, which has side effects).
	r = callSetTaskStatus(t, s, map[string]any{"task_id": id.String(), "status": "completed"})
	if r.IsError {
		t.Fatalf("re-complete must succeed, got: %s", resultText(r))
	}
	if spy.calls != 0 {
		t.Errorf("set_task_status reopen+re-complete must not touch the outcome store, got %d call(s)", spy.calls)
	}

	// Control assertion: record_outcome IS a genuine outcome writer. Calling
	// it directly with the same spy wired must increment the counter — this
	// proves the zero-count assertion above is meaningful, not a spy that
	// silently never fires regardless of what's called.
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"entity_type": "task",
		"entity_id":   id.String(),
		"result":      "success",
	}
	recRes, err := s.handleRecordOutcome(context.Background(), req)
	if err != nil {
		t.Fatalf("handleRecordOutcome error: %v", err)
	}
	if recRes.IsError {
		t.Fatalf("record_outcome control call must succeed, got: %s", resultText(recRes))
	}
	if spy.calls == 0 {
		t.Fatal("control assertion failed: spy did not detect record_outcome's CreateOutcome call — spy is not meaningful")
	}
}

func TestSetTaskStatus_NotFound(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callSetTaskStatus(t, s, map[string]any{"task_id": uuid.New().String(), "status": "in_progress"})
	if !r.IsError {
		t.Fatalf("non-existent task must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "not found") {
		t.Errorf("error should say 'not found', got: %s", resultText(r))
	}
}

func TestSetTaskStatus_InvalidStatus(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTaskWithDueDate(t, s, "")
	r := callSetTaskStatus(t, s, map[string]any{"task_id": id.String(), "status": "done"})
	if !r.IsError {
		t.Fatalf("invalid status must error, got: %s", resultText(r))
	}
}

func TestSetTaskStatus_MissingTaskID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callSetTaskStatus(t, s, map[string]any{"status": "pending"})
	if !r.IsError {
		t.Fatalf("missing task_id must error, got: %s", resultText(r))
	}
}

// ---- handleAddTask due_date HARD-REQUIRE tests ----

func TestAddTask_MissingDueDate_HardError(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callAddTask(t, s, map[string]any{
		"title": "no due date task",
	})
	if !r.IsError {
		t.Fatalf("missing due_date must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "due_date") {
		t.Errorf("error should mention due_date, got: %s", resultText(r))
	}
}

func TestAddTask_InvalidDueDateFormat_HardError(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callAddTask(t, s, map[string]any{
		"title":    "bad due date",
		"due_date": "2026-12-31", // not RFC3339
	})
	if !r.IsError {
		t.Fatalf("invalid due_date format must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "RFC3339") {
		t.Errorf("error should mention RFC3339, got: %s", resultText(r))
	}
}

func TestAddTask_ValidDueDate_TaskCreated(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callAddTask(t, s, map[string]any{
		"title":    "task with due date",
		"due_date": "2026-12-31T00:00:00Z",
	})
	if r.IsError {
		t.Fatalf("valid due_date should create task, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "task with due date") {
		t.Errorf("task title not found in response, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "2026-12-31") {
		t.Errorf("due_date not found in response, got: %s", resultText(r))
	}
}

func TestAddTask_EmptyDueDate_HardError(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callAddTask(t, s, map[string]any{
		"title":    "empty due date",
		"due_date": "",
	})
	if !r.IsError {
		t.Fatalf("empty due_date must error, got: %s", resultText(r))
	}
}
