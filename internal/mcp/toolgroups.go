package mcp

// Progressive tool disclosure — the static half.
//
// This file holds the two declarative tables the `expand_tools` flow reads:
//
//   - coreToolNames: the tool set advertised in `tools/list` before any
//     expansion. It is deliberately tiny (18 entries) because every connecting
//     client pays for the full serialised tool list on every `tools/list`.
//   - toolGroups: an exhaustive, mutually-exclusive partition of EVERY tool
//     registered by MCPServer() (except `expand_tools` itself), named after the
//     register*Tools call site that owns it in server.go. Reusing those names
//     avoids minting a second, drifting taxonomy of the same tools.
//
// Nothing here can make a tool uncallable. mcp-go applies tool filters only in
// handleListTools (server/server.go:1523-1530); handleToolCall (:1556-1605)
// looks the name up in the global registry directly and never consults a
// filter. Hiding a tool from the catalogue therefore only changes what the
// client is *told about* — a hidden tool invoked by name still executes.

// coreToolNames is the always-visible tool set.
//
// Membership is not a free choice: it is pinned to the compressed
// mcpInstructions string by the semantic-closure invariant
// (TestCoreToolSet_MatchesInstructions) — every tool named in mcpInstructions
// MUST be core, and every core tool MUST be named in mcpInstructions.
// Otherwise the protocol text would either advertise a tool the client cannot
// see in its catalogue, or ship catalogue weight the protocol never asks for.
var coreToolNames = []string{
	"add_task",
	"add_vision_item",
	"complete_task",
	"confirm_plan",
	"evaluate_outcome",
	"expand_tools",
	"get_project_arch",
	"get_today_context",
	"initial_instructions",
	"list_decisions",
	"list_tasks",
	"log_decision",
	"record_outcome",
	"resolve_handoff",
	"search_knowledge",
	"set_session_handoff",
	"update_task",
	"upsert_project_arch",
}

// coreToolSet is coreToolNames as a lookup set, built once at init.
var coreToolSet = func() map[string]bool {
	m := make(map[string]bool, len(coreToolNames))
	for _, n := range coreToolNames {
		m[n] = true
	}
	return m
}()

// expandGroupAll reveals every registered tool for the calling session.
// expandGroupReset drops the session back to coreToolNames.
// Neither may collide with a real group name (asserted by
// TestToolGroups_ReservedNamesAreNotGroups).
const (
	expandGroupAll   = "all"
	expandGroupReset = "reset"
)

// toolGroup is one expandable bundle of MCP tools.
type toolGroup struct {
	// Name is the register*Tools call site in server.go's MCPServer(), in
	// snake_case (registerWorkSessionTools -> "work_session").
	Name string
	// Tools are the tool names that call site registers, sorted.
	Tools []string
}

// toolGroups partitions all 91 non-expand_tools MCP tools. Order matches the
// register*Tools call order in MCPServer() so the catalogue reads the same way
// the server is wired.
//
// Invariants enforced by TestToolGroups_PartitionAllRegisteredTools:
//   - union(toolGroups) == MCPServer().ListTools() minus "expand_tools"
//   - no tool appears in two groups
//
// A newly registered tool therefore fails the build's test gate until it is
// classified here — the same forcing function discipline.MutatingTools uses.
var toolGroups = []toolGroup{
	{Name: "onboarding", Tools: []string{"initial_instructions"}},
	{Name: "context", Tools: []string{"get_today_context", "list_active_repos", "sync_repo"}},
	{Name: "gtd", Tools: []string{
		"add_task", "begin_task", "complete_task", "create_goal", "create_project",
		"delete_task", "get_project", "get_task", "get_upcoming_work", "list_goals",
		"list_projects", "list_tasks", "log_activity", "set_task_status",
		"task_checklist_add_item", "task_checklist_complete", "task_checklist_toggle",
		"update_project", "update_project_status", "update_task",
	}},
	{Name: "decision", Tools: []string{"list_decisions", "log_decision"}},
	{Name: "session", Tools: []string{"mark_next_action_done", "resolve_handoff", "set_session_handoff"}},
	{Name: "knowledge", Tools: []string{"add_knowledge", "list_knowledge", "search_knowledge", "sync_to_notion"}},
	{Name: "knowledge_nav", Tools: []string{"navigate_knowledge", "outline_knowledge"}},
	{Name: "learning", Tools: []string{"create_concept", "get_due_reviews", "submit_review"}},
	{Name: "plan", Tools: []string{"confirm_plan"}},
	{Name: "proposal", Tools: []string{
		"confirm_proposal", "confirm_proposals", "list_pending_proposals",
		"propose_goal", "propose_project",
	}},
	{Name: "health", Tools: []string{"system_health"}},
	{Name: "arch", Tools: []string{"get_project_arch", "upsert_project_arch"}},
	{Name: "status", Tools: []string{"generate_project_status"}},
	{Name: "work_session", Tools: []string{
		"checkpoint_work", "finish_work", "get_active_work", "get_work_session_trace",
		"list_recent_work_sessions", "start_work",
	}},
	{Name: "vision", Tools: []string{
		"add_vision_item", "list_vision_items", "promote_vision_to_task", "update_vision_item",
	}},
	{Name: "playbook", Tools: []string{"list_playbooks"}},
	{Name: "procedural", Tools: []string{"add_procedural", "mark_procedural_used", "query_procedural", "recall"}},
	{Name: "reflection", Tools: []string{
		"analyze_recent_patterns", "generate_reflection", "get_latest_reflection", "list_reflections",
	}},
	{Name: "atom", Tools: []string{"promote_atom_to_knowledge", "search_atoms", "traverse_atoms"}},
	{Name: "outcome", Tools: []string{
		"evaluate_outcome", "find_failed_patterns", "list_recent_outcomes", "record_outcome",
	}},
	{Name: "skill", Tools: []string{
		"extract_skill", "list_relevant_skills", "search_skills",
		"update_skill_from_outcome", "use_skill",
	}},
	{Name: "behavior_rule", Tools: []string{
		"apply_behavior_rules", "deprecate_behavior_rule", "list_behavior_rules", "propose_behavior_rule",
	}},
	{Name: "context_pack", Tools: []string{"assemble_context"}},
	{Name: "dashboard", Tools: []string{"detect_completion_candidates", "reconcile_dashboard"}},
	{Name: "reconcile", Tools: []string{"reconcile_merged_prs"}},
	{Name: "closeout", Tools: []string{"closeout_session_check"}},
	{Name: "watchdog", Tools: []string{"analyze_agent_behavior", "detect_unclosed_loops", "mark_loop_resolved"}},
}

// toolGroupIndex maps a group name to its tool list, built once at init.
var toolGroupIndex = func() map[string][]string {
	m := make(map[string][]string, len(toolGroups))
	for _, g := range toolGroups {
		m[g.Name] = g.Tools
	}
	return m
}()

// toolGroupNames returns every real group name in catalogue order. The two
// reserved verbs ("all", "reset") are NOT included.
func toolGroupNames() []string {
	out := make([]string, 0, len(toolGroups))
	for _, g := range toolGroups {
		out = append(out, g.Name)
	}
	return out
}

// expandToolsGroupEnum is the accepted value set for expand_tools' `group`
// argument: every real group plus the two reserved verbs. Passed straight to
// mcp.Enum() at registration so the seam's validation is derived from the
// advertised schema rather than hand-written a second time.
func expandToolsGroupEnum() []string {
	return append(toolGroupNames(), expandGroupAll, expandGroupReset)
}

// visibleToolSet returns the tool names a session with the given expanded
// groups may see in tools/list: always the core set, plus every tool of every
// expanded group. The reserved verb expandGroupAll unions every group (the
// groups partition the whole registry, so that is exactly "everything").
// Unknown group names are ignored — the enum rejects them at the boundary, but
// the lookup stays total anyway.
func visibleToolSet(expanded map[string]bool) map[string]bool {
	visible := make(map[string]bool, len(coreToolNames)*2)
	for _, n := range coreToolNames {
		visible[n] = true
	}
	if expanded[expandGroupAll] {
		for _, g := range toolGroups {
			for _, n := range g.Tools {
				visible[n] = true
			}
		}
		return visible
	}
	for g := range expanded {
		for _, n := range toolGroupIndex[g] {
			visible[n] = true
		}
	}
	return visible
}
