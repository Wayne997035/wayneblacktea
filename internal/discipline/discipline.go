// Package discipline implements the meta-rule drift-detection store: every
// successful mutating MCP tool call is recorded as a discipline_events row,
// and the system_health tool surfaces "drift signals" (mutating events that
// happened without a recent log_decision / confirm_plan in the same session).
//
// This is the portable MCP-native replacement for the cancelled PreToolUse
// hook approach (decision e5310a15) — the same enforcement logic, but
// implemented inside the MCP server so any client (Claude Code, Discord,
// CLI) gets identical drift signals.
//
// TTL is 30 days, enforced via `task discipline-prune` (build/Taskfile.yml).
// Per backend-security-design.md §1.3, this retention policy is mandatory
// for any "every observation" table.
package discipline

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// MutatingTools is the canonical allowlist of MCP tool names that trigger
// is_mutating=true when middleware records a discipline_events row.
//
// Membership is intentionally explicit: any tool not in this map is treated
// as read-only / advisory. New mutating tools MUST be added here in the same
// PR that introduces them so drift detection stays accurate.
var MutatingTools = map[string]bool{
	// GTD task lifecycle
	"add_task":        true,
	"update_task":     true,
	"complete_task":   true,
	"delete_task":     true,
	"set_task_status": true,
	"update_project":  true, // internal/mcp/tools_gtd.go:130/395/420 — UpdateProject
	"log_activity":    true, // tools_gtd.go:238/1074/1091 — LogActivity
	"begin_task":      true, // tools_gtd.go:330/1314/1342+1360 — BeginTask + UpdateTask

	// Task checklist sub-items (tools_gtd.go) — all call AddChecklistItem /
	// UpdateChecklistItem, which are Store writes on the task's checklist column.
	"task_checklist_add_item": true, // tools_gtd.go:270/1211/1251
	"task_checklist_toggle":   true, // tools_gtd.go:282/1261/1283
	"task_checklist_complete": true, // tools_gtd.go:294/1293/1304

	// Plan / proposal confirmation
	"confirm_plan":      true,
	"confirm_proposal":  true,
	"confirm_proposals": true,

	// Decision / knowledge / memory writes
	"log_decision":           true,
	"add_knowledge":          true,
	"add_procedural":         true,
	"add_vision_item":        true,
	"update_vision_item":     true,
	"promote_vision_to_task": true,
	"create_concept":         true,
	"submit_review":          true,

	// Session / handoff
	"set_session_handoff":   true,
	"resolve_handoff":       true,
	"mark_next_action_done": true, // internal/mcp/tools_session.go:32/101/118 — MarkNextActionDone

	// Work session lifecycle
	"start_work":      true,
	"finish_work":     true,
	"checkpoint_work": true,

	// Goals / projects
	"propose_goal":    true,
	"propose_project": true,
	"create_goal":     true,
	"create_project":  true,

	// Procedural / repo / arch / status
	"mark_procedural_used":  true,
	"sync_repo":             true,
	"upsert_project_arch":   true,
	"update_project_status": true,

	// Watchdog meta-cognition (Memory-8) — these tools mutate
	// discipline_events_m8 and are themselves observability writes.
	"analyze_agent_behavior": true,
	"mark_loop_resolved":     true,

	// PR reconciliation — batch-completes pending/in_progress tasks from
	// caller-supplied merged-PR data (up to 200 per call). Must be visible to
	// drift detection: this is a batch mutation with no server-side
	// verification the PR actually merged (round-1 review Finding 1, Critical).
	"reconcile_merged_prs": true, // internal/mcp/tools_reconcile.go:36/95/136 — BatchCompleteTasksByPRMatch

	// Memory atom bridge — promotes a consolidated atom into a pending
	// knowledge proposal and flips the atom's digest_status.
	"promote_atom_to_knowledge": true, // internal/mcp/tools_atom.go:22/253/339+348

	// Behavior rule governance lifecycle.
	"propose_behavior_rule":   true, // tools_behaviorrule.go:16/77/134 — Propose
	"apply_behavior_rules":    true, // tools_behaviorrule.go:49/181/201 — ApplyOutcome
	"deprecate_behavior_rule": true, // tools_behaviorrule.go:64/228/243 — Deprecate

	// Outcome / reflection loop.
	"record_outcome":      true, // tools_outcome.go:55/122/164 — CreateOutcome
	"evaluate_outcome":    true, // tools_outcome.go:81/185/222 — CreateEvaluation
	"generate_reflection": true, // tools_reflection.go:18/70/78 — Create

	// Skill extraction / usage tracking.
	"extract_skill":             true, // tools_skill.go:16/138/183 — Add
	"use_skill":                 true, // tools_skill.go:52/225/238 — IncrementSuccess
	"update_skill_from_outcome": true, // tools_skill.go:62/249/276 — UpdateFromOutcome
}

// DeliberatelyExcludedTools is the explicit, documented allowlist of MCP tool
// names that are registered on the server but intentionally NOT classified as
// mutating for drift-detection purposes, even though some of them do write to
// the database. This is the single source of truth referenced by both this
// package's doc comment and the structural parity test
// (internal/mcp.TestMCPServer_AllRegisteredToolsClassified) — do NOT maintain
// a second copy of this list elsewhere.
//
// Categories:
//   - System-generated cache/candidate writes: these tools persist derived,
//     re-computable observations (fuzzy match candidates, dashboard snapshots,
//     project status snapshots, closeout heuristic checks) rather than
//     authoritative user/agent decisions. A human/agent did not directly
//     "decide" to mutate state — the write is a caching side effect of a read
//     operation, so flagging it as drift would generate noise, not signal.
//   - External-only side effects: tools whose only side effect is calling an
//     external service (Notion) with no local Store row of authoritative
//     record; nothing here for local drift-detection to protect.
//   - Read-only tools: everything else registered on the server that never
//     calls a Store write method.
var DeliberatelyExcludedTools = map[string]bool{
	// System-generated cache/candidate writes.
	"detect_completion_candidates": true,
	"reconcile_dashboard":          true,
	"generate_project_status":      true,
	"closeout_session_check":       true,

	// External-only side effects (no local Store row of record).
	"sync_to_notion": true,

	// Read-only tools — verified (round-2 review remediation) to call only
	// List/Get/Search/Query-style Store methods, never a write. Kept as an
	// explicit allowlist (not just "absent from MutatingTools") so the
	// structural parity test forces a deliberate classification decision for
	// any newly-registered tool instead of silently passing it through.
	"analyze_recent_patterns": true,
	"assemble_context":        true, // tools_contextpack.go — Assembler.Assemble is retrieval-only, no Store writes.
	"detect_unclosed_loops":   true,
	// expand_tools mutates only process-local, per-session tools/list
	// visibility (internal/mcp/tools_expand.go) — no Store, no DB row, nothing
	// a drift signal could protect. Deliberately NOT in MutatingTools: putting
	// it there would demand a preceding log_decision/confirm_plan for what is
	// effectively a catalogue paging call.
	"expand_tools":              true,
	"find_failed_patterns":      true,
	"get_active_work":           true,
	"get_due_reviews":           true,
	"get_latest_reflection":     true,
	"get_project":               true,
	"get_project_arch":          true,
	"get_task":                  true,
	"get_today_context":         true,
	"get_upcoming_work":         true,
	"get_work_session_trace":    true, // tools_worksession.go — GetByID + GetEvidence are read-only.
	"initial_instructions":      true,
	"list_active_repos":         true,
	"list_behavior_rules":       true,
	"list_decisions":            true,
	"list_goals":                true,
	"list_knowledge":            true,
	"list_pending_proposals":    true,
	"list_playbooks":            true,
	"list_projects":             true,
	"list_recent_outcomes":      true,
	"list_recent_work_sessions": true, // tools_worksession.go — ListRecent is read-only.
	"list_reflections":          true,
	"list_relevant_skills":      true,
	"list_tasks":                true,
	"list_vision_items":         true,
	"navigate_knowledge":        true,
	"outline_knowledge":         true,
	"query_procedural":          true,
	"recall":                    true,
	"search_atoms":              true,
	"search_knowledge":          true,
	"search_skills":             true,
	"system_health":             true,
	"traverse_atoms":            true,
}

// IsMutating returns true when toolName is in the canonical MutatingTools set.
// Out-of-set tools (queries, lists, gets) are recorded with is_mutating=false.
func IsMutating(toolName string) bool {
	return MutatingTools[toolName]
}

// Event is the domain model for a single recorded MCP tool invocation.
type Event struct {
	ID               int64      `json:"id"`
	SessionID        string     `json:"session_id"`
	RepoName         string     `json:"repo_name,omitempty"`
	ToolName         string     `json:"tool_name"`
	IsMutating       bool       `json:"is_mutating"`
	ObservedAt       time.Time  `json:"observed_at"`
	LinkedDecisionID *uuid.UUID `json:"linked_decision_id,omitempty"`
	WorkspaceID      *uuid.UUID `json:"workspace_id,omitempty"`
}

// InsertParams holds the fields needed to record a new discipline_events row.
// session_id is required; the others are optional (NULL when zero-valued).
type InsertParams struct {
	SessionID        string
	RepoName         string
	ToolName         string
	IsMutating       bool
	LinkedDecisionID *uuid.UUID
	WorkspaceID      *uuid.UUID
}

// Store is the backend-agnostic contract for the discipline events bounded
// context. Postgres-backed *PgStore and SQLite-backed *SQLiteStore satisfy
// this interface.
type Store interface {
	// Insert records a single discipline event. Errors are returned to the
	// caller; the MCP middleware logs them via slog.Warn and continues.
	Insert(ctx context.Context, p InsertParams) error

	// RecentMutating returns mutating events observed at or after `since`,
	// ordered by observed_at DESC, capped at limit (caller-controlled).
	// Used by system_health to count 24h drift candidates.
	RecentMutating(ctx context.Context, since time.Time, limit int) ([]Event, error)

	// RecentDecisionTimes returns the observed_at timestamps of every
	// log_decision / confirm_plan event in `sessionID` at or after `since`,
	// ordered DESC. Used by drift-detection to know whether a mutating call
	// had a "preceding decision" inside the 15-minute window.
	RecentDecisionTimes(ctx context.Context, sessionID string, since time.Time) ([]time.Time, error)
}
