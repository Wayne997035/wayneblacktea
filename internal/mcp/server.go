package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/notion"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	wbtruntime "github.com/Wayne997035/wayneblacktea/internal/runtime"
	"github.com/Wayne997035/wayneblacktea/internal/search"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/watchdog"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Server wires all domain stores to MCP tools.
type Server struct {
	pool      *pgxpool.Pool
	gtd       *gtd.Store
	workspace *workspace.Store
	decision  *decision.Store
	session   *session.Store
	knowledge *knowledge.Store
	learning  *learning.Store
	proposal  *proposal.Store
	notion    *notion.Client
	watchdog  *watchdog.Watchdog
}

// New creates a Server connected to the given connection pool. The optional
// WORKSPACE_ID env scopes every domain store; unset = legacy unscoped mode.
func New(pool *pgxpool.Pool) (*Server, error) {
	wsID, err := wbtruntime.WorkspaceIDFromEnv()
	if err != nil {
		return nil, fmt.Errorf("reading WORKSPACE_ID env: %w", err)
	}
	embedClient := search.NewEmbeddingClient()
	return &Server{
		pool:      pool,
		gtd:       gtd.NewStore(pool, wsID),
		workspace: workspace.NewStore(pool, wsID),
		decision:  decision.NewStore(pool, wsID),
		session:   session.NewStore(pool, wsID),
		knowledge: knowledge.NewStore(pool, embedClient, wsID),
		learning:  learning.NewStore(pool, wsID),
		proposal:  proposal.NewStore(pool, wsID),
		notion:    notion.NewClient(),
		watchdog:  watchdog.New(200),
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
    "清掉", "拿掉", "刪", "remove", "drop"

When ANY of these fire on a plan described in the last 3 turns:
Call confirm_plan IMMEDIATELY with ALL phases as tasks and ALL confirmed decisions.
Do NOT call add_task + log_decision separately — confirm_plan handles both atomically and is more reliable.

Critical: when YOU (the assistant) propose options and the user replies with
just a letter or very short pick, that IS a plan confirmation. Treat it as such,
not as casual chat. The assistant proposing + user accepting still requires the
same logging discipline as user proposing.
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
- User says "tomorrow" / "next time" / "later" → set_session_handoff before
  responding
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
