package mcp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// [F1126-03] [F1126-04] Three-way required-argument report tests.

const (
	logDecisionRequiredBase = "title, context, decision and rationale are required"
	leakProbeA              = "SECRET-VALUE-9f3a"
	leakProbeB              = "Bearer abc"
)

var logDecisionRequired = []string{"title", "context", "decision", "rationale"}

// TestRequiredArgs_ThreeWay_AllAbsent: an empty payload (the transport-dropped
// case) lists every required key as missing and none as empty.
func TestRequiredArgs_ThreeWay_AllAbsent(t *testing.T) {
	t.Parallel()
	got := resultText(requiredArgsError(logDecisionRequiredBase, map[string]any{}, logDecisionRequired))
	want := logDecisionRequiredBase +
		` [received keys: none; missing: "title", "context", "decision", "rationale"; empty: none]`
	if got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

// TestRequiredArgs_ThreeWay_KeyPresentValueEmpty: blank values (empty string,
// whitespace, null) are "empty", never "missing".
func TestRequiredArgs_ThreeWay_KeyPresentValueEmpty(t *testing.T) {
	t.Parallel()
	args := map[string]any{"title": "", "context": "  ", "decision": nil, "rationale": "r"}
	got := resultText(requiredArgsError(logDecisionRequiredBase, args, logDecisionRequired))
	for _, w := range []string{
		`received keys: "context", "decision", "rationale", "title"`,
		`missing: none`,
		`empty: "title", "context", "decision"`,
	} {
		if !strings.Contains(got, w) {
			t.Errorf("message %q does not contain %q", got, w)
		}
	}
}

// TestRequiredArgs_ThreeWay_PartialArrival: a delivered non-blank key appears
// in neither missing nor empty.
func TestRequiredArgs_ThreeWay_PartialArrival(t *testing.T) {
	t.Parallel()
	args := map[string]any{"title": "t", "decision": ""}
	rep := classifyRequiredArgs(args, logDecisionRequired)
	if fmt.Sprint(rep.Received) != `[decision title]` {
		t.Errorf("Received = %v", rep.Received)
	}
	if fmt.Sprint(rep.Missing) != `[context rationale]` {
		t.Errorf("Missing = %v", rep.Missing)
	}
	if fmt.Sprint(rep.Empty) != `[decision]` {
		t.Errorf("Empty = %v", rep.Empty)
	}
	got := resultText(requiredArgsError(logDecisionRequiredBase, args, logDecisionRequired))
	want := logDecisionRequiredBase +
		` [received keys: "decision", "title"; missing: "context", "rationale"; empty: "decision"]`
	if got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}

// TestRequiredArgs_ProvidedBoolAndNumberNeverEmpty: a delivered boolean false
// or number 0 is a real value, so the seam's type-aware report must not list
// it under empty.
func TestRequiredArgs_ProvidedBoolAndNumberNeverEmpty(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	r := callChecklistToggle(t, s, map[string]any{"task_id": uuid.NewString(), "done": false})
	if !r.IsError {
		t.Fatalf("missing item_id must error, got %q", resultText(r))
	}
	text := resultText(r)
	if !strings.HasPrefix(text, "item_id is required") {
		t.Fatalf("message = %q, want item_id is required prefix", text)
	}
	if !strings.Contains(text, `missing: "item_id"`) {
		t.Errorf("message %q must list item_id as missing", text)
	}
	idx := strings.Index(text, "empty:")
	if idx < 0 {
		t.Fatalf("message %q has no empty: section", text)
	}
	emptySection := text[idx:]
	if strings.Contains(emptySection, `"done"`) || strings.Contains(emptySection, `"task_id"`) {
		t.Errorf("provided done:false / task_id listed as empty: %q", text)
	}
}

// TestRequiredArgs_NeverEchoesValues: values never reach the message, on the
// helper and on the four real handlers.
func TestRequiredArgs_NeverEchoesValues(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	results := map[string]string{
		"log_decision": resultText(callLogDecision(t, s, map[string]any{
			"title": leakProbeA, "context": leakProbeB, "decision": "  ",
		})),
		"set_session_handoff": resultText(callSetSessionHandoff(t, s, map[string]any{
			"intent": "  ", "context_summary": leakProbeA, "repo_name": leakProbeB,
		})),
		"add_task": resultText(callTool(t, s, "add_task", map[string]any{
			"title": "  ", "description": leakProbeA, "assignee": leakProbeB,
		}, s.handleAddTask)),
		"update_task": resultText(callUpdateTask(t, s, map[string]any{
			"task_id": "", "title": leakProbeA, "assignee": leakProbeB,
		})),
	}
	for tool, text := range results {
		if !strings.Contains(text, "[received keys:") {
			t.Errorf("%s: expected a three-way report, got %q", tool, text)
		}
		for _, secret := range []string{leakProbeA, leakProbeB, "SECRET", "Bearer"} {
			if strings.Contains(text, secret) {
				t.Errorf("%s: message echoes value %q: %q", tool, secret, text)
			}
		}
	}
}

// TestRequiredArgs_KeyNamesSanitizedAndCapped: key names are caller-controlled,
// so control bytes are stripped, each key is length-capped and the received
// list is capped at unknownArgMaxListed.
func TestRequiredArgs_KeyNamesSanitizedAndCapped(t *testing.T) {
	t.Parallel()
	args := map[string]any{
		"a\nb\x1b[31m":           "v",
		strings.Repeat("k", 200): "v",
	}
	for i := 0; i < 13; i++ {
		args[fmt.Sprintf("extra_%02d", i)] = "v"
	}
	// 15 keys in total.
	got := resultText(requiredArgsError("x is required", args, []string{"x"}))
	if strings.ContainsAny(got, "\n\x1b") {
		t.Errorf("control bytes survived in %q", got)
	}
	if !strings.Contains(got, ", +5 more") {
		t.Errorf("want ', +5 more' for 15 keys with cap 10, got %q", got)
	}
	if strings.Contains(got, strings.Repeat("k", unknownArgKeyMaxRunes+1)) {
		t.Errorf("200-rune key not capped to %d runes", unknownArgKeyMaxRunes)
	}
}

// TestRequireArgs_NonStringValueIsEmpty: a non-string value for a key read via
// stringArg never satisfies the check; it is reported under empty.
func TestRequireArgs_NonStringValueIsEmpty(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	ld := resultText(callLogDecision(t, s, map[string]any{
		"title": 123, "context": "c", "decision": "d", "rationale": "r",
	}))
	if !strings.HasPrefix(ld, logDecisionRequiredBase) || !strings.Contains(ld, `empty: "title"`) {
		t.Errorf("log_decision non-string title: %q", ld)
	}

	sh := resultText(callSetSessionHandoff(t, s, map[string]any{"intent": true}))
	if !strings.HasPrefix(sh, "intent is required") || !strings.Contains(sh, `empty: "intent"`) {
		t.Errorf("set_session_handoff non-string intent: %q", sh)
	}
}

// TestLogDecision_RequiredThreeWay: end-to-end on the real handler, including
// the whitespace-only case that now fails the required check.
func TestLogDecision_RequiredThreeWay(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	r := callLogDecision(t, s, map[string]any{})
	want := logDecisionRequiredBase +
		` [received keys: none; missing: "title", "context", "decision", "rationale"; empty: none]`
	if !r.IsError || resultText(r) != want {
		t.Errorf("empty payload: got %q, want %q", resultText(r), want)
	}

	r = callLogDecision(t, s, map[string]any{"title": "x", "context": "", "decision": "  "})
	got := resultText(r)
	for _, w := range []string{
		`received keys: "context", "decision", "title"`,
		`missing: "rationale"`,
		`empty: "context", "decision"`,
	} {
		if !r.IsError || !strings.Contains(got, w) {
			t.Errorf("partial payload: %q does not contain %q", got, w)
		}
	}
}

// TestAddTask_RequiredThreeWay: the seam's checkRequiredPresent path.
func TestAddTask_RequiredThreeWay(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	r := callTool(t, s, "add_task", map[string]any{}, s.handleAddTask)
	got := resultText(r)
	if !r.IsError || !strings.HasPrefix(got, wantTitleRequired) {
		t.Fatalf("got %q, want prefix %q", got, wantTitleRequired)
	}
	for _, w := range []string{"received keys: none", `missing: "title"`, "empty: none"} {
		if !strings.Contains(got, w) {
			t.Errorf("%q does not contain %q", got, w)
		}
	}
}

// TestUpdateTask_RequiredThreeWay: the seam's UUID-typed required branch.
func TestUpdateTask_RequiredThreeWay(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	r := callUpdateTask(t, s, map[string]any{})
	got := resultText(r)
	if !r.IsError || !strings.HasPrefix(got, "task_id is required") {
		t.Fatalf("got %q, want task_id is required prefix", got)
	}
	for _, w := range []string{"received keys: none", `missing: "task_id"`, "empty: none"} {
		if !strings.Contains(got, w) {
			t.Errorf("%q does not contain %q", got, w)
		}
	}

	r = callUpdateTask(t, s, map[string]any{"task_id": "", "title": "t"})
	got = resultText(r)
	if !strings.Contains(got, `empty: "task_id"`) || !strings.Contains(got, "missing: none") {
		t.Errorf("blank task_id must be empty, not missing: %q", got)
	}
}

// TestSetSessionHandoff_RequiredThreeWay: the second raw handler.
func TestSetSessionHandoff_RequiredThreeWay(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	r := callSetSessionHandoff(t, s, map[string]any{})
	want := `intent is required [received keys: none; missing: "intent"; empty: none]`
	if !r.IsError || resultText(r) != want {
		t.Errorf("got %q, want %q", resultText(r), want)
	}
}
