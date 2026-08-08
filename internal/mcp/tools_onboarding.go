package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// mcpProtocolFull is what initial_instructions returns: the injected core
// protocol plus everything that was too bulky to put on the initialize path.
//
// It is a CONCATENATION EXPRESSION on purpose. An earlier shape had
// initial_instructions return its own literal copy of the protocol; two
// literals holding "the same" rules is a guaranteed drift bug the moment one
// side is edited. Deriving the full text from mcpInstructions makes drift
// structurally impossible, and TestHandleInitialInstructions_ReturnsProtocolFull
// pins the derivation (prefix, suffix and strictly-greater length).
const mcpProtocolFull = mcpInstructions + "\n\n" + mcpProtocolAppendix

// mcpProtocolAppendix holds the on-demand half of the protocol: the routing
// rows, trigger vocabularies and per-tool guidance that do not fit the
// initialize-path budget. Rules here are NOT optional — they are the same
// protocol, just paid for only by clients that ask.
const mcpProtocolAppendix = `## APPENDIX — full routing table

Memory / knowledge routing (not in the injected core protocol):
- Scope or priority changes mid-session -> log_decision + update_task + set_session_handoff
- "Before complex task" / looking for relevant patterns -> list_playbooks (procedural rules distilled from past decisions)
- Recording a new how-to or reusable approach from this session -> add_procedural (title, when_to_use, approach_md)
- Retrieving a how-to / procedural approach -> query_procedural with keywords
- Marking a procedural memory as successfully used -> mark_procedural_used with id
- Cross-type memory search (episodic + semantic + procedural) -> recall with query

## MANDATORY GTD DISCIPLINE — triggers

Before dispatching any engineer/agent OR starting any Lead-direct implementation:
-> MUST call update_task(task_id, status="in_progress") for EVERY task being worked.

When a task is done (build passes, PR merged, or Lead-direct commit pushed):
-> MUST call complete_task(task_id, artifact="<PR URL or commit SHA>") immediately.

NEVER ask "should I update the GTD?" — just do it. Missing these calls = process bug.
- "dispatch engineer" -> update_task in_progress first
- "PR merged" -> complete_task with the PR URL
- "commit SHA" -> complete_task with the SHA
- "task check passes" -> complete_task if that was the acceptance criterion

## update_task triggers (in_progress)

Mandatory the moment work begins on a task — dispatching engineer / codex /
frontend-engineer, or Lead-direct execution starting. For a multi-phase plan
just created via confirm_plan, mark every phase task being worked in parallel
as in_progress. Skipping this is a process bug; the user should not have to
remind. Pair with complete_task at finish.

## Note on auto-logging

The high-signal tools (complete_task, confirm_plan, add_task, log_decision,
set_session_handoff) are auto-logged server-side, and the Stop hook creates a
session snapshot on its own. Those tools are still required: auto-log is a
safety net, not a replacement.

## Trigger vocabularies

confirm_plan: "可以" "好" "OK" "yes" "go" "對" "衝" "開工" "開始" "執行" "按這個"
— or any single letter/number picking from a list the assistant just proposed.
After confirm_plan fires, assess each task immediately: Lead-direct tasks start
now, complex tasks dispatch an engineer now, both in parallel, and every task
being worked gets update_task -> in_progress at that moment.

complete_task: "好了" "搞定" "done" "ship it" "looks good" "讚" "漂亮" — only
when the task is currently in_progress and the assistant just reported
completion.

set_session_handoff: "收工" "下班" "later" "good night". The Stop hook also
fires; call the tool anyway for richer context.

add_vision_item: "未來想做" "之後再說" "現在還不能" "等 X 完成才能做" "記一下以後".

## Per-tool detail

confirm_plan — on this server's two shipped backends (Postgres, SQLite) every
phase task and every decision commits together or none do, in one transaction.
Two things are NOT covered by that guarantee: (1) a server run with neither
backend wired falls back to a non-atomic sequential path (not a real deployment
configuration); (2) the in_progress work_session linking the phase tasks is
created separately, best-effort, AFTER the transaction commits — a work-session
failure never rolls back the already-committed tasks/decisions. ALWAYS read the
response text / is_error instead of assuming success: a success response lists
every task and decision actually created, and a failure response states exactly
what was written. A failure whose message says OUTCOME UNKNOWN means the plan
MAY already be stored — do NOT re-send it; call list_tasks / list_decisions
first and retry only what is genuinely missing.
  - phases: JSON array, each {"title":"...","description":"...","priority":2}
  - decisions: JSON array, each
    {"title":"...","context":"...","decision":"...","rationale":"...","alternatives":""}
  - assignee: the canonical actor confirming/executing the plan (claude, codex,
    human, or a recognised alias). Stamped onto any phase task that has no
    assignee when it flips to in_progress via the created work session. A phase
    task with neither an existing assignee nor this value stays pending instead
    of flipping (P6.8 assignee gate).

record_outcome — closes the Action->Result loop by capturing what actually
happened. entity_type is one of task, decision, sprint, project; result is one
of success, failure, partial, unknown, regressed. Optionally link behavior
rules via related_rule_ids so the governance scheduler can update their
confidence scores.
  - notes: max 500 runes per call. Notes ACCUMULATE across repeated calls
    against the same open draft (result="unknown") up to a cumulative cap.
    Once the cap is reached, further notes text is NOT written and the response
    carries notes_truncated=true — the write still succeeds, only the new text
    is dropped; existing notes are never lost.
  - related_rule_ids: JSON array of behavior rule UUIDs, max 20 per call. IDs
    also ACCUMULATE across calls against the same open draft up to a cumulative
    cap; past it, new IDs are not added and the response carries
    related_rule_ids_truncated=true. Existing links are never lost.
  - metrics_json: optional JSON object of numeric metrics, e.g. {"duration_ms": 1200}
  - session_id: optional work session UUID this outcome was recorded from —
    best-effort linked back onto the session via SetOutcomeLink.

add_task — call the moment follow-up work is identified during discussion.
  - due_date is required, RFC3339 (e.g. 2026-12-31T00:00:00Z)
  - priority 1-5 is execution order, lower runs first; importance 1-3
    (1=high, 2=med, 3=low) is a DIFFERENT axis and both are worth setting
  - context is free-form background: why this task came up
  - kind: general (default), fix-pr, feature, refactor, research, chore
  - assignee is validated against the canonical actor allowlist (claude, codex,
    human, or a recognised alias), not by length

update_task — updates one or more mutable fields; everything except task_id is
optional and omitted fields keep their existing value. Use complete_task, not
update_task, to mark a task completed. status accepts pending, in_progress or
cancelled. branch_name and pr_url accept an empty string to clear the field.

complete_task — if artifact is a GitHub PR URL (https://github.com/.../pull/N)
it is also stored as pr_url; if it is a 40-character hex SHA it is appended to
commit_shas.

expand_tools — tools/list advertises the core set only, to keep the connect-time
context cost low. Call expand_tools with no argument for the group catalogue,
then group="<name>" to reveal one group (the response carries the full JSON
schema of every newly revealed tool), group="all" for everything, or
group="reset" to go back to the core set. Hiding is a catalogue-level
concern only: every tool on this server is callable by name at any time,
whether or not it currently appears in tools/list.`

func (s *Server) registerOnboardingTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"initial_instructions",
		mcp.WithDescription(
			"Returns the complete usage protocol for this MCP server. "+
				"Call at session start after get_today_context for full workflow guidance.",
		),
	), s.handleInitialInstructions)
}

func (s *Server) handleInitialInstructions(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(mcpProtocolFull), nil
}
