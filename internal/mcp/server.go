package mcp

import (
	"encoding/json"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/notion"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/Wayne997035/wayneblacktea/internal/watchdog"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Server wires all domain stores to MCP tools.
//
// Domain stores are held as backend-agnostic StoreIface values so the same
// Server runs against either Postgres or SQLite. The pgxpool.Pool plus the
// concrete pg* fields are populated only on the Postgres bundle and used
// exclusively by acceptProposal (the only flow that needs a pgx-typed
// transaction across multiple stores). On SQLite they are nil and the flow
// falls back to a sequential best-effort path.
type Server struct {
	pool      *pgxpool.Pool
	gtd       gtd.StoreIface
	workspace workspace.StoreIface
	decision  decision.StoreIface
	session   session.StoreIface
	knowledge knowledge.StoreIface
	learning  learning.StoreIface
	proposal  proposal.StoreIface

	// pg* are concrete pg-backed Stores (or nil under SQLite) used by
	// acceptProposal to call WithTx(tx). Add new tx-typed code paths
	// reluctantly — see ServerStores doc comment for the migration plan.
	pgGTD      *gtd.Store
	pgProposal *proposal.Store
	pgLearning *learning.Store

	notion   *notion.Client
	watchdog *watchdog.Watchdog
}

// New creates a Server backed by the given pre-built ServerStores bundle.
// The bundle is responsible for the workspace-id scoping and the underlying
// connection lifecycle; cmd/mcp/main.go MUST defer stores.Close() after this
// call returns.
func New(stores storage.ServerStores) (*Server, error) {
	return &Server{
		pool:       stores.PgxPool(),
		gtd:        stores.GTD(),
		workspace:  stores.Workspace(),
		decision:   stores.Decision(),
		session:    stores.Session(),
		knowledge:  stores.Knowledge(),
		learning:   stores.Learning(),
		proposal:   stores.Proposal(),
		pgGTD:      stores.PgGTD(),
		pgProposal: stores.PgProposal(),
		pgLearning: stores.PgLearning(),
		notion:     notion.NewClient(),
		watchdog:   watchdog.New(200),
	}, nil
}

const mcpInstructions = `WAYNEBLACKTEA PERSONAL OS — USAGE PROTOCOL

## MANDATORY: Session Start
Call get_today_context at the start of EVERY session before doing anything else.
(Skip if MCP is unavailable or returns an error.)

## RECOMMENDED: When the question involves architecture, history, or past decisions
Call list_decisions first with the relevant repo_name. If empty or MCP returns an error, inspect code regardless.
list_decisions returning empty does NOT mean skip code inspection — always verify in code after checking DB.

## MANDATORY: When user confirms a plan
Trigger phrases (any of these counts as confirmation):
  - Affirmative words: "可以", "好", "好啊", "好的", "OK", "ok", "yes", "go", "對", "嗯", "行"
  - Single-letter / single-number option picks ("A", "B", "C", "D", "1", "2", "3", "4")
    when the assistant just listed lettered or numbered options
  - Schedule shifts: "明天", "明天做", "下次", "tomorrow", "later", "next time",
    "提早", "提前", "改天", "等到", "現在開始", "提早開始"
  - Action verbs: "start", "開始", "執行", "do it", "繼續",
    "那就…", "先做…", "改成…", "改…",
    "清掉", "拿掉", "刪", "remove", "drop",
    "開工", "動工", "衝", "按照你列出的", "按這個順序"

When ANY of these fire on a plan described in the last 3 turns:
Call confirm_plan IMMEDIATELY with ALL phases as tasks and ALL confirmed decisions.
Do NOT call add_task + log_decision separately — confirm_plan handles both atomically and is more reliable.

Critical: when YOU (the assistant) propose options and the user replies with
just a letter or very short pick, that IS a plan confirmation. Treat it as such,
not as casual chat. The assistant proposing + user accepting still requires the
same logging discipline as user proposing.

Anti-rationalization: tasks already existing in GTD does NOT exempt from calling
confirm_plan or log_decision. Confirming execution order IS a decision worth
logging. "Tasks exist → no need to log" is a failure mode, not a valid exception.
(Skip if MCP is unavailable or returns an error.)

## MANDATORY: After plan confirmation — immediate dispatch assessment
After confirm_plan or log_decision fires, Lead MUST immediately assess each task:
  - Lead-direct (< 5 files, 1-line change, no new logic): start doing NOW
  - Needs dispatch: invoke feature/fix skill and dispatch engineer + codex NOW

Do NOT wait for the user to say "and dispatch the agents" — that is always implied.
Both can happen in parallel: Lead-direct tasks + dispatching agents for complex ones.
Failure mode: Lead does CI-1 itself, then stops and waits. That is wrong.
(Skip if MCP is unavailable or returns an error.)

## MANDATORY: When user confirms a single decision (not a multi-phase plan)
Call log_decision BEFORE starting implementation. Include alternatives considered.
This applies equally whether the decision originated from the user OR was
proposed by the assistant and accepted.
(Skip if MCP is unavailable or returns an error.)

Loggable: bug fix approach, architecture, API design, deployment, third-party
service, non-obvious discoveries, schedule/scope pivots, dispatch strategy
changes, mid-conversation course corrections, rule-source edits (CLAUDE.md /
private memory / this mcpInstructions / .mcp.json — see Meta-rule section
below).

## MANDATORY: Meta-rule edits ARE loggable decisions
Editing the rule-sources themselves — CLAUDE.md, private agent memory, this
mcpInstructions text, or moving a rule between these sources (single-source-
of-truth re-assignments) — counts as a loggable architectural decision.

Call log_decision BEFORE running Edit / Write / rm on:
  - Any CLAUDE.md (global or per-project)
  - Files under ~/.claude/projects/*/memory/
  - internal/mcp/server.go mcpInstructions constant
  - .mcp.json or any MCP server configuration

Failure mode this guards against: treating "cleanup duplicate rules" as a
chore (no log) when it is actually a single-source-of-truth re-assignment
that affects every future session's behavior. The audit trail matters more,
not less, when the change is to the rule-source itself.
(Skip if MCP is unavailable or returns an error.)

## MANDATORY: When schedule, scope, or strategy changes mid-conversation
Examples that trigger this:
  - A blocker forces a feature to be deferred (quota exhausted, dependency
    missing, infra unavailable)
  - User changes priority order
  - Original plan abandoned for an alternative
  - Dispatch strategy shifts (skill flow vs hand-code, async vs sync, parallel
    vs sequential)

The instant the new direction is agreed:
  1. log_decision (include the abandoned alternatives + why)
  2. update_task on every task whose status or scope shifted
  3. set_session_handoff if the work now spans into a future session
  4. resolve_handoff on the previous handoff if it is now stale

Do all four BEFORE the next action. Never "I'll log it after I finish this
command" — that is the failure mode that makes the memory system unreliable.
(Skip if MCP is unavailable or returns an error.)

## MANDATORY: When user signals task completion (conversational)
Distinct from the build-pass / tests-pass trigger below — this fires on
conversational acknowledgement that work the assistant just reported as
shipped is accepted, even when there is no fresh build/test artifact in the
same turn.

Trigger phrases (any of these, when said about a task currently in_progress):
  - 中文: "好了", "可以了", "沒問題了", "OK 了", "ok 了", "搞定", "完成"
  - English: "done", "looks good", "ship it", "all set"
  - 帶肯定情緒（在 ship 報告之後出現視為對該任務的接收）:
    "不錯", "不錯耶", "漂亮", "讚", "nice", "perfect"

Trigger condition:
  - Only fires on a task whose status is currently in_progress.
  - If no task is in_progress, treat the phrase as casual chat — do NOT call
    complete_task speculatively.
  - If multiple in_progress tasks could plausibly match, ASK the user which
    one before calling complete_task. Never guess.
  - Bare 好 / OK / ok / yes (without 了 suffix) is ambiguous between plan
    confirmation (line 71 confirm_plan) and task completion. Priority rule:
    if a task is in_progress AND the assistant's last turn reported
    completion (artifact / PR URL / "done" summary for that task), treat
    the bare phrase as task acceptance under this section. Otherwise
    default to plan confirmation under confirm_plan.

Do NOT auto-fire if the build / tests have not yet passed for the work in
question — wait for green and apply the existing "After task completion"
rule below instead. This conversational rule is for the case where the
build/test cycle has already completed in an earlier turn and the user is
now verbally accepting the result.

Call complete_task with artifact (file path or PR URL) when the trigger
fires.
(Skip if MCP is unavailable or returns an error.)

## MANDATORY: Session-end auto-handoff
When the user signals that the session is ending — even casually — persist
continuation context BEFORE responding. Without this, the next session
rebuilds from zero and progress is lost.

Trigger phrases (any of these):
  - 中文: "收工", "下班", "下次再說", "改天", "明天再", "休息",
    "晚一點繼續", "先這樣", "掰"
  - English: "tomorrow", "later", "next time", "signing off", "call it",
    "wrap up", "good night", "ttyl"

Disambiguation with confirm_plan schedule-shift triggers (line 74-75):
Several phrases overlap with confirm_plan's "Schedule shifts" category
("明天", "tomorrow", "later", "next time", "改天", "下次"). Priority rule:
  - If the phrase appears in the context of confirming a previously-proposed
    plan (assistant just listed options or proposed scheduling in the last 3
    turns), treat as confirm_plan trigger.
  - Otherwise treat as session-end signal.
  - When ambiguous, ASK the user before persisting handoff — do NOT silently
    fire either tool.

Call set_session_handoff with:
  - intent: a SPECIFIC continuation point — file paths to resume editing,
    task IDs to pick up, pending decisions awaiting user input. NEVER vague
    placeholders like "繼續開發" or "keep going".
  - context_summary: today's last 3-5 decision id+title, currently
    in_progress tasks, and any outstanding questions awaiting answer.

Failure mode this guards against: user says "收工", assistant replies
"好的晚安" without persisting → next session has no handoff record → progress
is reconstructed from scratch and decisions are lost. The handoff write MUST
happen before the closing reply, not after.
(Skip if MCP is unavailable or returns an error.)

## MANDATORY: After task completion (build pass, tests pass)
Call complete_task with artifact (file path or PR URL).
(Skip if MCP is unavailable or returns an error.)

## MANDATORY: When an MCP tool call returns an error
If a wayneblacktea tool returns a validation or argument error:
  1. Read the error message verbatim.
  2. Re-issue the call with corrected arguments.
  3. Do NOT silently move on — silent failures equal unrecorded decisions,
     which breaks the memory system for both the current session and future
     ones.

Common pitfalls observed in practice:
  - Putting field values inside angle-bracket pseudo-XML *inside* a single
    parameter (e.g. an "intent" arg containing literal "</intent><context>…").
    Each field MUST be passed as a separate parameter.
  - Forgetting required fields on log_decision (title, context, decision,
    rationale are all REQUIRED — missing any → reject).

## Anti-pattern: Private per-agent memory for wayneblacktea behavior
Do NOT store wayneblacktea-specific behavior rules (trigger phrases, when to
log, how to log, what counts as a decision) in your private agent memory.
Those rules belong in THIS instructions text and ship with the server binary
so every consumer — current Claude, future Claude versions, Codex, any other
MCP-speaking AI, and friends self-hosting wayneblacktea — gets the same
behavior. Private memory is fine for personal preferences and global
cross-project rules; it is the wrong place for project-specific protocol that
must stay consistent across users and machines.

If you find yourself wanting to write a memory like "remember to call
log_decision when user picks an option", that signals THIS file needs an
update — propose a PR to wayneblacktea/internal/mcp/server.go instead.

## Proactive
- New follow-up work discovered → add_task immediately
- Session-end signals are covered by the dedicated MANDATORY section above
  (Session-end auto-handoff)
- Question about saved knowledge → search_knowledge before fetching or
  analysing URLs`

// MCPServer returns a configured MCP server with all tools registered.
//
// The watchdog middleware records every tool invocation in process memory so
// the system_health tool can surface "stuck" patterns (Claude updated a task
// to in_progress but never called complete_task, etc.).
func (s *Server) MCPServer() *server.MCPServer {
	ms := server.NewMCPServer("wayneblacktea", "0.1.0",
		server.WithInstructions(mcpInstructions),
		server.WithToolHandlerMiddleware(s.watchdog.Middleware()),
	)
	s.registerOnboardingTools(ms)
	s.registerContextTools(ms)
	s.registerGTDTools(ms)
	s.registerDecisionTools(ms)
	s.registerSessionTools(ms)
	s.registerKnowledgeTools(ms)
	s.registerLearningTools(ms)
	s.registerPlanTools(ms)
	s.registerProposalTools(ms)
	s.registerHealthTools(ms)
	return ms
}

// stringArg extracts a string argument from MCP tool arguments.
func stringArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

// numberArg extracts a float64 argument and returns it as int32.
func numberArg(args map[string]any, key string) int32 {
	v, _ := args[key].(float64)
	return int32(v)
}

// floatArg extracts a float64 argument from MCP tool arguments.
func floatArg(args map[string]any, key string) float64 {
	v, _ := args[key].(float64)
	return v
}

// jsonText marshals v to indented JSON and returns a tool result text.
func jsonText(v any) (*mcp.CallToolResult, error) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError("marshaling response"), nil
	}
	return mcp.NewToolResultText(string(out)), nil
}
