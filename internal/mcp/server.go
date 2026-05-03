package mcp

import (
	"encoding/json"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/arch"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/notion"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/Wayne997035/wayneblacktea/internal/watchdog"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
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
	pool        *pgxpool.Pool
	gtd         gtd.StoreIface
	workspace   workspace.StoreIface
	decision    decision.StoreIface
	session     session.StoreIface
	knowledge   knowledge.StoreIface
	learning    learning.StoreIface
	proposal    proposal.StoreIface
	arch        arch.StoreIface
	workSession worksession.StoreIface

	// pg* are concrete pg-backed Stores (or nil under SQLite) used by
	// acceptProposal to call WithTx(tx). Add new tx-typed code paths
	// reluctantly — see ServerStores doc comment for the migration plan.
	pgGTD      *gtd.Store
	pgProposal *proposal.Store
	pgLearning *learning.Store

	notion     *notion.Client
	watchdog   *watchdog.Watchdog
	classifier *ai.ActivityClassifier

	// snapshotStore / snapshotGen are optional; nil = feature disabled.
	// Populated via WithSnapshot when CLAUDE_API_KEY is set.
	snapshotStore snapshot.StoreIface
	snapshotGen   snapshot.GeneratorIface

	// workspaceID is populated from WORKSPACE_ID env at New time for use by
	// tools that need to scope snapshot writes without a pgxpool reference.
	workspaceID *uuid.UUID
}

// New creates a Server backed by the given pre-built ServerStores bundle.
// The bundle is responsible for the workspace-id scoping and the underlying
// connection lifecycle; cmd/mcp/main.go MUST defer stores.Close() after this
// call returns.
func New(stores storage.ServerStores) (*Server, error) {
	wsID := stores.WorkspaceID()
	return &Server{
		pool:        stores.PgxPool(),
		gtd:         stores.GTD(),
		workspace:   stores.Workspace(),
		decision:    stores.Decision(),
		session:     stores.Session(),
		knowledge:   stores.Knowledge(),
		learning:    stores.Learning(),
		proposal:    stores.Proposal(),
		arch:        stores.Arch(),
		workSession: stores.WorkSession(),
		pgGTD:       stores.PgGTD(),
		pgProposal:  stores.PgProposal(),
		pgLearning:  stores.PgLearning(),
		notion:      notion.NewClient(),
		watchdog:    watchdog.New(200),
		workspaceID: wsID,
	}, nil
}

// WithSnapshot wires a snapshot store and generator into the server so that
// the generate_project_status MCP tool is available. Passing nil store or gen
// is valid and disables the feature (e.g. when CLAUDE_API_KEY is not set).
func (s *Server) WithSnapshot(store snapshot.StoreIface, gen snapshot.GeneratorIface) *Server {
	s.snapshotStore = store
	s.snapshotGen = gen
	return s
}

// workspaceUUID returns the workspace UUID pointer for use in snapshot writes.
func (s *Server) workspaceUUID() *uuid.UUID {
	return s.workspaceID
}

// WithClassifier wires an ActivityClassifier into the server so that
// significant MCP tool calls are automatically classified for implicit
// decisions and follow-up tasks. Passing nil is valid and disables
// auto-classification (e.g. when CLAUDE_API_KEY is not set).
func (s *Server) WithClassifier(clf *ai.ActivityClassifier) *Server {
	s.classifier = clf
	return s
}

const mcpInstructions = `WAYNEBLACKTEA PERSONAL OS — USAGE PROTOCOL

## Session Start
Call get_today_context first. If there is a pending handoff, resolve_handoff after reading it.

## Architecture / past decisions
Call list_decisions with the relevant repo_name before answering. Always verify in code too.

## Tool routing

| Situation | Tool |
|-----------|------|
| Multi-phase plan confirmed ("好"/"go"/"衝"/"開始"/option pick) | confirm_plan — ALL phases + decisions atomically |
| User confirms a single decision | log_decision BEFORE implementation |
| **Start a task** (dispatch agent OR begin Lead-direct) | **update_task → in_progress immediately, no user reminder** |
| Build passes / PR merged / task done | complete_task with artifact URL |
| "收工" / "下班" / "later" / "good night" | set_session_handoff (Stop hook also fires; call this too for richer context) |
| Scope/priority changes mid-session | log_decision + update_task + set_session_handoff |
| New follow-up discovered | add_task immediately |
| Question about saved knowledge | search_knowledge first |

## confirm_plan triggers
"可以" "好" "OK" "yes" "go" "對" "衝" "開工" "開始" "執行" "按這個" — or any single letter/number picking from a list the assistant just proposed.
After confirm_plan fires: assess each task immediately — Lead-direct tasks start now,
complex tasks dispatch engineer now. Both in parallel. **At dispatch (or Lead-direct start),
MUST call update_task → in_progress for every task being worked. No user prompt needed.**

## update_task triggers (in_progress)
Mandatory the moment work begins on a task — dispatch engineer/codex/frontend-engineer,
or Lead-direct execution starts. For multi-phase plans just created via confirm_plan,
mark every phase task being worked in parallel as in_progress. Skipping this is a process
bug; user should not have to remind. Pair with complete_task at finish.

## log_decision scope
Architecture, API design, deployment config, third-party service choice, scope pivot,
mid-session course correction, rule-source changes (CLAUDE.md / mcpInstructions / .mcp.json).

## complete_task triggers
"好了" "搞定" "done" "ship it" "looks good" "讚" "漂亮" — only when a task is currently in_progress and the assistant just reported completion.

## Note on auto-logging
High-signal tools (complete_task, confirm_plan, add_task, log_decision, set_session_handoff)
are auto-logged server-side. Stop hook auto-creates a session snapshot. These tools are still
required — auto-log is a safety net, not a replacement.

## Architecture snapshots
After reading 3+ internal/ files from a project, MUST call upsert_project_arch to store the
architecture snapshot (slug = repo name, summary = one-paragraph description, file_map = path→purpose).
At session start, call get_project_arch first — if stale (last_commit_sha differs from git rev-parse HEAD),
re-read changed files and call upsert_project_arch again.

## Behavior rules live here, not in private memory
Do not store wayneblacktea protocol rules in agent memory. Rules ship with the binary so
all MCP clients get identical behavior. To change a rule, propose a PR to internal/mcp/server.go.`

// MCPServer returns a configured MCP server with all tools registered.
//
// The watchdog middleware records every tool invocation in process memory so
// the system_health tool can surface "stuck" patterns (Claude updated a task
// to in_progress but never called complete_task, etc.).
func (s *Server) MCPServer() *server.MCPServer {
	ms := server.NewMCPServer("wayneblacktea", "0.1.0",
		server.WithInstructions(mcpInstructions),
		server.WithToolHandlerMiddleware(s.watchdog.Middleware()),
		server.WithToolHandlerMiddleware(s.autoLogMiddleware()),
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
	s.registerArchTools(ms)
	s.registerStatusTools(ms)
	s.registerWorkSessionTools(ms)
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
