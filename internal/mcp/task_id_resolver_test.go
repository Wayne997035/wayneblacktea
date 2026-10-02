package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// prefixOf returns id's first 8 hex chars — the shortest prefix D3 accepts.
func prefixOf(id uuid.UUID) string {
	return strings.ReplaceAll(id.String(), "-", "")[:8]
}

// prefixOfN returns id's first n raw hex chars (no dashes), n <= 32 — used
// for SEC-197-01's boundary cases where the exact prefix length (crossing a
// dash position or not) is the thing under test, not just "any valid prefix"
// the way prefixOf's fixed 8 chars is.
func prefixOfN(id uuid.UUID, n int) string {
	return strings.ReplaceAll(id.String(), "-", "")[:n]
}

// TestCanonicalTaskIDPrefix is SEC-197-01's unit-level acceptance: every hex
// length canonicalTaskIDPrefix dash-inserts differently must land the dash
// at the correct standard-UUID-text position. The 9/13/17/21 cases are "one
// char past a dash boundary" (dash must appear immediately before the last
// char); the 8/12/16/20 cases are "exactly at a dash boundary" (no dash yet
// — the boundary hasn't been crossed); 31 is the regex's own upper bound.
func TestCanonicalTaskIDPrefix(t *testing.T) {
	t.Parallel()
	const hexSrc = "0123456789abcdef0123456789abcde" // 31 raw hex chars
	cases := []struct {
		n    int
		want string
	}{
		{8, "01234567"},
		{9, "01234567-8"},
		{12, "01234567-89ab"},
		{13, "01234567-89ab-c"},
		{16, "01234567-89ab-cdef"},
		{17, "01234567-89ab-cdef-0"},
		{20, "01234567-89ab-cdef-0123"},
		{21, "01234567-89ab-cdef-0123-4"},
		{31, "01234567-89ab-cdef-0123-456789abcde"},
	}
	for _, tc := range cases {
		got := canonicalTaskIDPrefix(hexSrc[:tc.n])
		if got != tc.want {
			t.Errorf("canonicalTaskIDPrefix(%d chars) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// fakeAmbiguousGTDStore embeds noopGTDStore (tools_contextpack_fakes_test.go,
// same package) so every gtd.StoreIface method it doesn't override is a
// harmless no-op, and overrides only FindTaskIDsByPrefix to deterministically
// return candidates regardless of the query — used to test the >=2-candidate
// ambiguous path without needing a real 8-hex-char UUID collision (~1 in 4
// billion odds against seeding it naturally).
type fakeAmbiguousGTDStore struct {
	noopGTDStore
	candidates []gtd.TaskIDTitle
}

func (f fakeAmbiguousGTDStore) FindTaskIDsByPrefix(context.Context, string, int) ([]gtd.TaskIDTitle, error) {
	return f.candidates, nil
}

// TestSeamTaskIDPrefix_ResolvesAndRewrites is F0930-18's positive control:
// each of the 9 seam-wrapped tools D3 enumerates resolves an 8+-char
// task_id prefix to the same result a full UUID call would produce.
func TestSeamTaskIDPrefix_ResolvesAndRewrites(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	// Each case is its own top-level function (seamPrefixCase*) rather than
	// a closure literal here, so gocyclo scores it separately from this
	// dispatcher function (same pattern as TestResolveTaskIDPrefix_PG/
	// _SQLite above and TestTaskArea_ReturnedByEveryReadPathPG elsewhere in
	// this repo).
	cases := []struct {
		name string
		run  func(t *testing.T, s *Server)
	}{
		{"get_task", seamPrefixCaseGetTask},
		{"update_task", seamPrefixCaseUpdateTask},
		{"complete_task", seamPrefixCaseCompleteTask},
		{"delete_task", seamPrefixCaseDeleteTask},
		{"task_checklist_add_item", seamPrefixCaseChecklistAddItem},
		{"task_checklist_toggle", seamPrefixCaseChecklistToggle},
		{"task_checklist_complete", seamPrefixCaseChecklistComplete},
		{"set_task_status", seamPrefixCaseSetTaskStatus},
		{"begin_task", seamPrefixCaseBeginTask},
	}
	for _, tc := range cases {
		run := tc.run
		t.Run(tc.name, func(t *testing.T) { t.Parallel(); run(t, s) })
	}
}

func seamPrefixCaseGetTask(t *testing.T, s *Server) {
	id := seedTask(t, s)
	r := callGetTask(t, s, map[string]any{"task_id": prefixOf(id)})
	if r.IsError {
		t.Fatalf("prefix call should succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), id.String()) {
		t.Errorf("response should contain resolved full UUID %s, got: %s", id, resultText(r))
	}
}

func seamPrefixCaseUpdateTask(t *testing.T, s *Server) {
	id := seedTask(t, s)
	r := callUpdateTask(t, s, map[string]any{"task_id": prefixOf(id), "title": "updated via prefix"})
	if r.IsError {
		t.Fatalf("prefix call should succeed, got: %s", resultText(r))
	}
	task, err := s.gtd.GetTaskByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if task.Title != "updated via prefix" {
		t.Errorf("title = %q, want %q — prefix call did not resolve to the seeded task", task.Title, "updated via prefix")
	}
}

func seamPrefixCaseCompleteTask(t *testing.T, s *Server) {
	id := seedTask(t, s)
	r := callCompleteTask(t, s, map[string]any{"task_id": prefixOf(id)})
	if r.IsError {
		t.Fatalf("prefix call should succeed, got: %s", resultText(r))
	}
	task, err := s.gtd.GetTaskByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if task.Status != taskStatusCompleted {
		t.Errorf("status = %q, want completed", task.Status)
	}
}

func seamPrefixCaseDeleteTask(t *testing.T, s *Server) {
	id := seedTask(t, s)
	// Step 1 only — the two-step token flow itself is covered by
	// TestDeleteTask_PrefixKeepsTwoStepToken below. This case only proves
	// the prefix resolves to the seeded task at all.
	r := callDeleteTask(t, s, map[string]any{"task_id": prefixOf(id)})
	token := extractToken(t, r)
	if token == "" {
		t.Fatal("expected a non-empty deletion_token")
	}
}

func seamPrefixCaseChecklistAddItem(t *testing.T, s *Server) {
	id := seedTask(t, s)
	r := callChecklistAddItem(t, s, map[string]any{"task_id": prefixOf(id), "title": "step one"})
	if r.IsError {
		t.Fatalf("prefix call should succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "step one") {
		t.Errorf("response should contain the new item, got: %s", resultText(r))
	}
}

func seamPrefixCaseChecklistToggle(t *testing.T, s *Server) {
	id := seedTask(t, s)
	itemID := seedChecklistItem(t, s, id)
	r := callChecklistToggle(t, s, map[string]any{
		"task_id": prefixOf(id), "item_id": itemID.String(), "done": true,
	})
	if r.IsError {
		t.Fatalf("prefix call should succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), `"done":true`) {
		t.Errorf("item should be marked done, got: %s", resultText(r))
	}
}

func seamPrefixCaseChecklistComplete(t *testing.T, s *Server) {
	id := seedTask(t, s)
	itemID := seedChecklistItem(t, s, id)
	r := callChecklistComplete(t, s, map[string]any{"task_id": prefixOf(id), "item_id": itemID.String()})
	if r.IsError {
		t.Fatalf("prefix call should succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), `"done":true`) {
		t.Errorf("item should be marked done, got: %s", resultText(r))
	}
}

func seamPrefixCaseSetTaskStatus(t *testing.T, s *Server) {
	id := seedTaskWithDueDate(t, s, "")
	r := callSetTaskStatus(t, s, map[string]any{"task_id": prefixOf(id), "status": statusInProgress})
	if r.IsError {
		t.Fatalf("prefix call should succeed, got: %s", resultText(r))
	}
	task, err := s.gtd.GetTaskByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if task.Status != statusInProgress {
		t.Errorf("status = %q, want %s", task.Status, statusInProgress)
	}
}

func seamPrefixCaseBeginTask(t *testing.T, s *Server) {
	id := seedTask(t, s)
	r := callBeginTask(t, s, map[string]any{"task_id": prefixOf(id), "assignee": "claude"})
	if r.IsError {
		t.Fatalf("prefix call should succeed, got: %s", resultText(r))
	}
	task, err := s.gtd.GetTaskByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if task.Status != statusInProgress {
		t.Errorf("status = %q, want %s", task.Status, statusInProgress)
	}
}

// TestBeginTask_MalformedTaskIDMessageUnchanged is begin_task's own
// regression guard for a case the acceptance criteria require but no
// pre-existing test covered: begin_task's task_id is deliberately NOT
// uuidArgs-marked (tools_gtd.go's registration comment), so pass-0
// (resolveTaskIDPrefix) no-ops on a malformed value and handleBeginTask's own
// inline uuid.Parse still produces the exact same "invalid task_id: <err>"
// message it always has — same message SHAPE as before D3/D14 existed, not a
// new "too short"/prefix-flavoured message.
func TestBeginTask_MalformedTaskIDMessageUnchanged(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	r := callBeginTask(t, s, map[string]any{"task_id": "not-a-uuid", "assignee": "claude"})
	if !r.IsError || !strings.HasPrefix(resultText(r), "invalid task_id: ") {
		t.Errorf("got %q, want a message starting with %q", resultText(r), "invalid task_id: ")
	}
}

// TestSeamTaskIDPrefix_InvalidAndAmbiguous is F0930-18's negative control:
// 7-char, non-hex, and ambiguous (>=2 candidates) task_id values each error
// the same way across the seam, exercised through get_task as a
// representative seam tool (the pass-0 logic under test lives in
// toolSpec.validate, shared by all 9 tools — not in get_task's own handler).
func TestSeamTaskIDPrefix_InvalidAndAmbiguous(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	t.Run("7 char prefix stays invalid UUID, no prefix query attempted", func(t *testing.T) {
		t.Parallel()
		r := callGetTask(t, s, map[string]any{"task_id": "abcdef0"})
		if !r.IsError || resultText(r) != errMsgInvalidTaskIDUUID {
			t.Errorf("got %q, want %q", resultText(r), errMsgInvalidTaskIDUUID)
		}
	})

	t.Run("8 char non-hex stays invalid UUID, no prefix query attempted", func(t *testing.T) {
		t.Parallel()
		r := callGetTask(t, s, map[string]any{"task_id": "1234567g"})
		if !r.IsError || resultText(r) != errMsgInvalidTaskIDUUID {
			t.Errorf("got %q, want %q", resultText(r), errMsgInvalidTaskIDUUID)
		}
	})

	t.Run("8 hex prefix matching zero tasks is not found", func(t *testing.T) {
		t.Parallel()
		r := callGetTask(t, s, map[string]any{"task_id": "abcdef01"})
		if !r.IsError || resultText(r) != errMsgTaskNotFound {
			t.Errorf("got %q, want %q", resultText(r), errMsgTaskNotFound)
		}
	})

	t.Run("ambiguous prefix lists candidates and writes nothing", func(t *testing.T) {
		t.Parallel()
		// A real 8-hex-char UUID collision is a ~1-in-4-billion event, so
		// this swaps in a fake gtd.StoreIface (localS.gtd is reassignable —
		// same package as *Server) that deterministically returns 2
		// candidates for ANY prefix query, rather than trying to seed a real
		// collision. resolveTaskIDPrefix reads ts.server.gtd at call time
		// (a pointer field, not a snapshot), so this swap is visible to the
		// very next callGetTask below.
		localS := newTestWorkSessionServer(t)
		idA, idB := uuid.New(), uuid.New()
		localS.gtd = fakeAmbiguousGTDStore{
			candidates: []gtd.TaskIDTitle{{ID: idA, Title: "task A"}, {ID: idB, Title: "task B"}},
		}

		r := callGetTask(t, localS, map[string]any{"task_id": "deadbeef"})
		if !r.IsError {
			t.Fatalf("ambiguous prefix must error, got success: %s", resultText(r))
		}
		if !strings.Contains(resultText(r), "ambiguous") {
			t.Errorf("error should mention ambiguity, got: %s", resultText(r))
		}
		for _, id := range []uuid.UUID{idA, idB} {
			if !strings.Contains(resultText(r), id.String()) {
				t.Errorf("ambiguous error should list candidate %s, got: %s", id, resultText(r))
			}
		}
	})

	t.Run("prefix scoped to a different workspace is not found", func(t *testing.T) {
		t.Parallel()
		// A genuinely separate SQLite file/workspace, not merely a different
		// wsID over the same store — s (this subtest's outer Server) is
		// scoped "" (newTestWorkSessionServer's default). The predicate
		// itself (workspace_id filter on a SHARED table) is what
		// TestResolveTaskIDPrefix's mutation test in the store package
		// proves; this MCP-level check only proves the resolver plumbs
		// workspace scoping through end-to-end.
		dbPath := newMigratedSQLitePath(t, "task-id-prefix-other-ws.db")
		otherDB, err := wbtsqlite.Open(context.Background(), dbPath, "22222222-2222-4222-8222-222222222222")
		if err != nil {
			t.Fatalf("wbtsqlite.Open: %v", err)
		}
		t.Cleanup(func() { _ = otherDB.Close() })
		otherGTD := wbtsqlite.NewGTDStore(otherDB)
		otherTask, err := otherGTD.CreateTask(context.Background(), gtd.CreateTaskParams{Title: "other-workspace task"})
		if err != nil {
			t.Fatalf("CreateTask (other workspace): %v", err)
		}

		r := callGetTask(t, s, map[string]any{"task_id": prefixOf(otherTask.ID)})
		if !r.IsError || resultText(r) != errMsgTaskNotFound {
			t.Errorf("cross-workspace prefix should be not-found, got %q", resultText(r))
		}
	})
}

// TestDeleteTask_PrefixKeepsTwoStepToken verifies delete_task's 2-step
// confirmation flow is unaffected by prefix resolution: both calls supply
// the SAME prefix, each independently resolves to the same full UUID, and
// the token issued by step 1 is accepted by step 2 (Constraints, D3 spec:
// the token key is taskDeletionKey(id.String()) using the post-resolution
// uuid.UUID).
func TestDeleteTask_PrefixKeepsTwoStepToken(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	prefix := prefixOf(id)

	r1 := callDeleteTask(t, s, map[string]any{"task_id": prefix})
	token := extractToken(t, r1)

	r2 := callDeleteTask(t, s, map[string]any{"task_id": prefix, "confirm": true, "deletion_token": token})
	if r2.IsError {
		t.Fatalf("step 2 with the same prefix + token should succeed, got: %s", resultText(r2))
	}

	_, err := s.gtd.GetTaskByID(context.Background(), id)
	if err == nil {
		t.Error("task should be deleted after step 2")
	}
}

// TestSeamTaskIDPrefix_NilServerFailsCleanly is F0930-18's panic-safety
// regression guard: toolspec_test.go's ~19 pre-existing call sites (and any
// direct ts.validate/seam call with a nil *Server) never had a *Server to
// resolve a task_id prefix against. An 8+-char, non-UUID task_id through one
// of those specs must fail cleanly (the same invalid-UUID message a
// malformed value already produced) instead of dereferencing the nil
// *Server.
func TestSeamTaskIDPrefix_NilServerFailsCleanly(t *testing.T) {
	t.Parallel()
	tool := mcpmsg.NewTool("spec_nil_server_task_id", mcpmsg.WithString("task_id", mcpmsg.Required()))
	ts := registerToolSpec(tool, uuidArgs("task_id"))

	r := ts.validate(context.Background(), nil, map[string]any{"task_id": "deadbeef"})
	if r == nil || !r.IsError {
		t.Fatal("expected a clean error result, not a panic and not success")
	}
	if resultText(r) != errMsgInvalidTaskIDUUID {
		t.Errorf("message = %q, want %q", resultText(r), errMsgInvalidTaskIDUUID)
	}
}

// TestSeamTaskIDPrefix_MutationVisibleToDownstreamMiddleware is the
// dispatch's required assumption check for the seam path (F0930-18 half of
// two, see TestLogDecision_TaskIDPrefix for the manual-call-site half):
// resolveTaskIDPrefix's success path MUST mutate the SAME map instance
// req.GetArguments() returns, not a shallow copy — otherwise a downstream
// middleware reading req.GetArguments() on the same request would still see
// the raw prefix. mcp-go v0.49.0's GetArguments (mcp/tools.go) type-asserts
// r.Params.Arguments into map[string]any and returns that exact reference,
// so asserting the map we passed in now holds the resolved UUID is
// sufficient to prove the property this test is named for.
func TestSeamTaskIDPrefix_MutationVisibleToDownstreamMiddleware(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	args := map[string]any{"task_id": prefixOf(id)}
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args

	res, err := seam(s, "get_task", s.handleGetTask)(context.Background(), req)
	if err != nil {
		t.Fatalf("get_task error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got: %s", resultText(res))
	}
	if got, _ := args["task_id"].(string); got != id.String() {
		t.Errorf("args[task_id] after seam validation = %q, want resolved full UUID %q — "+
			"a downstream middleware reading req.GetArguments() on this request would still see the raw prefix",
			got, id.String())
	}
}

// TestSeamTaskIDPrefix_NineAndThirteenCharPrefix is SEC-197-01's seam-layer
// acceptance: a 9-char prefix crosses the first dash position (UUID text's
// 9th char is always '-') and a 13-char prefix crosses the second — both are
// exactly the lengths that produced a guaranteed-wrong, un-dashed LIKE
// pattern before canonicalTaskIDPrefix existed. Checks both resolution
// (get_task succeeds) and the args-mutation contract (same assertion style
// as TestSeamTaskIDPrefix_MutationVisibleToDownstreamMiddleware above).
func TestSeamTaskIDPrefix_NineAndThirteenCharPrefix(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		n    int
	}{
		{"9-char prefix (crosses first dash)", 9},
		{"13-char prefix (crosses second dash)", 13},
	}
	for _, tc := range cases {
		n := tc.n
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newTestWorkSessionServer(t)
			id := seedTask(t, s)

			args := map[string]any{"task_id": prefixOfN(id, n)}
			req := mcpmsg.CallToolRequest{}
			req.Params.Arguments = args

			res, err := seam(s, "get_task", s.handleGetTask)(context.Background(), req)
			if err != nil {
				t.Fatalf("get_task error: %v", err)
			}
			if res.IsError {
				t.Fatalf("%d-char prefix call should succeed, got: %s", n, resultText(res))
			}
			if got, _ := args["task_id"].(string); got != id.String() {
				t.Errorf("args[task_id] after seam validation = %q, want resolved full UUID %q", got, id.String())
			}
		})
	}
}

// TestSeamTaskIDPrefix_LengthBoundaries is SEC-197-01's upper-bound
// acceptance: 31 chars is taskIDPrefixRe's own max (still a prefix lookup);
// 32 chars is a complete dashless UUID literal, caught by uuid.Parse before
// the prefix regex ever runs; 33 chars matches neither form and must be
// rejected the same way any other malformed task_id is.
func TestSeamTaskIDPrefix_LengthBoundaries(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	t.Run("31-char prefix resolves via FindTaskIDsByPrefix", func(t *testing.T) {
		t.Parallel()
		id := seedTask(t, s)
		r := callGetTask(t, s, map[string]any{"task_id": prefixOfN(id, 31)})
		if r.IsError {
			t.Fatalf("31-char prefix call should succeed, got: %s", resultText(r))
		}
		if !strings.Contains(resultText(r), id.String()) {
			t.Errorf("response should contain resolved full UUID %s, got: %s", id, resultText(r))
		}
	})

	t.Run("32-char dashless hex parses directly as a full UUID, no prefix query", func(t *testing.T) {
		t.Parallel()
		id := seedTask(t, s)
		r := callGetTask(t, s, map[string]any{"task_id": prefixOfN(id, 32)})
		if r.IsError {
			t.Fatalf("32-char dashless UUID call should succeed, got: %s", resultText(r))
		}
		if !strings.Contains(resultText(r), id.String()) {
			t.Errorf("response should contain resolved full UUID %s, got: %s", id, resultText(r))
		}
	})

	t.Run("33-char hex is invalid, matches neither a full UUID nor a bounded prefix", func(t *testing.T) {
		t.Parallel()
		id := seedTask(t, s)
		tooLong := prefixOfN(id, 32) + "0"
		r := callGetTask(t, s, map[string]any{"task_id": tooLong})
		if !r.IsError || resultText(r) != errMsgInvalidTaskIDUUID {
			t.Errorf("got %q, want %q", resultText(r), errMsgInvalidTaskIDUUID)
		}
	})
}
