package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/mark3labs/mcp-go/server"
)

// mcpInstructionsMaxRunes is the hard budget for the string injected into
// every `initialize` response. Measured in RUNES, not bytes: the text contains
// CJK trigger words, so len() over-counts by ~3x per CJK character and would
// let the real (token-relevant) size drift upwards unnoticed.
//
// Retuned 2000 -> 2288 (token-diet W5, 2026-08-15): mcpInstructions' own
// content is unaffected by W1-W4 (confirmed it never references any
// wayneblacktea:// resource URI or carries jsonText-shaped example text), but
// "the content didn't move" is not a reason to skip the retune — the whole
// point of this line item is that the OLD 2000 budget left only 11 runes of
// headroom over the measured 1989, so any single new discipline line in this
// string would trip the ceiling and blame "budget exceeded" rather than "you
// added something." New budget is ceil(1989 x 1.15) = 2288, restoring real
// headroom for exactly that kind of routine addition.
const mcpInstructionsMaxRunes = 2288

// coreToolSerializedMaxBytes is the budget for the whole core tool list as it
// goes out on the wire. Retuned 16,500 -> 19,294 (token-diet W5, 2026-08-15):
// W2 added get_project_arch's include_file_map param and W3 added a
// wayneblacktea://session/handoff/latest mention to get_today_context's
// description, raising the measured total to 16,777 bytes; new budget is
// ceil(16,777 x 1.15) = 19,294 (~5,847 tokens at 3.30 chars/token), leaving
// ~15% headroom for the next unrelated description edit to land without
// renegotiating this budget.
const coreToolSerializedMaxBytes = 19294

// newTestMCPServer builds a real Server on a throwaway SQLite file and returns
// its wired *server.MCPServer — the same object cmd/server and mcprunner use.
func newTestMCPServer(t *testing.T) (*Server, *server.MCPServer) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "toolgroups.db")
	stores, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{
		Backend:    storage.BackendSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("NewServerStores: %v", err)
	}
	t.Cleanup(func() { _ = stores.Close() })

	srv, err := New(stores)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, srv.MCPServer()
}

// mentionsTool reports whether text names tool as a whole identifier.
// strings.Contains is wrong here: "get_project" is a substring of
// "get_project_arch", so a plain Contains would report the non-core
// get_project as "mentioned" and silently break the closure invariant.
func mentionsTool(text, tool string) bool {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(tool) + `\b`).MatchString(text)
}

func TestMCPInstructions_WithinRuneBudget(t *testing.T) {
	got := len([]rune(mcpInstructions))
	t.Logf("mcpInstructions = %d runes (%d bytes), budget %d runes",
		got, len(mcpInstructions), mcpInstructionsMaxRunes)
	if got > mcpInstructionsMaxRunes {
		t.Errorf("mcpInstructions is %d runes, budget is %d — shrink the payload (move detail to "+
			"mcpProtocolAppendix), OR retune mcpInstructionsMaxRunes alongside a fresh measurement "+
			"— pick one, don't silently bump the number", got, mcpInstructionsMaxRunes)
	}
	if got < 500 {
		t.Errorf("mcpInstructions is only %d runes — the compression dropped real rules", got)
	}
}

// TestMCPInstructions_PreservesDisciplines pins the 17 behavioural rules the
// protocol text existed to enforce. Wording may change; a rule disappearing
// may not. Each case asserts every listed fragment is present, so a rewrite
// that quietly drops "MUST" or drops a tool from a rule fails here.
//
// The count is asserted below, not just stated here: a comment claiming "15"
// while the table held 17 is what shipped in PR #156 and made the third
// review army's count disagree with the report's.
func TestMCPInstructions_PreservesDisciplines(t *testing.T) {
	cases := []struct {
		discipline string
		fragments  []string
	}{
		{"session start: today context first", []string{"get_today_context", "pulled_forward"}},
		{"session start: pending handoff", []string{"resolve_handoff"}},
		{"architecture questions consult decisions and verify in code", []string{
			"list_decisions", "repo_name", "BEFORE answering", "verify in code",
		}},
		{"dispatch or Lead-direct requires in_progress", []string{
			"Lead-direct", "MUST update_task", `status="in_progress"`, "EVERY task",
		}},
		{"done work requires complete_task with artifact", []string{
			"PR merged", "MUST complete_task", "artifact=",
		}},
		{"never ask permission to update the GTD", []string{
			`NEVER ask "should I update the GTD?"`,
		}},
		{"confirmed multi-phase plan routes to confirm_plan", []string{
			"Multi-phase plan confirmed", "confirm_plan", "instead of add_task",
		}},
		{"log_decision before implementation, with its scope list", []string{
			"log_decision BEFORE implementation", "architecture", "deploy config",
			"scope pivot", "rule-source",
		}},
		{"draft outcome must be upgraded", []string{
			`result="unknown"`, "evaluate_outcome", "record_outcome",
		}},
		{"auto-log is a safety net, not a replacement", []string{
			"safety net", "NOT a replacement",
		}},
		{"3+ internal files requires an arch snapshot", []string{
			"3+ internal/ files", "MUST upsert_project_arch", "file_map",
		}},
		{"protocol rules never live in agent memory", []string{
			"NEVER store these rules in agent memory", "PR to internal/mcp/server.go",
		}},
		{"new follow-up work becomes a task", []string{"New follow-up", "add_task"}},
		{"future intent becomes a vision item", []string{"未來想做", "add_vision_item"}},
		{"sign-off writes a handoff", []string{"收工", "set_session_handoff"}},
		{"knowledge questions search first", []string{"search_knowledge"}},
		{"full protocol is one call away", []string{"initial_instructions", "expand_tools"}},
	}
	const wantDisciplines = 17
	if len(cases) != wantDisciplines {
		t.Errorf("discipline table has %d cases, the doc comment says %d — "+
			"update both together", len(cases), wantDisciplines)
	}
	for _, tc := range cases {
		t.Run(tc.discipline, func(t *testing.T) {
			for _, frag := range tc.fragments {
				if !strings.Contains(mcpInstructions, frag) {
					t.Errorf("discipline %q lost: mcpInstructions no longer contains %q",
						tc.discipline, frag)
				}
			}
		})
	}
}

// TestCoreToolSet_MatchesInstructions is the semantic-closure invariant: the
// injected protocol and the always-visible tool list must describe the same
// world. A tool named in the protocol but filtered out of tools/list would be
// advertised without a schema; a core tool the protocol never names is
// catalogue weight nobody asked for.
func TestCoreToolSet_MatchesInstructions(t *testing.T) {
	for _, name := range coreToolNames {
		if !mentionsTool(mcpInstructions, name) {
			t.Errorf("core tool %q is never named in mcpInstructions — "+
				"either name it there or drop it from coreToolNames", name)
		}
	}

	_, ms := newTestMCPServer(t)
	for name := range ms.ListTools() {
		if coreToolSet[name] {
			continue
		}
		if mentionsTool(mcpInstructions, name) {
			t.Errorf("mcpInstructions names %q but it is not in coreToolNames — "+
				"the client cannot see it in tools/list until expand_tools runs", name)
		}
	}
}

// sentenceBoundary splits a tool description into sentences: a terminator
// followed by whitespace or end of string. Rough by design — the invariant
// below only needs "is the pointer to expand_tools next to the tool it is
// pointing at", not linguistic accuracy.
var sentenceBoundary = regexp.MustCompile(`[.!?](?:\s|$)`)

// TestCoreToolDescriptions_RouteHiddenToolsThroughExpandTools is the second
// half of the semantic-closure invariant TestCoreToolSet_MatchesInstructions
// starts. That test covers the protocol text; this one covers the DESCRIPTIONS
// of the core tools, which are advertised in exactly the same breath.
//
// The rule: a core tool's description may name a non-core tool only in a
// sentence that also names expand_tools. A new session's tools/list carries
// the core set only, so "call get_task(task_id) for the full text" — the
// wording PR #156 shipped — sends the model after a tool that is not in the
// catalogue it was handed, with no hint that a call is needed to reveal it.
// Server-side routing still accepts hidden tools by name
// (TestAllTools_CallableWhenHidden), but that proves the server would answer,
// not that a client will ask.
func TestCoreToolDescriptions_RouteHiddenToolsThroughExpandTools(t *testing.T) {
	_, ms := newTestMCPServer(t)
	registered := ms.ListTools()

	hidden := make([]string, 0, len(registered))
	for name := range registered {
		if !coreToolSet[name] {
			hidden = append(hidden, name)
		}
	}
	sort.Strings(hidden)
	if len(hidden) == 0 {
		t.Fatal("no non-core tools registered — this invariant would be vacuous")
	}

	for _, name := range coreToolNames {
		st, ok := registered[name]
		if !ok {
			t.Fatalf("core tool %q is not registered on the server", name)
		}
		for _, sentence := range sentenceBoundary.Split(st.Tool.Description, -1) {
			if mentionsTool(sentence, expandToolsName) {
				continue
			}
			for _, other := range hidden {
				if mentionsTool(sentence, other) {
					t.Errorf("core tool %q describes %q, which a fresh session cannot see in "+
						"tools/list, without naming %s in the same sentence: %q",
						name, other, expandToolsName, strings.TrimSpace(sentence))
				}
			}
		}
	}
}

func TestCoreToolSet_SerializedBytesWithinBudget(t *testing.T) {
	_, ms := newTestMCPServer(t)
	registered := ms.ListTools()

	total := 0
	for _, name := range coreToolNames {
		st, ok := registered[name]
		if !ok {
			t.Fatalf("core tool %q is not registered on the server", name)
		}
		raw, err := json.Marshal(st.Tool)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		total += len(raw)
	}
	t.Logf("core tool set = %d tools, %d serialized bytes (budget %d, ~%d tokens at 3.30 chars/token)",
		len(coreToolNames), total, coreToolSerializedMaxBytes, total*10/33)
	if total > coreToolSerializedMaxBytes {
		t.Errorf("core tool set serializes to %d bytes, budget is %d — shrink the payload "+
			"(trim a description or move a tool out of coreToolNames), OR retune "+
			"coreToolSerializedMaxBytes alongside a fresh measurement — pick one, don't silently "+
			"bump the number", total, coreToolSerializedMaxBytes)
	}
}

// TestToolGroups_PartitionAllRegisteredTools proves the group table is an
// exhaustive, duplicate-free partition of everything the server registers
// (minus expand_tools itself). Without this, a newly registered tool would be
// permanently invisible: not core, not in any group, unreachable by any
// expand_tools call.
func TestToolGroups_PartitionAllRegisteredTools(t *testing.T) {
	_, ms := newTestMCPServer(t)
	registered := ms.ListTools()

	union := map[string]string{} // tool -> owning group
	for _, g := range toolGroups {
		for _, name := range g.Tools {
			if prev, dup := union[name]; dup {
				t.Errorf("tool %q is in two groups: %q and %q", name, prev, g.Name)
				continue
			}
			union[name] = g.Name
			if _, ok := registered[name]; !ok {
				t.Errorf("group %q lists %q, which is not registered on the server", g.Name, name)
			}
		}
	}

	for name := range registered {
		if name == expandToolsName {
			if _, grouped := union[name]; grouped {
				t.Errorf("%q must not be in a group — it is the always-visible entry point", name)
			}
			continue
		}
		if _, ok := union[name]; !ok {
			t.Errorf("registered tool %q belongs to no group — add it to toolGroups", name)
		}
	}

	if want, got := len(registered)-1, len(union); want != got {
		t.Errorf("group union covers %d tools, server registers %d (excluding %s)",
			got, want, expandToolsName)
	}
}

func TestToolGroups_ReservedNamesAreNotGroups(t *testing.T) {
	for _, g := range toolGroups {
		if g.Name == expandGroupAll || g.Name == expandGroupReset {
			t.Errorf("group name %q collides with a reserved expand_tools verb", g.Name)
		}
		if len(g.Tools) == 0 {
			t.Errorf("group %q is empty", g.Name)
		}
	}
	enum := expandToolsGroupEnum()
	if len(enum) != len(toolGroups)+2 {
		t.Fatalf("expandToolsGroupEnum returned %d values, want %d groups + 2 verbs",
			len(enum), len(toolGroups))
	}
	seen := map[string]bool{}
	for _, v := range enum {
		if seen[v] {
			t.Errorf("expandToolsGroupEnum contains duplicate %q", v)
		}
		seen[v] = true
	}
	if !seen[expandGroupAll] || !seen[expandGroupReset] {
		t.Error("expandToolsGroupEnum is missing a reserved verb")
	}
}

func TestVisibleToolSet(t *testing.T) {
	tests := []struct {
		name        string
		expanded    map[string]bool
		wantVisible []string
		wantHidden  []string
	}{
		{
			name:        "no expansion shows core only",
			expanded:    nil,
			wantVisible: []string{"add_task", "expand_tools", "record_outcome"},
			wantHidden:  []string{"begin_task", "recall", "system_health"},
		},
		{
			name:        "one group adds exactly that group",
			expanded:    map[string]bool{"procedural": true},
			wantVisible: []string{"recall", "add_procedural", "add_task"},
			wantHidden:  []string{"system_health", "begin_task"},
		},
		{
			name:        "all reveals every group",
			expanded:    map[string]bool{expandGroupAll: true},
			wantVisible: []string{"system_health", "begin_task", "recall", "sync_to_notion"},
			wantHidden:  nil,
		},
		{
			name:        "unknown group name is inert",
			expanded:    map[string]bool{"not_a_group": true},
			wantVisible: []string{"add_task"},
			wantHidden:  []string{"system_health", "recall"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := visibleToolSet(tc.expanded)
			for _, name := range tc.wantVisible {
				if !got[name] {
					t.Errorf("expected %q visible", name)
				}
			}
			for _, name := range tc.wantHidden {
				if got[name] {
					t.Errorf("expected %q hidden", name)
				}
			}
		})
	}
}
