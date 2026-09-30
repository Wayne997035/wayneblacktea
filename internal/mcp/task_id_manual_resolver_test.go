package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// logDecisionToolName is the one occurrence this file needs of the
// "log_decision" tool-name literal (goconst counts it across the whole
// package, and several other files already carry their own instances this
// file has no authority to touch).
const logDecisionToolName = "log_decision"

// fakeByPrefixGTDStore embeds noopGTDStore (same package) and answers
// FindTaskIDsByPrefix deterministically per-prefix, keyed by the exact
// prefix string queried — used where a single test needs two DIFFERENT
// prefix outcomes (one resolves cleanly, one is ambiguous) without needing
// two real colliding UUIDs.
type fakeByPrefixGTDStore struct {
	noopGTDStore
	responses map[string][]gtd.TaskIDTitle
}

func (f fakeByPrefixGTDStore) FindTaskIDsByPrefix(_ context.Context, prefix string, _ int) ([]gtd.TaskIDTitle, error) {
	return f.responses[prefix], nil
}

// pollActivityLogAction polls s.gtd.ListActivityLogsSince (real store, not a
// mock — log_decision runs through a real *Server in these tests) until an
// entry with the given action appears, or the deadline passes. autoLog fires
// in a background goroutine, so this is the poll-based equivalent of
// middleware_autolog_test.go's waitForLogs for a non-mock GTD store.
func pollActivityLogAction(t *testing.T, s *Server, since time.Time, action string, deadline time.Duration) *struct {
	ProjectID *uuid.UUID
} {
	t.Helper()
	timeout := time.After(deadline)
	for {
		logs, err := s.gtd.ListActivityLogsSince(context.Background(), since, 50)
		if err != nil {
			t.Fatalf("ListActivityLogsSince: %v", err)
		}
		for _, l := range logs {
			if l.Action == action {
				out := &struct{ ProjectID *uuid.UUID }{}
				if l.ProjectID.Valid {
					pid := uuid.UUID(l.ProjectID.Bytes)
					out.ProjectID = &pid
				}
				return out
			}
		}
		select {
		case <-timeout:
			t.Fatalf("timed out waiting for activity_log action %q", action)
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// TestLogDecision_TaskIDPrefix covers F0930-19's first manual call site.
// Each case below builds its own *Server (never a shared one across
// t.Parallel() subtests): the "no decision written" cases assert a
// before/after count on s.decision, which would be racy against sibling
// cases writing decisions concurrently on a shared server. Cases are their
// own top-level functions (logDecisionPrefixCase*) so gocyclo scores each
// separately from this dispatcher (same pattern used throughout this
// dispatch — see TestResolveTaskIDPrefix_PG's doc comment).
func TestLogDecision_TaskIDPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"positive: prefix resolves, decision.task_id is the full UUID, autoLog enriches project_id", logDecisionPrefixCasePositive},
		{"negative: 7-char prefix errors and writes no decision", logDecisionPrefixCaseTooShort},
		{"negative: prefix matching no task errors and writes no decision", logDecisionPrefixCaseNotFound},
		{"log_decision with task_id omitted is unchanged (still optional)", logDecisionPrefixCaseOmitted},
	}
	for _, tc := range cases {
		run := tc.run
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); run(t) })
	}
}

func logDecisionPrefixCasePositive(t *testing.T) {
	s := newTestWorkSessionServer(t)
	projID := seedProject(t, s, "log-decision-prefix-proj")
	task, err := s.gtd.CreateTask(context.Background(), gtd.CreateTaskParams{
		Title: "log-decision-prefix-task", ProjectID: &projID,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	since := time.Now().Add(-time.Minute)
	mw := s.autoLogMiddleware()
	wrapped := mw(func(ctx context.Context, req mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
		return s.handleLogDecision(ctx, req)
	})

	req := mcpmsg.CallToolRequest{}
	req.Params.Name = logDecisionToolName
	req.Params.Arguments = map[string]any{
		"title": "prefix decision", "context": "c", "decision": "d", "rationale": "r",
		"task_id": prefixOf(task.ID),
	}
	res, err := wrapped(context.Background(), req)
	if err != nil {
		t.Fatalf("log_decision returned Go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("prefix call should succeed, got: %s", resultText(res))
	}
	if !strings.Contains(resultText(res), task.ID.String()) {
		t.Errorf("decision response should carry the resolved full UUID %s, got: %s", task.ID, resultText(res))
	}

	entry := pollActivityLogAction(t, s, since, "decision:logged", 2*time.Second)
	if entry.ProjectID == nil {
		t.Fatal("autoLog entry has nil project_id — prefix was not resolved before the enrichment lookup ran")
	}
	if *entry.ProjectID != projID {
		t.Errorf("autoLog project_id = %s, want %s", *entry.ProjectID, projID)
	}
}

func logDecisionPrefixCaseTooShort(t *testing.T) {
	s := newTestWorkSessionServer(t)
	before, err := s.decision.List(context.Background(), decision.ListParams{Limit: 100})
	if err != nil {
		t.Fatalf("List (before): %v", err)
	}
	r := callLogDecision(t, s, map[string]any{
		"title": "x", "context": "c", "decision": "d", "rationale": "r",
		"task_id": "abcdef0",
	})
	if !r.IsError || resultText(r) != errMsgInvalidTaskIDUUID {
		t.Errorf("got %q, want %q", resultText(r), errMsgInvalidTaskIDUUID)
	}
	after, err := s.decision.List(context.Background(), decision.ListParams{Limit: 100})
	if err != nil {
		t.Fatalf("List (after): %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("decision count changed from %d to %d — a rejected task_id must not write a decision", len(before), len(after))
	}
}

func logDecisionPrefixCaseNotFound(t *testing.T) {
	s := newTestWorkSessionServer(t)
	before, err := s.decision.List(context.Background(), decision.ListParams{Limit: 100})
	if err != nil {
		t.Fatalf("List (before): %v", err)
	}
	r := callLogDecision(t, s, map[string]any{
		"title": "x", "context": "c", "decision": "d", "rationale": "r",
		"task_id": "abcdef01",
	})
	if !r.IsError || resultText(r) != errMsgTaskNotFound {
		t.Errorf("got %q, want %q", resultText(r), errMsgTaskNotFound)
	}
	after, err := s.decision.List(context.Background(), decision.ListParams{Limit: 100})
	if err != nil {
		t.Fatalf("List (after): %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("decision count changed from %d to %d — a not-found task_id must not write a decision", len(before), len(after))
	}
}

func logDecisionPrefixCaseOmitted(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callLogDecision(t, s, map[string]any{
		"title": "no task", "context": "c", "decision": "d", "rationale": "r",
	})
	if r.IsError {
		t.Fatalf("omitted task_id should still succeed, got: %s", resultText(r))
	}
}

// TestRecordOutcome_TaskIDPrefix_OnlyForTaskEntity covers F0930-19's second
// manual call site — the entity_type gate is the load-bearing property here
// (Risk flags' named danger: resolving unconditionally would misroute
// sprint/decision/project entity_id values through a task-only query).
func TestRecordOutcome_TaskIDPrefix_OnlyForTaskEntity(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	t.Run("positive: entity_type=task resolves prefix, outcome recorded against full UUID", func(t *testing.T) {
		t.Parallel()
		id := seedTask(t, s)
		r := callRecordOutcome(t, s, map[string]any{
			"entity_type": entityTypeTask,
			"entity_id":   prefixOf(id),
			"result":      "success",
		})
		if r.IsError {
			t.Fatalf("prefix call should succeed, got: %s", resultText(r))
		}
		got, err := s.outcome.GetLatestForEntity(context.Background(), s.workspaceUUID(), "task", id)
		if err != nil {
			t.Fatalf("GetLatestForEntity: %v", err)
		}
		if got.EntityID != id {
			t.Errorf("EntityID = %s, want resolved full UUID %s", got.EntityID, id)
		}
	})

	t.Run("negative: entity_type=decision leaves an 8-hex-looking entity_id unresolved", func(t *testing.T) {
		t.Parallel()
		r := callRecordOutcome(t, s, map[string]any{
			"entity_type": "decision",
			"entity_id":   "deadbeef",
			"result":      "success",
		})
		if !r.IsError || resultText(r) != "invalid entity_id UUID" {
			t.Errorf("got %q, want %q — an entity_type other than task must never reach the task resolver", resultText(r), "invalid entity_id UUID")
		}
	})
}

// TestSetSessionHandoff_RefTaskIDPrefix covers F0930-19's third manual call
// site: per-element resolution inside next_actions, with the index-prefixed
// error convention parseAndValidateNextActions already uses for every other
// per-element validation failure.
func TestSetSessionHandoff_RefTaskIDPrefix(t *testing.T) {
	t.Parallel()

	t.Run("positive: unique prefix resolves to the full UUID in the stored handoff", func(t *testing.T) {
		t.Parallel()
		s := newTestWorkSessionServer(t)
		id := seedTask(t, s)
		raw, err := json.Marshal([]session.NextAction{
			{Step: 1, Title: "step one", RefTaskID: strPtr(prefixOf(id))},
		})
		if err != nil {
			t.Fatalf("marshal next_actions: %v", err)
		}
		r := callSetSessionHandoff(t, s, map[string]any{
			"intent": "continue work", "next_actions": string(raw),
		})
		if r.IsError {
			t.Fatalf("prefix call should succeed, got: %s", resultText(r))
		}
		var view struct {
			NextActions []session.NextAction `json:"next_actions"`
		}
		if err := json.Unmarshal([]byte(resultText(r)), &view); err != nil {
			t.Fatalf("unmarshal response: %v\nraw=%s", err, resultText(r))
		}
		if len(view.NextActions) != 1 || view.NextActions[0].RefTaskID == nil {
			t.Fatalf("expected 1 next_action with a resolved ref_task_id, got: %+v", view.NextActions)
		}
		if *view.NextActions[0].RefTaskID != id.String() {
			t.Errorf("ref_task_id = %q, want resolved full UUID %q", *view.NextActions[0].RefTaskID, id.String())
		}
	})

	t.Run("negative: index 1's ambiguous prefix fails with a next_actions[1]: prefix, index 0 unaffected", func(t *testing.T) {
		t.Parallel()
		s := newTestWorkSessionServer(t)
		idA, idB := uuid.New(), uuid.New()
		s.gtd = fakeByPrefixGTDStore{
			responses: map[string][]gtd.TaskIDTitle{
				"aaaaaaaa": {{ID: idA, Title: "task A"}},
				"bbbbbbbb": {{ID: idA, Title: "task A"}, {ID: idB, Title: "task B"}},
			},
		}
		raw, err := json.Marshal([]session.NextAction{
			{Step: 1, Title: "step one", RefTaskID: strPtr("aaaaaaaa")},
			{Step: 2, Title: "step two", RefTaskID: strPtr("bbbbbbbb")},
		})
		if err != nil {
			t.Fatalf("marshal next_actions: %v", err)
		}
		r := callSetSessionHandoff(t, s, map[string]any{
			"intent": "continue work", "next_actions": string(raw),
		})
		if !r.IsError {
			t.Fatalf("ambiguous ref_task_id at index 1 must error, got success: %s", resultText(r))
		}
		if !strings.HasPrefix(resultText(r), "next_actions[1]: ") {
			t.Errorf("error should be prefixed next_actions[1]:, got: %s", resultText(r))
		}
		if !strings.Contains(resultText(r), "ambiguous") {
			t.Errorf("error should mention ambiguity, got: %s", resultText(r))
		}
	})
}
