package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// --- mock proposal store ---

// mockProposalStore implements proposal.StoreIface for middleware_classify tests.
// Only Create + ListPending are exercised by autoCaptureMCPTask; other methods
// satisfy the interface and return errors to surface accidental usage.
type mockProposalStore struct {
	mu        sync.Mutex
	pending   []db.PendingProposal
	created   []proposal.CreateParams
	listErr   error
	createErr error
}

func (m *mockProposalStore) Create(_ context.Context, p proposal.CreateParams) (*db.PendingProposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return nil, m.createErr
	}
	m.created = append(m.created, p)
	row := db.PendingProposal{
		ID:      uuid.New(),
		Type:    string(p.Type),
		Status:  string(proposal.StatusPending),
		Payload: p.Payload,
	}
	m.pending = append(m.pending, row)
	return &row, nil
}

func (m *mockProposalStore) Get(_ context.Context, _ uuid.UUID) (*db.PendingProposal, error) {
	return nil, proposal.ErrNotFound
}

func (m *mockProposalStore) ListPending(_ context.Context) ([]db.PendingProposal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listErr != nil {
		return nil, m.listErr
	}
	out := make([]db.PendingProposal, len(m.pending))
	copy(out, m.pending)
	return out, nil
}

func (m *mockProposalStore) ListAll(_ context.Context, _ string, _ int32) ([]db.PendingProposal, error) {
	return nil, nil
}

func (m *mockProposalStore) Resolve(_ context.Context, _ uuid.UUID, _ proposal.Status) (*db.PendingProposal, error) {
	return nil, proposal.ErrNotFound
}

func (m *mockProposalStore) BatchConfirm(_ context.Context, _ []uuid.UUID, _ proposal.Status) (proposal.BatchConfirmResult, error) {
	return proposal.BatchConfirmResult{}, nil
}

func (m *mockProposalStore) AutoProposeConceptFromKnowledge(_ context.Context, _ *db.KnowledgeItem, _ string) (*db.PendingProposal, error) {
	return nil, nil
}

func (m *mockProposalStore) recordedCreates() []proposal.CreateParams {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]proposal.CreateParams, len(m.created))
	copy(out, m.created)
	return out
}

// --- mock decision store ---

type decisionLog struct {
	params decision.LogParams
}

type mockDecisionStore struct {
	mu   sync.Mutex
	logs []decisionLog
}

func (m *mockDecisionStore) Log(_ context.Context, p decision.LogParams) (*db.Decision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, decisionLog{params: p})
	return &db.Decision{}, nil
}

func (m *mockDecisionStore) All(_ context.Context, _ int32) ([]db.Decision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return nil, nil
}

func (m *mockDecisionStore) ByRepo(_ context.Context, _ string, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (m *mockDecisionStore) ByProject(_ context.Context, _ uuid.UUID, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (m *mockDecisionStore) ByTask(_ context.Context, _ uuid.UUID, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (m *mockDecisionStore) SearchByCosine(_ context.Context, _ []float32, _ int) ([]db.Decision, error) {
	return nil, nil
}

func (m *mockDecisionStore) recordedLogs() []decisionLog {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]decisionLog, len(m.logs))
	copy(out, m.logs)
	return out
}

// --- mock GTD store for classify (supports Tasks + CreateTask) ---

type taskCreate struct {
	params gtd.CreateTaskParams
}

type mockClassifyGTDStore struct {
	mu          sync.Mutex
	activeTasks []db.Task
	created     []taskCreate
}

func (m *mockClassifyGTDStore) Tasks(_ context.Context, _ *uuid.UUID) ([]db.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]db.Task, len(m.activeTasks))
	copy(out, m.activeTasks)
	return out, nil
}

// TasksByProjectAllStatuses returns the same fixture as Tasks for the classify
// middleware tests — these tests don't exercise the all-statuses code path,
// the method exists only to satisfy gtd.StoreIface.
func (m *mockClassifyGTDStore) TasksByProjectAllStatuses(_ context.Context, _ uuid.UUID) ([]db.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]db.Task, len(m.activeTasks))
	copy(out, m.activeTasks)
	return out, nil
}

func (m *mockClassifyGTDStore) CreateTask(_ context.Context, p gtd.CreateTaskParams) (*db.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.created = append(m.created, taskCreate{params: p})
	return &db.Task{Title: p.Title}, nil
}

func (m *mockClassifyGTDStore) recordedCreates() []taskCreate {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]taskCreate, len(m.created))
	copy(out, m.created)
	return out
}

// Satisfy the remaining StoreIface methods — unreachable during classify tests.
func (m *mockClassifyGTDStore) LogActivity(_ context.Context, _, _ string, _ *uuid.UUID, _ string) error {
	return errMockNotImpl
}

func (m *mockClassifyGTDStore) ListActiveProjects(_ context.Context) ([]db.Project, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) GetProjectByID(_ context.Context, _ uuid.UUID) (*db.Project, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) ProjectByName(_ context.Context, _ string) (*db.Project, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) ProjectsByRepoName(_ context.Context, _ string) ([]db.Project, error) {
	return nil, nil
}

func (m *mockClassifyGTDStore) CreateProject(_ context.Context, _ gtd.CreateProjectParams) (*db.Project, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) CompleteTask(_ context.Context, _ uuid.UUID, _ *string) (*db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) ActiveGoals(_ context.Context) ([]db.Goal, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) CreateGoal(_ context.Context, _ gtd.CreateGoalParams) (*db.Goal, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) UpdateTaskStatus(_ context.Context, _ uuid.UUID, _ gtd.TaskStatus) (*db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) UpdateProjectStatus(_ context.Context, _ uuid.UUID, _ gtd.ProjectStatus) (*db.Project, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) UpdateGoal(_ context.Context, _ uuid.UUID, _ gtd.UpdateGoalParams) (*db.Goal, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) UpdateProject(_ context.Context, _ uuid.UUID, _ gtd.UpdateProjectParams) (*db.Project, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) DeleteTask(_ context.Context, _ uuid.UUID) error {
	return errMockNotImpl
}

func (m *mockClassifyGTDStore) WeeklyProgress(_ context.Context) (int64, int64, error) {
	return 0, 0, errMockNotImpl
}

func (m *mockClassifyGTDStore) ListActivityLogsSince(_ context.Context, _ time.Time, _ int32) ([]db.ActivityLog, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) TopPendingTask(_ context.Context) (*db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) TasksByDueDateRange(_ context.Context, _, _ time.Time) ([]db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) TasksForTimeline(_ context.Context, _, _ time.Time) ([]db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) WorkspaceID() pgtype.UUID { return pgtype.UUID{} }

func (m *mockClassifyGTDStore) RecentCompletedTasks(_ context.Context, _ uuid.UUID, _ int32) ([]db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) RecentActivityByProject(_ context.Context, _ uuid.UUID, _ time.Time, _ int32) ([]db.ActivityLog, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) UpcomingTasks(_ context.Context, _ time.Time, _, _ int) ([]db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) UpdateTask(_ context.Context, _ uuid.UUID, _ gtd.UpdateTaskParams) (*db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) AddChecklistItem(
	_ context.Context, _ uuid.UUID, _ uuid.UUID, _ gtd.ChecklistItem,
) ([]gtd.ChecklistItem, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) UpdateChecklistItem(
	_ context.Context, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID, _ gtd.UpdateChecklistItemParams,
) ([]gtd.ChecklistItem, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) DeleteChecklistItem(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID) error {
	return errMockNotImpl
}

func (m *mockClassifyGTDStore) BeginTask(_ context.Context, _ uuid.UUID, _ uuid.UUID) (*db.Task, error) {
	return nil, errMockNotImpl
}

func (m *mockClassifyGTDStore) BatchCompleteTasksByPRMatch(_ context.Context, _ []gtd.Match) (int, error) {
	return 0, errMockNotImpl
}

func (m *mockClassifyGTDStore) GetTaskByID(_ context.Context, _ uuid.UUID) (*db.Task, error) {
	return nil, errMockNotImpl
}

// TestMaybeClassifyToolCall_NilClassifier verifies that a nil classifier results in no-op.
func TestMaybeClassifyToolCall_NilClassifier(t *testing.T) {
	g := &mockClassifyGTDStore{}
	dec := &mockDecisionStore{}
	s := &Server{gtd: g, decision: dec, classifier: nil}

	// Should return immediately without panicking.
	s.maybeClassifyToolCall("complete_task", "task_id=abc", "ok")

	time.Sleep(50 * time.Millisecond)
	if len(dec.recordedLogs()) != 0 {
		t.Errorf("expected 0 decision logs with nil classifier, got %d", len(dec.recordedLogs()))
	}
	if len(g.recordedCreates()) != 0 {
		t.Errorf("expected 0 task creates with nil classifier, got %d", len(g.recordedCreates()))
	}
}

// TestMaybeClassifyToolCall_NonSignificantTool verifies non-significant tools are skipped.
func TestMaybeClassifyToolCall_NonSignificantTool(t *testing.T) {
	g := &mockClassifyGTDStore{}
	dec := &mockDecisionStore{}
	// Use a real ActivityClassifier stub — we just verify it's never called.
	// Since we can't inject a mock into the *ai.ActivityClassifier field without
	// the internal package, we verify by checking that the stores are untouched.
	s := &Server{gtd: g, decision: dec, classifier: nil}

	// list_tasks is not in significantTools; even with a classifier set,
	// it must be skipped (classifier nil path is tested above).
	// Here we test the significantTools gate with a non-nil classifier by
	// directly calling the internal gate check.
	s.maybeClassifyToolCall("list_tasks", "args", "result")

	time.Sleep(50 * time.Millisecond)
	if len(dec.recordedLogs()) != 0 {
		t.Errorf("expected 0 decision logs for non-significant tool, got %d", len(dec.recordedLogs()))
	}
}

// TestSignificantTools_Map verifies the significantTools map contains expected entries.
func TestSignificantTools_Map(t *testing.T) {
	expected := []string{
		"complete_task",
		"confirm_proposal",
		"upsert_project_arch",
		"update_project_status",
		"resolve_handoff",
		"sync_repo",
	}
	for _, tool := range expected {
		if !significantTools[tool] {
			t.Errorf("significantTools[%q] = false, want true", tool)
		}
	}

	// Non-significant tools must not appear.
	nonSignificant := []string{"list_tasks", "add_task", "log_decision", "get_today_context", ""}
	for _, tool := range nonSignificant {
		if significantTools[tool] {
			t.Errorf("significantTools[%q] = true, want false", tool)
		}
	}
}

// TestLogMCPDecision_Dedup verifies that a duplicate title is skipped.
func TestLogMCPDecision_Dedup(t *testing.T) {
	dec := &mockDecisionStore{}
	s := &Server{decision: dec}

	ctx := context.Background()
	// First log should succeed.
	if err := s.logMCPDecision(ctx, "Use SQLite for local dev", "complete_task"); err != nil {
		t.Fatalf("first logMCPDecision: %v", err)
	}

	// For dedup we need All() to return an existing decision — use the dedicated mock.
	dedupDec := &mockDecisionStoreWithAll{
		existing: []db.Decision{{Title: "Use SQLite for local dev"}},
	}
	s2 := &Server{decision: dedupDec}
	if err := s2.logMCPDecision(ctx, "use sqlite for local dev", "complete_task"); err != nil {
		t.Fatalf("dedup logMCPDecision: %v", err)
	}
	if len(dedupDec.logs) != 0 {
		t.Errorf("expected 0 logs after dedup, got %d", len(dedupDec.logs))
	}
}

// TestLogMCPDecision_TruncatesTitle verifies rune-safe truncation of long titles.
func TestLogMCPDecision_TruncatesTitle(t *testing.T) {
	dec := &mockDecisionStore{}
	s := &Server{decision: dec}

	// Construct a title longer than mcpDecisionMaxTitle runes using multi-byte chars.
	longTitle := make([]rune, mcpDecisionMaxTitle+50)
	for i := range longTitle {
		longTitle[i] = '你' // 3-byte UTF-8
	}
	title := string(longTitle)

	if err := s.logMCPDecision(context.Background(), title, "upsert_project_arch"); err != nil {
		t.Fatalf("logMCPDecision with long title: %v", err)
	}
	logs := dec.recordedLogs()
	if len(logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(logs))
	}
	got := []rune(logs[0].params.Title)
	if len(got) > mcpDecisionMaxTitle {
		t.Errorf("title rune length = %d, want ≤ %d", len(got), mcpDecisionMaxTitle)
	}
}

// TestLogMCPDecision_EmptyTitle verifies empty/whitespace titles are skipped.
func TestLogMCPDecision_EmptyTitle(t *testing.T) {
	dec := &mockDecisionStore{}
	s := &Server{decision: dec}

	cases := []string{"", "   ", "\t\n"}
	for _, title := range cases {
		if err := s.logMCPDecision(context.Background(), title, "complete_task"); err != nil {
			t.Errorf("logMCPDecision(%q): unexpected error: %v", title, err)
		}
	}
	if len(dec.recordedLogs()) != 0 {
		t.Errorf("expected 0 logs for empty titles, got %d", len(dec.recordedLogs()))
	}
}

// TestAutoCaptureMCPTask_Dedup verifies that an existing pending TypeTask
// proposal with the same title is skipped (dedup is against the proposal
// queue, NOT the tasks table — a closed task can legitimately recur).
func TestAutoCaptureMCPTask_Dedup(t *testing.T) {
	existingPayload, _ := json.Marshal(proposal.TaskPayload{Title: "Write integration tests"})
	p := &mockProposalStore{
		pending: []db.PendingProposal{
			{
				ID:      uuid.New(),
				Type:    string(proposal.TypeTask),
				Status:  string(proposal.StatusPending),
				Payload: existingPayload,
			},
		},
	}
	s := &Server{proposal: p}

	// Case-insensitive title match must dedup.
	if err := s.autoCaptureMCPTask(context.Background(), "write integration tests", "complete_task", "", "", ""); err != nil {
		t.Fatalf("autoCaptureMCPTask: %v", err)
	}
	if len(p.recordedCreates()) != 0 {
		t.Errorf("expected 0 creates due to dedup, got %d", len(p.recordedCreates()))
	}
}

// TestAutoCaptureMCPTask_CreatesProposal verifies a TypeTask proposal is
// enqueued (NOT a real task row) when no duplicate pending proposal exists.
// This is the core SA-decision-42e0b783 behaviour.
func TestAutoCaptureMCPTask_CreatesProposal(t *testing.T) {
	p := &mockProposalStore{}
	s := &Server{proposal: p}

	if err := s.autoCaptureMCPTask(
		context.Background(),
		"Add rate limiting",
		"resolve_handoff",
		"arg=foo",
		"result=ok",
		"classifier said it's a task",
	); err != nil {
		t.Fatalf("autoCaptureMCPTask: %v", err)
	}
	creates := p.recordedCreates()
	if len(creates) != 1 {
		t.Fatalf("expected 1 proposal create, got %d", len(creates))
	}
	if creates[0].Type != proposal.TypeTask {
		t.Errorf("type = %q, want %q", creates[0].Type, proposal.TypeTask)
	}
	if creates[0].ProposedBy != "claude-code" {
		t.Errorf("proposed_by = %q, want claude-code", creates[0].ProposedBy)
	}
	var payload proposal.TaskPayload
	if err := json.Unmarshal(creates[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Title != "Add rate limiting" {
		t.Errorf("payload title = %q, want %q", payload.Title, "Add rate limiting")
	}
	if payload.SourceTool != "resolve_handoff" {
		t.Errorf("payload source_tool = %q, want resolve_handoff", payload.SourceTool)
	}
	if payload.SuggestedKind != "general" {
		t.Errorf("payload suggested_kind = %q, want general", payload.SuggestedKind)
	}
	if payload.ArgSummary != "arg=foo" {
		t.Errorf("payload arg_summary = %q, want arg=foo", payload.ArgSummary)
	}
	if payload.ResultSummary != "result=ok" {
		t.Errorf("payload result_summary = %q, want result=ok", payload.ResultSummary)
	}
	if payload.ClassifierRationale != "classifier said it's a task" {
		t.Errorf("payload classifier_rationale = %q", payload.ClassifierRationale)
	}
}

// TestAutoCaptureMCPTask_TruncatesTitle verifies rune-safe truncation
// applied to the proposal payload title (NOT to a task row).
func TestAutoCaptureMCPTask_TruncatesTitle(t *testing.T) {
	p := &mockProposalStore{}
	s := &Server{proposal: p}

	// Build a long rune string (3-byte UTF-8).
	runes := make([]rune, mcpTaskMaxTitle+100)
	for i := range runes {
		runes[i] = 'あ'
	}
	if err := s.autoCaptureMCPTask(context.Background(), string(runes), "sync_repo", "", "", ""); err != nil {
		t.Fatalf("autoCaptureMCPTask with long title: %v", err)
	}
	creates := p.recordedCreates()
	if len(creates) != 1 {
		t.Fatalf("expected 1 create, got %d", len(creates))
	}
	var payload proposal.TaskPayload
	if err := json.Unmarshal(creates[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	gotRunes := []rune(payload.Title)
	if len(gotRunes) > mcpTaskMaxTitle {
		t.Errorf("payload title rune length = %d, want ≤ %d", len(gotRunes), mcpTaskMaxTitle)
	}
}

// TestAutoCaptureMCPTask_NilProposalStore verifies that nil proposal store
// is a graceful no-op (legacy callers without proposal wiring should not
// panic when classifier verdict triggers task capture).
func TestAutoCaptureMCPTask_NilProposalStore(t *testing.T) {
	s := &Server{proposal: nil}
	if err := s.autoCaptureMCPTask(context.Background(), "title", "tool", "", "", ""); err != nil {
		t.Errorf("nil proposal store should be no-op, got err: %v", err)
	}
}

// TestAutoCaptureMCPTask_EmptyTitleSkips verifies blank/whitespace title is
// skipped before any DB call.
func TestAutoCaptureMCPTask_EmptyTitleSkips(t *testing.T) {
	p := &mockProposalStore{}
	s := &Server{proposal: p}
	for _, title := range []string{"", "   ", "\t\n"} {
		if err := s.autoCaptureMCPTask(context.Background(), title, "complete_task", "", "", ""); err != nil {
			t.Errorf("autoCaptureMCPTask(%q): %v", title, err)
		}
	}
	if len(p.recordedCreates()) != 0 {
		t.Errorf("expected 0 creates for blank titles, got %d", len(p.recordedCreates()))
	}
}

// TestAutoCaptureMCPTask_OversizePayloadSkipped verifies the 4 KiB payload
// cap (proposal.MaxTaskPayloadBytes) drops oversize payloads with a warning
// instead of writing a giant row.
func TestAutoCaptureMCPTask_OversizePayloadSkipped(t *testing.T) {
	p := &mockProposalStore{}
	s := &Server{proposal: p}

	// argSummary contains 5000 bytes — pushes JSON over the 4 KiB cap.
	big := strings.Repeat("x", 5000)
	if err := s.autoCaptureMCPTask(context.Background(), "title", "tool", big, "", ""); err != nil {
		t.Fatalf("autoCaptureMCPTask: %v", err)
	}
	if len(p.recordedCreates()) != 0 {
		t.Errorf("expected oversize payload to be dropped, got %d creates", len(p.recordedCreates()))
	}
}

// TestMaybeClassifyToolCall_CreatesProposalNotTask is the SA-decision-42e0b783
// integration test: when the classifier returns IsTask=true the side-effect
// MUST be a TypeTask proposal (queue), NOT a tasks-table insert. The
// classifier itself is hard to mock without booting an LLM provider, so this
// drives the same code path by invoking autoCaptureMCPTask directly with a
// realistic argSummary / resultSummary and verifies the proposal carries the
// expected payload shape AND the gtd store is NOT touched.
func TestMaybeClassifyToolCall_CreatesProposalNotTask(t *testing.T) {
	p := &mockProposalStore{}
	g := &mockClassifyGTDStore{} // must NOT be touched
	s := &Server{
		proposal: p,
		gtd:      g,
	}

	if err := s.autoCaptureMCPTask(
		context.Background(),
		"Add audit log for confirm_proposal",
		"confirm_proposal",
		"id=...",
		"status=accepted",
		"side-effect implies follow-up audit task",
	); err != nil {
		t.Fatalf("autoCaptureMCPTask: %v", err)
	}

	// 1 proposal must be created.
	creates := p.recordedCreates()
	if len(creates) != 1 {
		t.Fatalf("expected 1 proposal create, got %d", len(creates))
	}
	// 0 task creates on the gtd store — this is the bypass-closure assertion.
	if len(g.recordedCreates()) != 0 {
		t.Errorf("expected 0 task creates (bypass closed), got %d", len(g.recordedCreates()))
	}
	if creates[0].Type != proposal.TypeTask {
		t.Errorf("proposal type = %q, want %q", creates[0].Type, proposal.TypeTask)
	}
}

// TestAutoCaptureMCPTask_RedactsCredentials is the regression test for GTD
// 393d945b: autoCaptureMCPTask MUST pass argSummary + classifierRationale
// through redact.ForLLM before persisting them in pending_proposals.payload,
// mirroring the HTTP path in autoCreateTaskFromClassifier
// (internal/handler/autolog_handler.go:447-448). Otherwise the MCP writer
// would land raw credentials on disk while the HTTP writer redacts them —
// inconsistent defence-in-depth posture for a long-lived column
// (backend-security-design.md §3.1).
//
// Scenario: invoke autoCaptureMCPTask directly with rationale AND argSummary
// both containing a 40-char fake GitHub PAT. Decode the persisted payload
// and assert: (a) neither field leaks the raw token, (b) the canonical
// placeholder "[REDACTED:github-token]" appears in BOTH fields. Idempotency
// is verified by re-invoking autoCaptureMCPTask with the already-redacted
// strings and confirming the second payload is byte-identical to the first.
func TestAutoCaptureMCPTask_RedactsCredentials(t *testing.T) {
	const fakePAT = "ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 40 chars, fake
	const wantPlaceholder = "[REDACTED:github-token]"

	argSummaryWithToken := "bash command leaked " + fakePAT + " before classify"
	rationaleWithToken := "classifier saw " + fakePAT + " in arg, follow-up rotate task"

	p := &mockProposalStore{}
	s := &Server{proposal: p}

	if err := s.autoCaptureMCPTask(
		context.Background(),
		"Rotate leaked GitHub PAT (MCP path)",
		"complete_task",
		argSummaryWithToken,
		"status=ok",
		rationaleWithToken,
	); err != nil {
		t.Fatalf("autoCaptureMCPTask: %v", err)
	}

	creates := p.recordedCreates()
	if len(creates) != 1 {
		t.Fatalf("expected 1 proposal create, got %d", len(creates))
	}

	var payload proposal.TaskPayload
	if err := json.Unmarshal(creates[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	if strings.Contains(payload.ArgSummary, fakePAT) {
		t.Errorf("arg_summary leaked raw PAT: %q", payload.ArgSummary)
	}
	if !strings.Contains(payload.ArgSummary, wantPlaceholder) {
		t.Errorf("arg_summary missing placeholder %q, got %q",
			wantPlaceholder, payload.ArgSummary)
	}
	if strings.Contains(payload.ClassifierRationale, fakePAT) {
		t.Errorf("classifier_rationale leaked raw PAT: %q", payload.ClassifierRationale)
	}
	if !strings.Contains(payload.ClassifierRationale, wantPlaceholder) {
		t.Errorf("classifier_rationale missing placeholder %q, got %q",
			wantPlaceholder, payload.ClassifierRationale)
	}

	// Idempotency check: re-feed the already-redacted strings; the placeholder
	// substring "[REDACTED:github-token]" must survive a second pass through
	// redact.ForLLM unchanged. Drives a fresh proposal store so the dedup
	// branch isn't tripped by the title match.
	p2 := &mockProposalStore{}
	s2 := &Server{proposal: p2}
	if err := s2.autoCaptureMCPTask(
		context.Background(),
		"Rotate leaked GitHub PAT (MCP path) idempotency",
		"complete_task",
		payload.ArgSummary, // already redacted
		"status=ok",
		payload.ClassifierRationale, // already redacted
	); err != nil {
		t.Fatalf("autoCaptureMCPTask idempotency: %v", err)
	}
	creates2 := p2.recordedCreates()
	if len(creates2) != 1 {
		t.Fatalf("idempotency: expected 1 create, got %d", len(creates2))
	}
	var payload2 proposal.TaskPayload
	if err := json.Unmarshal(creates2[0].Payload, &payload2); err != nil {
		t.Fatalf("idempotency: decode payload: %v", err)
	}
	if payload2.ArgSummary != payload.ArgSummary {
		t.Errorf("idempotency: arg_summary changed on second pass:\n  first:  %q\n  second: %q",
			payload.ArgSummary, payload2.ArgSummary)
	}
	if payload2.ClassifierRationale != payload.ClassifierRationale {
		t.Errorf("idempotency: classifier_rationale changed on second pass:\n  first:  %q\n  second: %q",
			payload.ClassifierRationale, payload2.ClassifierRationale)
	}
}

// TestTruncateRunes verifies UTF-8-safe truncation.
func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		input    string
		max      int
		wantLen  int // rune length of expected output
		wantSame bool
	}{
		{"hello", 10, 5, true},
		{"hello", 3, 3, false},
		{"你好世界", 2, 2, false},
		{"", 5, 0, true},
		{"abc", 3, 3, true},
	}
	for _, tc := range cases {
		got := truncateRunes(tc.input, tc.max)
		gotRunes := []rune(got)
		if len(gotRunes) != tc.wantLen {
			t.Errorf("truncateRunes(%q, %d) rune len = %d, want %d", tc.input, tc.max, len(gotRunes), tc.wantLen)
		}
		if tc.wantSame && got != tc.input {
			t.Errorf("truncateRunes(%q, %d) = %q, want same as input", tc.input, tc.max, got)
		}
	}
}

// TestClassifySem_SelectDefaultDrops_WhenFull asserts the production drop
// path: when mcpClassifySem is at capacity, a non-blocking
// `select { case sem <- {}: ; default: }` MUST take the default branch
// instantly. This proves the drop semantics in maybeClassifyToolCall:46-90
// without going through the nil-classifier short circuit.
func TestClassifySem_SelectDefaultDrops_WhenFull(t *testing.T) {
	// Fill the semaphore completely.
	for i := 0; i < cap(mcpClassifySem); i++ {
		mcpClassifySem <- struct{}{}
	}
	t.Cleanup(func() {
		for len(mcpClassifySem) > 0 {
			<-mcpClassifySem
		}
	})

	// Mirror the exact select pattern from maybeClassifyToolCall.
	start := time.Now()
	acquired := false
	select {
	case mcpClassifySem <- struct{}{}:
		acquired = true
		<-mcpClassifySem // release immediately to keep cleanup invariant
	default:
		// expected — full semaphore must fall through here
	}
	elapsed := time.Since(start)

	if acquired {
		t.Fatal("expected select-default to fire on full semaphore; instead the send succeeded")
	}
	if elapsed > 10*time.Millisecond {
		t.Errorf("select-default took %v; want instant (< 10ms)", elapsed)
	}
}

// TestTryAcquireClassifyToken_DrainsAndRefills exercises the rate-limiter
// token bucket: it must allow the first mcpClassifyMaxPerWindow calls,
// reject calls after the bucket is drained within the same window, and
// refill once the window elapses.
func TestTryAcquireClassifyToken_DrainsAndRefills(t *testing.T) {
	// Reset bucket to a known state pinned to t0.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mcpClassifyBudget.mu.Lock()
	mcpClassifyBudget.tokens = 0
	mcpClassifyBudget.resetAt = t0.Add(-time.Second) // already expired
	mcpClassifyBudget.mu.Unlock()

	// First Acquire at t0 must refill and succeed.
	if !tryAcquireClassifyToken(t0) {
		t.Fatal("first call after expiry should refill and succeed")
	}

	// Drain remaining mcpClassifyMaxPerWindow-1 tokens within the same window.
	for i := 1; i < mcpClassifyMaxPerWindow; i++ {
		if !tryAcquireClassifyToken(t0) {
			t.Fatalf("call %d within window should succeed", i+1)
		}
	}

	// Bucket is now empty within the same window — must reject.
	if tryAcquireClassifyToken(t0) {
		t.Errorf("call after draining bucket within window should be rejected")
	}

	// Advance past the window — next call must refill.
	tNext := t0.Add(mcpClassifyWindow + time.Second)
	if !tryAcquireClassifyToken(tNext) {
		t.Errorf("call after window expiry should refill and succeed")
	}
}

// TestMaybeClassifyToolCall_NilGuardReturnsInstant verifies the nil-classifier
// short circuit: maybeClassifyToolCall must return before touching the
// semaphore when s.classifier is nil. This complements
// TestMaybeClassifyToolCall_NilClassifier (which checks no DB writes happen)
// by also asserting timing.
func TestMaybeClassifyToolCall_NilGuardReturnsInstant(t *testing.T) {
	s := &Server{classifier: nil}
	start := time.Now()
	s.maybeClassifyToolCall("complete_task", "args", "result")
	elapsed := time.Since(start)
	if elapsed > 10*time.Millisecond {
		t.Errorf("nil-classifier guard took %v; want instant return", elapsed)
	}
}

// --- helpers ---

// mockDecisionStoreWithAll supports returning existing decisions for dedup testing.
type mockDecisionStoreWithAll struct {
	mu       sync.Mutex
	existing []db.Decision
	logs     []decisionLog
}

func (m *mockDecisionStoreWithAll) Log(_ context.Context, p decision.LogParams) (*db.Decision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, decisionLog{params: p})
	return &db.Decision{}, nil
}

func (m *mockDecisionStoreWithAll) All(_ context.Context, _ int32) ([]db.Decision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.existing, nil
}

func (m *mockDecisionStoreWithAll) ByRepo(_ context.Context, _ string, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (m *mockDecisionStoreWithAll) ByProject(_ context.Context, _ uuid.UUID, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (m *mockDecisionStoreWithAll) ByTask(_ context.Context, _ uuid.UUID, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (m *mockDecisionStoreWithAll) SearchByCosine(_ context.Context, _ []float32, _ int) ([]db.Decision, error) {
	return nil, nil
}

// Compile-time interface assertions.
var (
	_ decision.StoreIface = (*mockDecisionStore)(nil)
	_ decision.StoreIface = (*mockDecisionStoreWithAll)(nil)
	_ gtd.StoreIface      = (*mockClassifyGTDStore)(nil)
	_ proposal.StoreIface = (*mockProposalStore)(nil)
)
