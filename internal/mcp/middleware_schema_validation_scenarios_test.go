package mcp

// User-scenario tests for the generic schema-validation middleware (MCP-6,
// GTD:822f9ae7), stage 3. The stage-1 tests mostly guard "a bad call is
// blocked"; these guard "a legitimate call is NOT blocked" and the things a
// real caller does (whitespace titles, wrong shapes, task_id prefixes, nulls,
// concurrency). Expected values come from the dispatch record, never from
// printing what the code returns.
//
// All helper functions of the stage-1 test file are reused, not copied.
//
// No test here calls t.Parallel: they share the process-global
// schemaValidationExempt / toolSpecRegistry state with the stage-1 tests.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/Wayne997035/wayneblacktea/internal/vision"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	scenVariantMin   = "min"
	scenVariantMaxA  = "maxA"
	scenVariantMaxB  = "maxB"
	scenVariantNulls = "nulls"

	scenTypeArray = "array"

	scenToolAddProcedural = "add_procedural"
	scenToolListTasks     = "list_tasks"
	scenToolAddTask       = "add_task"
	scenToolLogDecision   = "log_decision"
)

// Invisible characters are built with string(rune(...)) on purpose: the
// editing tooling turns backslash-u escapes into the literal character.
var (
	scenU3000 = string(rune(0x3000)) // ideographic space (White_Space)
	scenU00A0 = string(rune(0x00A0)) // no-break space (White_Space)
	scenU2003 = string(rune(0x2003)) // em space (White_Space)
	scenU200B = string(rune(0x200B)) // zero-width space (NOT White_Space)
	scenUFEFF = string(rune(0xFEFF)) // BOM / zero-width no-break space (NOT White_Space)
)

// scenToolNames returns the sorted names of tools.
func scenToolNames(tools map[string]*server.ServerTool) []string {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// scenIsRequired reports whether name is in the tool's required list.
func scenIsRequired(tool mcpmsg.Tool, name string) bool {
	for _, r := range tool.InputSchema.Required {
		if r == name {
			return true
		}
	}
	return false
}

// scenFieldNames returns every declared property name of the tool, sorted.
func scenFieldNames(tool mcpmsg.Tool) []string {
	names := make([]string, 0, len(tool.InputSchema.Properties))
	for name := range tool.InputSchema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// scenHasOptional reports whether the tool has at least one optional field.
func scenHasOptional(tool mcpmsg.Tool) bool {
	for _, name := range scenFieldNames(tool) {
		if !scenIsRequired(tool, name) {
			return true
		}
	}
	return false
}

// scenValue is a valid value for one field under a value variant (min, maxA
// or maxB).
func scenValue(tool mcpmsg.Tool, name, variant string) any {
	switch schemaValPropType(tool, name) {
	case argTypeNumber:
		if variant == scenVariantMaxA {
			return 1.5
		}
		return float64(0)
	case argTypeBoolean:
		return variant == scenVariantMaxA
	case scenTypeArray:
		if variant == scenVariantMaxA {
			return []any{"x"}
		}
		return []any{}
	default:
		if e := schemaValFirstEnum(tool, name); e != "" {
			return e
		}
		if strings.HasSuffix(name, "_id") {
			return schemaValUUID
		}
		return "測試 x"
	}
}

// scenArgs builds the arguments for one variant: min = required only; maxA and
// maxB = every field; nulls = required as min, every optional field JSON null.
func scenArgs(tool mcpmsg.Tool, variant string) map[string]any {
	args := map[string]any{}
	for _, name := range scenFieldNames(tool) {
		switch {
		case scenIsRequired(tool, name):
			if variant == scenVariantMaxA || variant == scenVariantMaxB {
				args[name] = scenValue(tool, name, variant)
			} else {
				args[name] = scenValue(tool, name, scenVariantMin)
			}
		case variant == scenVariantMaxA || variant == scenVariantMaxB:
			args[name] = scenValue(tool, name, variant)
		case variant == scenVariantNulls:
			args[name] = nil
		}
	}
	return args
}

// scenJSON marshals a Go map to the JSON text handed to tools/call.
func scenJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %v: %v", v, err)
	}
	return string(raw)
}

// scenRawCall sends one tools/call JSON-RPC message and decodes isError and the
// first text block. It never touches *testing.T, so goroutines may call it.
func scenRawCall(ms *server.MCPServer, rpc string) (isErr bool, text string, err error) {
	resp := ms.HandleMessage(context.Background(), json.RawMessage(rpc))
	raw, err := json.Marshal(resp)
	if err != nil {
		return false, "", fmt.Errorf("marshal response: %w", err)
	}
	var decoded struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false, "", fmt.Errorf("decode response: %w", err)
	}
	if decoded.Error != nil {
		return false, "", fmt.Errorf("JSON-RPC error: %s", decoded.Error.Message)
	}
	if len(decoded.Result.Content) == 0 {
		return false, "", fmt.Errorf("no content in response %s", raw)
	}
	return decoded.Result.IsError, decoded.Result.Content[0].Text, nil
}

// scenRPC builds a tools/call message with the given raw arguments JSON.
func scenRPC(name, argsJSON string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, name, argsJSON)
}

// TestSchemaValidation_ValidCallsPassEveryTool [F1127-01]: every registered tool
// accepts a legal call in three shapes (required only; every field with
// number 1.5 / true / ["x"]; every field with 0 / false / []).
// [F1127-01]
func TestSchemaValidation_ValidCallsPassEveryTool(t *testing.T) {
	_, ms := newTestMCPServer(t)
	tools := ms.ListTools()
	names := scenToolNames(tools)
	stubs := schemaValStubs(t, ms, names...)

	rows := 0
	for _, name := range names {
		for _, variant := range []string{scenVariantMin, scenVariantMaxA, scenVariantMaxB} {
			rows++
			args := scenJSON(t, scenArgs(tools[name].Tool, variant))
			schemaValWantReaches(t, ms, stubs[name], name, args)
		}
	}
	if len(names) != 94 || rows != 282 {
		t.Errorf("swept %d tools / %d rows, want 94 / 282 (a new tool or field must be checked here before the constant moves)",
			len(names), rows)
	}
}

// TestSchemaValidation_OptionalNullSkipped [F1127-05]: an optional field sent as
// JSON null is not a type error at the middleware layer. NOTE: the seam's own
// handlers still reject an optional null (master behaviour, tracked by
// GTD:6b7db3ae); this test only covers the middleware layer, with stub handlers.
// [F1127-05]
func TestSchemaValidation_OptionalNullSkipped(t *testing.T) {
	_, ms := newTestMCPServer(t)
	tools := ms.ListTools()
	names := scenToolNames(tools)
	stubs := schemaValStubs(t, ms, names...)

	reached := 0
	for _, name := range names {
		if !scenHasOptional(tools[name].Tool) {
			continue
		}
		reached++
		schemaValWantReaches(t, ms, stubs[name], name, scenJSON(t, scenArgs(tools[name].Tool, scenVariantNulls)))
	}
	if reached != 65 {
		t.Errorf("tools with an optional field = %d, want 65", reached)
	}
}

// TestSchemaValidation_ZeroFalseEmptyAreNotBlank [F1127-04]: the blank rule is
// about strings only; a required number 0, boolean false or empty array is a
// real value.
// [F1127-04]
func TestSchemaValidation_ZeroFalseEmptyAreNotBlank(t *testing.T) {
	_, ms := newTestMCPServer(t)
	rows := []struct {
		tool  string
		field string
		value any
	}{
		{"mark_next_action_done", "step", float64(0)},
		{"submit_review", "rating", float64(0)},
		{"task_checklist_toggle", "done", false},
		{"update_skill_from_outcome", "success", false},
		{"confirm_proposals", "ids", []any{}},
	}
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.tool)
	}
	stubs := schemaValStubs(t, ms, names...)
	for _, r := range rows {
		args := scenArgs(ms.GetTool(r.tool).Tool, scenVariantMin)
		args[r.field] = r.value
		schemaValWantReaches(t, ms, stubs[r.tool], r.tool, scenJSON(t, args))
	}
}

// TestSchemaValidation_UnicodeWhitespaceRequiredRejected [F1127-04]: a required
// string made only of Unicode White_Space (ideographic space, no-break space,
// em space, tab / newline, CR LF, a mix) is blank and rejected with the
// "empty:" wording; zero-width space and BOM alone are NOT White_Space and pass
// (Lead boundary, follows strings.TrimSpace like the seam's blankFor); real
// text padded with spaces passes.
// [F1127-04]
func TestSchemaValidation_UnicodeWhitespaceRequiredRejected(t *testing.T) {
	_, ms := newTestMCPServer(t)
	st := schemaValStubs(t, ms, scenToolAddProcedural, scenToolAddTask)
	const wantProc = `title is required [received keys: "title", "when_to_use"; missing: none; empty: "title"]`
	const wantTask = `title is required [received keys: "area", "title"; missing: none; empty: "title"]`

	procArgs := func(title string) string {
		return scenJSON(t, map[string]any{"title": title, "when_to_use": "w"})
	}
	blocked := []struct{ label, title string }{
		{"U+3000", scenU3000},
		{"U+00A0", scenU00A0},
		{"U+2003", scenU2003},
		{"tab-newline", "\t\n"},
		{"cr-lf", "\r\n"},
		{"mixed", " " + scenU3000 + scenU00A0},
	}
	for _, c := range blocked {
		t.Run("blocked/"+c.label, func(t *testing.T) {
			schemaValWantBlocked(t, ms, st[scenToolAddProcedural], scenToolAddProcedural, procArgs(c.title), wantProc)
		})
	}
	schemaValWantBlocked(t, ms, st[scenToolAddTask], scenToolAddTask,
		scenJSON(t, map[string]any{"title": scenU3000, "area": "x"}), wantTask)

	passes := []struct{ label, title string }{
		{"U+200B", scenU200B},
		{"U+FEFF", scenUFEFF},
		{"padded-cjk", scenU3000 + "任務" + scenU3000},
		{"cjk", "中文標題"},
	}
	for _, c := range passes {
		t.Run("passes/"+c.label, func(t *testing.T) {
			schemaValWantReaches(t, ms, st[scenToolAddProcedural], scenToolAddProcedural, procArgs(c.title))
		})
	}
}

// TestSchemaValidation_NumberEdgeValuesPass [F1127-05]: the middleware checks
// that a number is a number, not its range; zero, negatives, fractions and
// exponent forms all go to the handler, which owns range checks.
// [F1127-05]
func TestSchemaValidation_NumberEdgeValuesPass(t *testing.T) {
	_, ms := newTestMCPServer(t)
	st := schemaValStubs(t, ms, "analyze_recent_patterns", "list_decisions")
	rows := []struct{ tool, args string }{
		{"analyze_recent_patterns", `{"days":0}`},
		{"analyze_recent_patterns", `{"days":-1}`},
		{"analyze_recent_patterns", `{"days":2.5}`},
		{"analyze_recent_patterns", `{"days":1e9}`},
		{"analyze_recent_patterns", `{"days":1E+2}`},
		{"analyze_recent_patterns", `{"days":0.000001}`},
		{"analyze_recent_patterns", `{"days":1e308}`},
		{"list_decisions", `{"limit":0}`},
	}
	for _, r := range rows {
		schemaValWantReaches(t, ms, st[r.tool], r.tool, r.args)
	}
}

// TestSchemaValidation_TaskIDPrefixReachesSeam [F1127-07]: an 8-character task_id
// prefix and an upper-case UUID are legitimate task ids; the middleware must not
// check UUID format or case (the seam resolves prefixes).
// [F1127-07]
func TestSchemaValidation_TaskIDPrefixReachesSeam(t *testing.T) {
	_, ms := newTestMCPServer(t)
	st := schemaValStubs(t, ms, "complete_task", "get_task", "begin_task", "task_checklist_toggle")
	rows := []struct {
		tool string
		args map[string]any
	}{
		{"complete_task", map[string]any{"task_id": "3f2b8c1e"}},
		{"get_task", map[string]any{"task_id": "3f2b8c1e"}},
		{"begin_task", map[string]any{"task_id": "3F2B8C1E"}},
		{"complete_task", map[string]any{"task_id": "3F2B8C1E-9A4D-4E6F-8B7A-1C2D3E4F5A6B"}},
		{"task_checklist_toggle", map[string]any{"task_id": "3f2b8c1e", "item_id": schemaValUUID, "done": false}},
	}
	for _, r := range rows {
		schemaValWantReaches(t, ms, st[r.tool], r.tool, scenJSON(t, r.args))
	}
}

// scenWantAbsentArgs calls name with no "arguments" member at all (the standard
// helper always sends one). wantText == "" means the call must reach the
// handler; otherwise it must be blocked with exactly wantText.
func scenWantAbsentArgs(t *testing.T, ms *server.MCPServer, n interface{ Load() int32 }, name, wantText string) {
	t.Helper()
	before := n.Load()
	rpc := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `"}}`
	isErr, text, err := scenRawCall(ms, rpc)
	if err != nil {
		t.Fatalf("%s absent arguments: %v", name, err)
	}
	calls := n.Load() - before
	if wantText == "" {
		if isErr || calls != 1 {
			t.Errorf("%s absent arguments: want handler reached once, got isError=%v calls=%d text=%q", name, isErr, calls, text)
		}
		return
	}
	if !isErr || text != wantText || calls != 0 {
		t.Errorf("%s absent arguments: got isError=%v calls=%d text=%q, want blocked with %q", name, isErr, calls, text, wantText)
	}
}

// TestSchemaValidation_ArgumentsShapes [F1127-04]: whatever shape "arguments"
// takes (absent, null, [], string, number, boolean, a double-encoded JSON string),
// a tool with no required field is reached and a tool with required fields is
// rejected as if every required argument were missing.
// [F1127-04]
func TestSchemaValidation_ArgumentsShapes(t *testing.T) {
	_, ms := newTestMCPServer(t)
	st := schemaValStubs(t, ms, "get_today_context", scenToolLogDecision)
	const wantMissing = `title is required [received keys: none; missing: "title", "context", "decision", "rationale"; empty: none]`
	doubleEncoded := scenJSON(t, `{"title":"t","context":"c","decision":"d","rationale":"r"}`)

	shapes := []struct{ label, raw string }{
		{"null", `null`},
		{"array", `[]`},
		{"string", `"x"`},
		{"number", `5`},
		{"boolean", `true`},
		{"double-encoded", doubleEncoded},
	}
	for _, s := range shapes {
		t.Run(s.label, func(t *testing.T) {
			schemaValWantReaches(t, ms, st["get_today_context"], "get_today_context", s.raw)
			schemaValWantBlocked(t, ms, st[scenToolLogDecision], scenToolLogDecision, s.raw, wantMissing)
		})
	}
	scenWantAbsentArgs(t, ms, st["get_today_context"], "get_today_context", "")
	scenWantAbsentArgs(t, ms, st[scenToolLogDecision], scenToolLogDecision, wantMissing)
}

// TestSchemaValidation_EnumVariantsRejected [F1127-06]: an enum is matched
// exactly: no case folding, no trimming, no hyphen-for-underscore, no
// translation, and each tool's own member list is in the message.
// [F1127-06]
func TestSchemaValidation_EnumVariantsRejected(t *testing.T) {
	_, ms := newTestMCPServer(t)
	st := schemaValStubs(t, ms, scenToolListTasks, "set_task_status", "update_project_status", "update_task", "expand_tools")

	const wantListTasks = "status must be one of: active, all, pending, in_progress, completed, cancelled"
	for _, v := range []string{"Pending", "PENDING", " pending", "pending ", "in-progress", "In Progress", "進行中"} {
		schemaValWantBlocked(t, ms, st[scenToolListTasks], scenToolListTasks, scenJSON(t, map[string]any{"status": v}), wantListTasks)
	}
	schemaValWantBlocked(t, ms, st["set_task_status"], "set_task_status",
		scenJSON(t, map[string]any{"task_id": schemaValUUID, "status": "done"}),
		"status must be one of: pending, in_progress, completed, cancelled")
	schemaValWantBlocked(t, ms, st["update_project_status"], "update_project_status",
		scenJSON(t, map[string]any{"project_id": schemaValUUID, "status": "Active"}),
		"status must be one of: active, completed, archived, on_hold")
	schemaValWantBlocked(t, ms, st["update_task"], "update_task",
		scenJSON(t, map[string]any{"task_id": schemaValUUID, "status": "completed"}),
		"status must be one of: pending, in_progress, cancelled")
	schemaValWantBlocked(t, ms, st["expand_tools"], "expand_tools", `{"group":"GTD"}`,
		"group must be one of: onboarding, context, gtd, decision, session, knowledge, knowledge_nav, learning, plan, "+
			"proposal, health, arch, status, work_session, vision, playbook, procedural, reflection, atom, outcome, "+
			"skill, behavior_rule, context_pack, dashboard, reconcile, closeout, watchdog, all, reset")
}

// TestSchemaValidation_WrongShapesRejected [F1127-05]: the shapes an LLM caller
// really produces (an object where a JSON string is expected, a string where an
// array is expected, a number for a boolean, ...) are rejected, never coerced.
// [F1127-05]
func TestSchemaValidation_WrongShapesRejected(t *testing.T) {
	_, ms := newTestMCPServer(t)
	rows := []struct{ tool, args, want string }{
		{"set_session_handoff", `{"intent":"i","next_actions":[{"step":"x"}]}`, "next_actions must be a string"},
		{"confirm_plan", `{"phases":[{"name":"p"}]}`, "phases must be a string"},
		{"add_vision_item", `{"title":"t","why_blocked":"w","depends_on":["a"]}`, "depends_on must be a string"},
		{"upsert_project_arch", `{"slug":"s","file_map":{"a.go":"x"}}`, "file_map must be a string"},
		{"delete_task", `{"task_id":"` + schemaValUUID + `","confirm":1}`, "confirm must be a boolean"},
		{"list_decisions", `{"limit":true}`, "limit must be a number"},
		{"assemble_context", `{"objective":"o","files_touched":"a.go,b.go"}`, "files_touched must be an array"},
		{"confirm_proposals", `{"action":"x","ids":"id1"}`, "ids must be an array"},
		{scenToolListTasks, `{"summary":"false"}`, "summary must be a boolean"},
	}
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.tool)
	}
	st := schemaValStubs(t, ms, names...)
	for _, r := range rows {
		schemaValWantBlocked(t, ms, st[r.tool], r.tool, r.args, r.want)
	}
}

// scenConcurrentStep is one call of the T10 round: reach == true expects the
// stub's success, otherwise the call is blocked with exactly want.
type scenConcurrentStep struct {
	tool, args, want string
	reach            bool
}

// TestSchemaValidation_ConcurrentCallsRaceFree [F1127-07]: the middleware is
// safe when called from many goroutines at once (run it under -race): every
// result is right and the stub counters add up exactly. Goroutines never call a
// helper that can t.Fatal (testing.T.FailNow is only legal on the test's own
// goroutine); they use scenRawCall and t.Errorf.
// [F1127-07]
func TestSchemaValidation_ConcurrentCallsRaceFree(t *testing.T) {
	_, ms := newTestMCPServer(t)
	st := schemaValStubs(t, ms, scenToolListTasks, scenToolAddProcedural, "analyze_recent_patterns")
	const wantEmptyTitle = `title is required [received keys: "title", "when_to_use"; missing: none; empty: "title"]`
	steps := []scenConcurrentStep{
		{scenToolListTasks, `{"status":"pending","limit":5}`, "", true},
		{
			scenToolListTasks, `{"status":"Pending"}`,
			"status must be one of: active, all, pending, in_progress, completed, cancelled", false,
		},
		{scenToolAddProcedural, `{"title":"   ","when_to_use":"w"}`, wantEmptyTitle, false},
		{scenToolAddProcedural, `{"title":"t","when_to_use":"w"}`, "", true},
		{"analyze_recent_patterns", `{"days":"3"}`, "days must be a number", false},
	}

	const goroutines, rounds = 8, 25
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range rounds {
				for _, s := range steps {
					isErr, text, err := scenRawCall(ms, scenRPC(s.tool, s.args))
					switch {
					case err != nil:
						t.Errorf("%s %s: %v", s.tool, s.args, err)
					case s.reach && (isErr || text != "stub-ok"):
						t.Errorf("%s %s: want stub success, got isError=%v text=%q", s.tool, s.args, isErr, text)
					case !s.reach && (!isErr || text != s.want):
						t.Errorf("%s %s: want blocked with %q, got isError=%v text=%q", s.tool, s.args, s.want, isErr, text)
					}
				}
			}
		}()
	}
	wg.Wait()

	want := map[string]int32{scenToolListTasks: 200, scenToolAddProcedural: 200, "analyze_recent_patterns": 0}
	for tool, n := range want {
		if got := st[tool].Load(); got != n {
			t.Errorf("%s stub ran %d times, want %d", tool, got, n)
		}
	}
}

// scenCanary is the sentinel value planted in T11 calls; it must never come
// back in an error text.
const scenCanary = "VALUE-CANARY-9d2"

// scenWantNoEcho asserts a rejected call: isError, handler not run, text does
// not contain the canary and starts with the field name plus a space.
func scenWantNoEcho(t *testing.T, ms *server.MCPServer, n interface{ Load() int32 }, name, field string, args map[string]any) {
	t.Helper()
	argsJSON := scenJSON(t, args)
	before := n.Load()
	isErr, text := unknownArgsRPCResult(t, ms, context.Background(), name, argsJSON)
	if !isErr || n.Load() != before {
		t.Errorf("%s %s: want rejected with handler not run, got isError=%v handlerRan=%v text=%q",
			name, field, isErr, n.Load() != before, text)
	}
	if strings.Contains(text, scenCanary) {
		t.Errorf("%s %s: error text leaks the argument value: %q", name, field, text)
	}
	if !strings.HasPrefix(text, field+" ") {
		t.Errorf("%s %s: text %q does not start with the field name and a space", name, field, text)
	}
}

// TestSchemaValidation_ErrorTextNeverEchoesValues [F1127-05]: for every field of
// every tool, a wrong-typed value (and, for enum fields, a non-member string) is
// rejected with a message that names the field and never repeats the value.
// [F1127-05]
func TestSchemaValidation_ErrorTextNeverEchoesValues(t *testing.T) {
	_, ms := newTestMCPServer(t)
	tools := ms.ListTools()
	names := scenToolNames(tools)
	stubs := schemaValStubs(t, ms, names...)

	typeRows, enumRows := 0, 0
	for _, name := range names {
		tool := tools[name].Tool
		for _, field := range scenFieldNames(tool) {
			args := scenArgs(tool, scenVariantMin)
			if schemaValPropType(tool, field) == argTypeString || schemaValPropType(tool, field) == "" {
				args[field] = []any{scenCanary}
			} else {
				args[field] = scenCanary
			}
			typeRows++
			scenWantNoEcho(t, ms, stubs[name], name, field, args)
		}
	}
	for _, name := range names {
		tool := tools[name].Tool
		for _, field := range scenFieldNames(tool) {
			if schemaValFirstEnum(tool, field) == "" || (field == "assignee" && scenAssigneeNoEnum[name]) {
				continue
			}
			args := scenArgs(tool, scenVariantMin)
			args[field] = scenCanary
			enumRows++
			scenWantNoEcho(t, ms, stubs[name], name, field, args)
		}
	}
	if typeRows != 304 || enumRows != 8 {
		t.Errorf("rows: type=%d enum=%d, want 304 and 8 (a new field or enum must be checked here before the constants move)",
			typeRows, enumRows)
	}
}

// scenAssigneeNoEnum are the three seam tools whose assignee skips the enum
// check on purpose (the handler's NormalizeActor judges it).
var scenAssigneeNoEnum = map[string]bool{scenToolAddTask: true, "begin_task": true, "update_task": true}

// TestSchemaValidation_FirstFailureWins [F1127-04]: with several problems in one
// call the first step wins: required, then types (alphabetical by field), then
// enums.
// [F1127-04]
func TestSchemaValidation_FirstFailureWins(t *testing.T) {
	_, ms := newTestMCPServer(t)
	st := schemaValStubs(t, ms, scenToolLogDecision, scenToolListTasks)

	// A: a missing required field beats a wrong-typed optional one.
	schemaValWantBlocked(t, ms, st[scenToolLogDecision], scenToolLogDecision,
		`{"context":"c","decision":"d","rationale":"r","repo_name":5}`,
		`title is required [received keys: "context", "decision", "rationale", "repo_name"; missing: "title"; empty: none]`)
	// B: a type error beats a bad enum.
	schemaValWantBlocked(t, ms, st[scenToolListTasks], scenToolListTasks,
		`{"status":"bogus","limit":"5"}`, "limit must be a number")
	// C: two type errors: the alphabetically first field (limit < summary) is reported.
	schemaValWantBlocked(t, ms, st[scenToolListTasks], scenToolListTasks,
		`{"summary":"no","limit":"5"}`, "limit must be a number")
}

// scenPersistCall is one tools/call of the T13 scenarios.
type scenPersistCall struct{ tool, args string }

// scenWantNoWrite asserts every blocked call is an error that leaves count()
// unchanged, then that the valid call succeeds and raises count() by exactly 1
// (the +1 proves count() can see what the real handler writes).
func scenWantNoWrite(t *testing.T, ms *server.MCPServer, label string, count func() int, blocked []scenPersistCall, valid scenPersistCall) {
	t.Helper()
	before := count()
	for _, c := range blocked {
		isErr, text := unknownArgsRPCResult(t, ms, context.Background(), c.tool, c.args)
		if !isErr {
			t.Errorf("%s: %s %s: want isError, got success (%q)", label, c.tool, c.args, text)
		}
		if got := count(); got != before {
			t.Errorf("%s: %s %s: row count %d -> %d, a blocked call must not write", label, c.tool, c.args, before, got)
		}
	}
	isErr, text := unknownArgsRPCResult(t, ms, context.Background(), valid.tool, valid.args)
	if isErr {
		t.Errorf("%s: valid %s %s: want success, got error %q", label, valid.tool, valid.args, text)
	}
	if got := count(); got != before+1 {
		t.Errorf("%s: valid %s %s: row count %d -> %d, want +1", label, valid.tool, valid.args, before, got)
	}
}

// TestSchemaValidation_BlankNeverPersisted [F1127-04]: REAL handlers, no stubs.
// A blank-title or wrong-shaped call is rejected AND writes nothing; the same
// tool then accepts a valid call and the row count goes up by one.
// [F1127-04]
func TestSchemaValidation_BlankNeverPersisted(t *testing.T) {
	srv, ms := newTestMCPServer(t)
	ctx := context.Background()

	procCount := func() int {
		rows, err := srv.procedural.Query(ctx, procedural.QueryFilter{Limit: 100})
		if err != nil {
			t.Fatalf("procedural.Query: %v", err)
		}
		return len(rows)
	}
	visionCount := func() int {
		rows, err := srv.vision.List(ctx, vision.ListVisionFilter{})
		if err != nil {
			t.Fatalf("vision.List: %v", err)
		}
		return len(rows)
	}
	handoffCount := func() int {
		rows, err := srv.session.HandoffsSince(ctx, time.Time{}, 100)
		if err != nil {
			t.Fatalf("session.HandoffsSince: %v", err)
		}
		return len(rows)
	}

	scenWantNoWrite(t, ms, "procedural", procCount,
		[]scenPersistCall{
			{scenToolAddProcedural, scenJSON(t, map[string]any{"title": "   ", "when_to_use": "w"})},
			{scenToolAddProcedural, scenJSON(t, map[string]any{"title": scenU3000, "when_to_use": "w"})},
		},
		scenPersistCall{scenToolAddProcedural, `{"title":"t","when_to_use":"w"}`})
	scenWantNoWrite(t, ms, "vision", visionCount,
		[]scenPersistCall{
			{"add_vision_item", scenJSON(t, map[string]any{"title": scenU3000, "why_blocked": "x"})},
		},
		scenPersistCall{"add_vision_item", `{"title":"t","why_blocked":"x"}`})
	scenWantNoWrite(t, ms, "handoff", handoffCount,
		[]scenPersistCall{
			{"set_session_handoff", `{"intent":"i","next_actions":[{"step":"x"}]}`},
		},
		scenPersistCall{"set_session_handoff", `{"intent":"i"}`})
}
