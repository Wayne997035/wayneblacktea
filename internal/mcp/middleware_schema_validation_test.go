package mcp

// Tests for the generic schema-validation middleware (MCP-6, GTD:822f9ae7).
// Written against the spec's acceptance table, not against an implementation:
// every "must be blocked" case asserts the tool's handler was NEVER called
// (counting stub swapped in with ms.AddTool), so a seam or raw handler that
// happens to return the same text cannot mask a missing middleware.
//
// No test here calls t.Parallel: several read or write the process-global
// schemaValidationExempt map.

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const schemaValUUID = "11111111-1111-4111-8111-111111111111"

// schemaValStubs replaces each named real tool on ms with a counting stub that
// returns a plain success, keeping the tool's real registered schema. The
// returned counters are keyed by tool name.
func schemaValStubs(t *testing.T, ms *server.MCPServer, names ...string) map[string]*atomic.Int32 {
	t.Helper()
	out := make(map[string]*atomic.Int32, len(names))
	for _, name := range names {
		st := ms.GetTool(name)
		if st == nil {
			t.Fatalf("tool %q is not registered", name)
		}
		var n atomic.Int32
		out[name] = &n
		ms.AddTool(st.Tool, func(context.Context, mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
			n.Add(1)
			return mcpmsg.NewToolResultText("stub-ok"), nil
		})
	}
	return out
}

// schemaValAddSynthetic registers a brand-new tool (new name, never a real
// one) with a counting stub on ms.
func schemaValAddSynthetic(ms *server.MCPServer, tool mcpmsg.Tool) *atomic.Int32 {
	var n atomic.Int32
	ms.AddTool(tool, func(context.Context, mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
		n.Add(1)
		return mcpmsg.NewToolResultText("stub-ok"), nil
	})
	return &n
}

// schemaValCall drives one tools/call through ms.HandleMessage and reports
// isError, text, and how many times the stub counter moved.
func schemaValCall(t *testing.T, ms *server.MCPServer, n *atomic.Int32, name, argsJSON string) (isErr bool, text string, calls int32) {
	t.Helper()
	before := int32(0)
	if n != nil {
		before = n.Load()
	}
	isErr, text = unknownArgsRPCResult(t, ms, context.Background(), name, argsJSON)
	if n != nil {
		calls = n.Load() - before
	}
	return isErr, text, calls
}

// schemaValWantBlocked asserts the call is rejected with exactly wantText and
// the handler stub never ran.
func schemaValWantBlocked(t *testing.T, ms *server.MCPServer, n *atomic.Int32, name, argsJSON, wantText string) {
	t.Helper()
	isErr, text, calls := schemaValCall(t, ms, n, name, argsJSON)
	if !isErr {
		t.Errorf("%s %s: want isError, got success (text %q)", name, argsJSON, text)
	}
	if text != wantText {
		t.Errorf("%s %s: text = %q, want %q", name, argsJSON, text, wantText)
	}
	if calls != 0 {
		t.Errorf("%s %s: handler ran %d time(s), want 0", name, argsJSON, calls)
	}
}

// schemaValWantReaches asserts the call got past the middleware: handler stub
// ran once and the result is not an error.
func schemaValWantReaches(t *testing.T, ms *server.MCPServer, n *atomic.Int32, name, argsJSON string) {
	t.Helper()
	isErr, text, calls := schemaValCall(t, ms, n, name, argsJSON)
	if calls != 1 || isErr {
		t.Errorf("%s %s: want handler reached once without error, got calls=%d isError=%v text=%q",
			name, argsJSON, calls, isErr, text)
	}
}

// TestSchemaValidation_MissingRequired [F1127-04] AC-1.
func TestSchemaValidation_MissingRequired(t *testing.T) {
	_, ms := newTestMCPServer(t)
	st := schemaValStubs(t, ms, "add_procedural")
	n := st["add_procedural"]

	schemaValWantBlocked(t, ms, n, "add_procedural", `{"when_to_use":"w"}`,
		`title is required [received keys: "when_to_use"; missing: "title"; empty: none]`)
	// Negative: complete input reaches the handler.
	schemaValWantReaches(t, ms, n, "add_procedural", `{"title":"t","when_to_use":"w"}`)
}

// TestSchemaValidation_TypeMismatch [F1127-05] AC-2, AC-3, AC-7, plus the
// Target-7 canary: an argument VALUE never appears in the error text.
func TestSchemaValidation_TypeMismatch(t *testing.T) {
	_, ms := newTestMCPServer(t)
	st := schemaValStubs(t, ms,
		"analyze_recent_patterns", "assemble_context", "delete_task", "reconcile_merged_prs")

	// AC-2
	schemaValWantBlocked(t, ms, st["analyze_recent_patterns"], "analyze_recent_patterns",
		`{"days":"3"}`, "days must be a number")
	schemaValWantReaches(t, ms, st["analyze_recent_patterns"], "analyze_recent_patterns", `{"days":3}`)

	// Canary: a wrong-typed value must be rejected and never echoed back.
	const canary = "VALUE-CANARY-7f3"
	isErr, text, calls := schemaValCall(t, ms, st["analyze_recent_patterns"],
		"analyze_recent_patterns", `{"days":"`+canary+`"}`)
	if !isErr || calls != 0 {
		t.Errorf("canary call: want rejected with handler not run, got isError=%v calls=%d text=%q", isErr, calls, text)
	}
	if strings.Contains(text, canary) {
		t.Errorf("error text leaks the argument value: %q", text)
	}
	if text != "days must be a number" {
		t.Errorf("canary call text = %q, want %q", text, "days must be a number")
	}

	// AC-3
	schemaValWantBlocked(t, ms, st["assemble_context"], "assemble_context",
		`{"objective":"o","persist":"true"}`, "persist must be a boolean")
	schemaValWantBlocked(t, ms, st["assemble_context"], "assemble_context",
		`{"objective":"o","files_touched":"x"}`, "files_touched must be an array")
	schemaValWantReaches(t, ms, st["assemble_context"], "assemble_context",
		`{"objective":"o","persist":true}`)
	schemaValWantReaches(t, ms, st["assemble_context"], "assemble_context",
		`{"objective":"o","files_touched":["a"]}`)

	// AC-7
	schemaValWantBlocked(t, ms, st["delete_task"], "delete_task",
		`{"task_id":"`+schemaValUUID+`","confirm":"true"}`, "confirm must be a boolean")
	schemaValWantBlocked(t, ms, st["reconcile_merged_prs"], "reconcile_merged_prs",
		`{"confirm":"true"}`, "confirm must be a boolean")
	schemaValWantReaches(t, ms, st["delete_task"], "delete_task",
		`{"task_id":"`+schemaValUUID+`","confirm":true}`)
	schemaValWantReaches(t, ms, st["reconcile_merged_prs"], "reconcile_merged_prs", `{"confirm":true}`)
}

// TestSchemaValidation_EnumInvalid [F1127-06] AC-4.
func TestSchemaValidation_EnumInvalid(t *testing.T) {
	_, ms := newTestMCPServer(t)
	n := schemaValStubs(t, ms, "set_task_status")["set_task_status"]

	schemaValWantBlocked(t, ms, n, "set_task_status",
		`{"task_id":"`+schemaValUUID+`","status":"bogus"}`,
		"status must be one of: pending, in_progress, completed, cancelled")
	schemaValWantReaches(t, ms, n, "set_task_status",
		`{"task_id":"`+schemaValUUID+`","status":"completed"}`)
}

// TestSchemaValidation_EmptyEnumPasses [F1127-06] AC-5: enum value "" means
// unset and passes; a real non-member does not.
func TestSchemaValidation_EmptyEnumPasses(t *testing.T) {
	_, ms := newTestMCPServer(t)
	st := schemaValStubs(t, ms, "list_tasks", "expand_tools")

	schemaValWantReaches(t, ms, st["list_tasks"], "list_tasks", `{"status":""}`)
	schemaValWantReaches(t, ms, st["expand_tools"], "expand_tools", `{"group":""}`)
	// Negative: the same field with a bogus value is rejected before the handler.
	schemaValWantBlocked(t, ms, st["list_tasks"], "list_tasks", `{"status":"bogus"}`,
		"status must be one of: active, all, pending, in_progress, completed, cancelled")
}

// TestSchemaValidation_AssigneeAliasPasses [F1127-07] AC-6: assignee skips the
// enum check on the three seam tools; NormalizeActor in the real handler judges.
func TestSchemaValidation_AssigneeAliasPasses(t *testing.T) {
	// Negative half first, on a server whose begin_task is the REAL handler.
	_, real := newTestMCPServer(t)
	isErr, text := unknownArgsRPCResult(t, real, context.Background(), "begin_task",
		`{"task_id":"`+schemaValUUID+`","assignee":"bogus"}`)
	const wantActor = "assignee is not a recognized actor; must be one of: claude, codex, human"
	if !isErr || !strings.Contains(text, wantActor) {
		t.Errorf("real begin_task assignee=bogus: isError=%v text=%q, want error containing %q", isErr, text, wantActor)
	}

	_, ms := newTestMCPServer(t)
	st := schemaValStubs(t, ms, "begin_task", "update_task", "add_task")
	schemaValWantReaches(t, ms, st["begin_task"], "begin_task",
		`{"task_id":"`+schemaValUUID+`","assignee":"Claude"}`)
	schemaValWantReaches(t, ms, st["update_task"], "update_task",
		`{"task_id":"`+schemaValUUID+`","assignee":" codex "}`)
	schemaValWantReaches(t, ms, st["add_task"], "add_task",
		`{"title":"t","area":"a","assignee":"Claude"}`)
	// "bogus" also passes the middleware (the handler, not the enum, rejects it).
	schemaValWantReaches(t, ms, st["begin_task"], "begin_task",
		`{"task_id":"`+schemaValUUID+`","assignee":"bogus"}`)
}

// TestSchemaValidation_UnknownArgsStillFirst [F1127-01] AC-12: a call with an
// unknown key AND a missing required key reports the unknown key.
func TestSchemaValidation_UnknownArgsStillFirst(t *testing.T) {
	_, ms := newTestMCPServer(t)
	n := schemaValStubs(t, ms, "complete_task")["complete_task"]
	schemaValWantBlocked(t, ms, n, "complete_task", `{"artifact_url":"x"}`, wantCompleteTaskUnknownArgText)
}

// TestDeriveToolSpec_EnumAsAnySlice [F1127-03]: an enum decoded as []any must
// be kept, not silently dropped (a dropped enum is a fail-open hole).
func TestDeriveToolSpec_EnumAsAnySlice(t *testing.T) {
	tool := mcpmsg.NewTool("zz_enum_any_tool", mcpmsg.WithString("mode", mcpmsg.Enum("a", "b")))
	prop, ok := tool.InputSchema.Properties["mode"].(map[string]any)
	if !ok {
		t.Fatalf("test setup: property mode is %T, want map[string]any", tool.InputSchema.Properties["mode"])
	}
	prop["enum"] = []any{"a", "b"}

	spec, err := deriveToolSpec(tool)
	if err != nil {
		t.Fatalf("deriveToolSpec returned error: %v", err)
	}
	if spec == nil {
		t.Fatal("deriveToolSpec returned nil spec")
	}
	arg := spec.args["mode"]
	if arg == nil {
		t.Fatal("spec has no arg \"mode\"")
	}
	if !reflect.DeepEqual(arg.enum, []string{"a", "b"}) {
		t.Errorf("spec.args[mode].enum = %#v, want []string{\"a\", \"b\"}", arg.enum)
	}
}

// TestSchemaValidation_FailClosed [F1127-02] AC-9: a schema the middleware
// cannot read, or no MCPServer at all, rejects the call and never reaches next.
func TestSchemaValidation_FailClosed(t *testing.T) {
	srv, ms := newTestMCPServer(t)

	// RawInputSchema tool: Type stays "object"; the raw schema itself makes it unreadable.
	rawName := "zz_raw_schema_tool"
	rawN := schemaValAddSynthetic(ms, mcpmsg.NewTool(rawName,
		mcpmsg.WithRawInputSchema(json.RawMessage(`{"type":"object","properties":{}}`))))
	schemaValWantBlocked(t, ms, rawN, rawName, `{}`,
		`tool "zz_raw_schema_tool" has no readable input schema`)

	// Negative: listed in schemaValidationExempt -> passes through to the handler.
	exName := "zz_exempt_raw_tool"
	exN := schemaValAddSynthetic(ms, mcpmsg.NewTool(exName,
		mcpmsg.WithRawInputSchema(json.RawMessage(`{"type":"object","properties":{}}`))))
	schemaValidationExempt[exName] = "test: exempt raw-schema tool"
	t.Cleanup(func() { delete(schemaValidationExempt, exName) })
	schemaValWantReaches(t, ms, exN, exName, `{}`)

	// MCPServer not set: fail closed, NOT next.
	nilName := "zz_nil_ms_tool"
	nilN := schemaValAddSynthetic(ms, mcpmsg.NewTool(nilName, mcpmsg.WithString("note")))
	srv.mcpServer.Store(nil)
	schemaValWantBlocked(t, ms, nilN, nilName, `{}`,
		`schema unavailable for tool "zz_nil_ms_tool"`)
}

// schemaValBadCall returns arguments the middleware must reject for this tool:
// {} when it has a required field, else one wrong-typed or bad-enum value.
// constrained is false when the tool has nothing to reject (no required field
// and no number / boolean / array / enum property).
func schemaValBadCall(tool mcpmsg.Tool) (argsJSON string, constrained bool) {
	if len(tool.InputSchema.Required) > 0 {
		return `{}`, true
	}
	names := make([]string, 0, len(tool.InputSchema.Properties))
	for k := range tool.InputSchema.Properties {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		prop, ok := tool.InputSchema.Properties[k].(map[string]any)
		if !ok {
			continue
		}
		typ, _ := prop["type"].(string)
		if typ != "" && typ != argTypeString { // number / boolean / array
			return `{"` + k + `":"x"}`, true
		}
		if prop["enum"] != nil {
			return `{"` + k + `":"bogus-enum-value"}`, true
		}
	}
	return "", false
}

// schemaValUnconstrained lists the tools with nothing the middleware can
// reject (sorted).
func schemaValUnconstrained(tools map[string]*server.ServerTool) []string {
	var out []string
	for name, st := range tools {
		if _, c := schemaValBadCall(st.Tool); !c {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// schemaValPinnedVacuous is the pinned list of tools with no required field
// and no number / boolean / array / enum property (spec AC-10).
var schemaValPinnedVacuous = []string{
	"closeout_session_check", "detect_unclosed_loops", "get_due_reviews",
	"get_today_context", "initial_instructions", "list_playbooks",
	"list_vision_items", "navigate_knowledge", "reconcile_dashboard",
}

// TestSchemaValidation_AllRegisteredToolsCovered [F1127-01] AC-10.
func TestSchemaValidation_AllRegisteredToolsCovered(t *testing.T) {
	_, ms := newTestMCPServer(t)
	tools := ms.ListTools()

	if len(schemaValidationExempt) != 0 {
		t.Errorf("schemaValidationExempt has %d entries, want 0: %v", len(schemaValidationExempt), schemaValidationExempt)
	}
	for name := range schemaValidationExempt {
		if tools[name] == nil {
			t.Errorf("schemaValidationExempt names %q, which is not a registered tool", name)
		}
	}

	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	stubs := schemaValStubs(t, ms, names...)

	vacuous := schemaValUnconstrained(tools)
	if !reflect.DeepEqual(vacuous, schemaValPinnedVacuous) {
		t.Errorf("tools with no constrained property = %v, want the pinned list %v "+
			"(a new tool with only optional strings must be acknowledged here)", vacuous, schemaValPinnedVacuous)
	}

	covered := 0
	for _, name := range names {
		argsJSON, constrained := schemaValBadCall(tools[name].Tool)
		if !constrained {
			continue
		}
		covered++
		isErr, text, calls := schemaValCall(t, ms, stubs[name], name, argsJSON)
		if !isErr || calls != 0 {
			t.Errorf("%s %s: not blocked (isError=%v, handler calls=%d, text=%q)", name, argsJSON, isErr, calls, text)
		}
	}
	if covered != 85 || len(vacuous) != 9 || covered+len(vacuous) != 94 || len(tools) != 94 {
		t.Errorf("counts: covered=%d vacuous=%d registered=%d, want 85 + 9 == 94 == registered", covered, len(vacuous), len(tools))
	}

	schemaValAssertOptionalOnlyToolDetected(t)
}

// schemaValAssertOptionalOnlyToolDetected is AC-10's negative half: a 95th tool
// with only optional strings is listed by the helper, so the pinned-list
// comparison would turn red for it.
func schemaValAssertOptionalOnlyToolDetected(t *testing.T) {
	t.Helper()
	_, ms2 := newTestMCPServer(t)
	schemaValAddSynthetic(ms2, mcpmsg.NewTool("zz_optional_only", mcpmsg.WithString("note")))
	got := schemaValUnconstrained(ms2.ListTools())
	found := false
	for _, n := range got {
		if n == "zz_optional_only" {
			found = true
		}
	}
	if !found {
		t.Errorf("helper did not list the optional-only synthetic tool: %v", got)
	}
	if reflect.DeepEqual(got, schemaValPinnedVacuous) {
		t.Errorf("helper output with an extra optional-only tool still equals the pinned list")
	}
}

// schemaValGapCRequired is the 18 required string fields whose handlers accept a
// whitespace-only value today (spec Gap C), with each tool's required list.
var schemaValGapCRequired = map[string][]string{
	"add_procedural":   {"title", "when_to_use"},
	"add_vision_item":  {"title", "why_blocked"},
	"create_concept":   {"title", "content"},
	"extract_skill":    {"name"},
	"get_active_work":  {"repo_name"},
	"propose_goal":     {"title", "area"},
	"propose_project":  {"name", "title", "area"},
	"query_procedural": {"keywords"},
	"recall":           {"query"},
	"search_atoms":     {"query"},
	"search_knowledge": {"query"},
	"search_skills":    {"query"},
}

// TestSchemaValidation_BlankRequiredRejected [F1127-04] AC-8.
func TestSchemaValidation_BlankRequiredRejected(t *testing.T) {
	_, ms := newTestMCPServer(t)
	names := []string{"list_decisions", "complete_task"}
	for tool := range schemaValGapCRequired {
		names = append(names, tool)
	}
	sort.Strings(names)
	st := schemaValStubs(t, ms, names...)

	// The two add_procedural texts are fixed verbatim by the spec.
	const wantTitleEmpty = `title is required [received keys: "title", "when_to_use"; missing: none; empty: "title"]`
	schemaValWantBlocked(t, ms, st["add_procedural"], "add_procedural", `{"title":"","when_to_use":"w"}`, wantTitleEmpty)
	schemaValWantBlocked(t, ms, st["add_procedural"], "add_procedural", `{"title":"   ","when_to_use":"w"}`, wantTitleEmpty)

	rows := 0
	for tool, req := range schemaValGapCRequired {
		for _, field := range req {
			rows++
			args := map[string]any{}
			for _, r := range req {
				args[r] = "v"
			}
			args[field] = "   "
			raw, err := json.Marshal(args)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			isErr, text, calls := schemaValCall(t, ms, st[tool], tool, string(raw))
			if !isErr || calls != 0 {
				t.Errorf("%s blank %s: not blocked (isError=%v, handler calls=%d, text=%q)", tool, field, isErr, calls, text)
			}
			if !strings.HasPrefix(text, field+" is required [") {
				t.Errorf("%s blank %s: text %q does not start with %q", tool, field, text, field+" is required [")
			}
			if !strings.HasSuffix(text, `; empty: "`+field+`"]`) {
				t.Errorf("%s blank %s: text %q does not name the field in its empty: slot", tool, field, text)
			}
		}
	}
	if rows != 18 {
		t.Errorf("Gap-C table has %d rows, want 18", rows)
	}

	// Negatives: complete input, an optional blank, and a seam UUID blank all
	// get past the middleware.
	schemaValWantReaches(t, ms, st["add_procedural"], "add_procedural", `{"title":"t","when_to_use":"w"}`)
	schemaValWantReaches(t, ms, st["list_decisions"], "list_decisions", `{"repo_name":"   "}`)
	schemaValWantReaches(t, ms, st["complete_task"], "complete_task", `{"task_id":"   "}`)
}

// schemaValSeamTools are the 23 seam tools (spec Inventory, route = seam).
var schemaValSeamTools = []string{
	"add_task", "begin_task", "complete_task", "create_goal", "create_project",
	"delete_project", "delete_task", "expand_tools", "get_project", "get_task",
	"get_upcoming_work", "list_goals", "list_projects", "list_tasks", "log_activity",
	"restore_project", "set_task_status", "task_checklist_add_item",
	"task_checklist_complete", "task_checklist_toggle", "update_project",
	"update_project_status", "update_task",
}

func schemaValPropType(tool mcpmsg.Tool, name string) string {
	prop, _ := tool.InputSchema.Properties[name].(map[string]any)
	typ, _ := prop["type"].(string)
	return typ
}

func schemaValFirstEnum(tool mcpmsg.Tool, name string) string {
	prop, _ := tool.InputSchema.Properties[name].(map[string]any)
	switch e := prop["enum"].(type) {
	case []string:
		if len(e) > 0 {
			return e[0]
		}
	case []any:
		if len(e) > 0 {
			s, _ := e[0].(string)
			return s
		}
	}
	return ""
}

// schemaValDummyArgs gives every required field a plausible valid value.
func schemaValDummyArgs(tool mcpmsg.Tool) map[string]any {
	args := map[string]any{}
	for _, name := range tool.InputSchema.Required {
		switch {
		case schemaValPropType(tool, name) != argTypeString:
			// The only non-string required field among the seam tools is
			// task_checklist_toggle.done (boolean).
			if name != "done" {
				panic("schemaValDummyArgs: unexpected non-string required field " + name)
			}
			args[name] = true
		case schemaValFirstEnum(tool, name) != "":
			args[name] = schemaValFirstEnum(tool, name)
		case strings.HasSuffix(name, "_id"):
			args[name] = schemaValUUID
		case name == "actor":
			args[name] = "claude"
		default:
			args[name] = "x"
		}
	}
	return args
}

// schemaValDirect runs the registered handler of name directly, bypassing
// every middleware, and reports isError plus the first text block.
func schemaValDirect(t *testing.T, st *server.ServerTool, name string, args map[string]any) (bool, string) {
	t.Helper()
	var req mcpmsg.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := st.Handler(context.Background(), req)
	if err != nil || res == nil {
		t.Fatalf("%s direct handler: res=%v err=%v", name, res, err)
	}
	text := ""
	if len(res.Content) > 0 {
		if c, ok := res.Content[0].(mcpmsg.TextContent); ok {
			text = c.Text
		}
	}
	return res.IsError, text
}

type schemaValProbe struct {
	label string
	args  map[string]any
}

// schemaValSeamProbes builds the three AC-11 inputs for one seam tool: {}, one
// wrong-typed field (required fields stay valid), and each required string
// field blank in turn.
func schemaValSeamProbes(tool mcpmsg.Tool) []schemaValProbe {
	probes := []schemaValProbe{{"empty", map[string]any{}}}

	props := make([]string, 0, len(tool.InputSchema.Properties))
	for k := range tool.InputSchema.Properties {
		props = append(props, k)
	}
	sort.Strings(props)
	wrong := schemaValProbe{label: "wrong-type", args: schemaValDummyArgs(tool)}
	picked := false
	for _, k := range props {
		if schemaValPropType(tool, k) != argTypeString {
			wrong.args[k] = "x"
			picked = true
			break
		}
	}
	if !picked && len(props) > 0 {
		wrong.args[props[0]] = float64(123)
	}
	probes = append(probes, wrong)

	for _, k := range tool.InputSchema.Required {
		if schemaValPropType(tool, k) != argTypeString {
			continue
		}
		args := schemaValDummyArgs(tool)
		args[k] = "   "
		probes = append(probes, schemaValProbe{label: "blank " + k, args: args})
	}
	return probes
}

// TestSchemaValidation_SeamParity [F1127-07] AC-11: for the 23 seam tools the
// middleware path and the seam's own handler give the same error text (or, when
// either succeeds, the same isError flag). Handlers are the REAL ones here.
func TestSchemaValidation_SeamParity(t *testing.T) {
	if len(schemaValSeamTools) != 23 {
		t.Fatalf("seam list has %d names, want 23", len(schemaValSeamTools))
	}
	_, ms := newTestMCPServer(t)
	for _, name := range schemaValSeamTools {
		if ms.GetTool(name) == nil {
			t.Fatalf("seam tool %q is not registered", name)
		}
	}

	for _, name := range schemaValSeamTools {
		st := ms.GetTool(name)
		for _, p := range schemaValSeamProbes(st.Tool) {
			raw, err := json.Marshal(p.args)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			viaErr, viaText := unknownArgsRPCResult(t, ms, context.Background(), name, string(raw))
			dirErr, dirText := schemaValDirect(t, st, name, p.args)
			switch {
			case viaErr && dirErr:
				if viaText != dirText {
					t.Errorf("%s [%s] %s: middleware path %q != seam path %q", name, p.label, raw, viaText, dirText)
				}
			default:
				if viaErr != dirErr {
					t.Errorf("%s [%s] %s: isError differs: middleware path %v (%q), seam path %v (%q)",
						name, p.label, raw, viaErr, viaText, dirErr, dirText)
				}
			}
		}
	}

	// A requiredMsg override keeps its own text through the middleware path.
	isErr, text := unknownArgsRPCResult(t, ms, context.Background(), "create_project", `{}`)
	if !isErr || !strings.HasPrefix(text, "name, title and area are required [") {
		t.Errorf("create_project {}: isError=%v text=%q, want the requiredMsg override prefix", isErr, text)
	}
}
