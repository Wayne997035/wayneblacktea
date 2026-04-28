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

## MANDATORY: When user confirms a plan ("可以","好","go","ok","明天做","start","開始")
Call confirm_plan IMMEDIATELY with ALL phases as tasks and ALL confirmed decisions.
Do NOT call add_task + log_decision separately — confirm_plan handles both atomically and is more reliable.
(Skip if MCP is unavailable or returns an error.)

## MANDATORY: When user confirms a single decision (not a multi-phase plan)
Call log_decision BEFORE starting implementation. Include alternatives considered.
(Skip if MCP is unavailable or returns an error.)

Loggable: bug fix approach, architecture, API design, deployment, third-party service, non-obvious discoveries.

## MANDATORY: After task completion (build pass, tests pass)
Call complete_task with artifact (file path or PR URL).
(Skip if MCP is unavailable or returns an error.)

## Proactive
- New follow-up work discovered → add_task immediately
- User says "tomorrow"/"next time"/"later" → set_session_handoff before responding
- Question about saved knowledge → search_knowledge before fetching/analyzing URLs`

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
