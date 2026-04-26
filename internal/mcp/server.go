package mcp

import (
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/waynechen/wayneblacktea/internal/decision"
	"github.com/waynechen/wayneblacktea/internal/gtd"
	"github.com/waynechen/wayneblacktea/internal/knowledge"
	"github.com/waynechen/wayneblacktea/internal/learning"
	"github.com/waynechen/wayneblacktea/internal/notion"
	"github.com/waynechen/wayneblacktea/internal/search"
	"github.com/waynechen/wayneblacktea/internal/session"
	"github.com/waynechen/wayneblacktea/internal/workspace"
)

// Server wires all domain stores to MCP tools.
type Server struct {
	gtd       *gtd.Store
	workspace *workspace.Store
	decision  *decision.Store
	session   *session.Store
	knowledge *knowledge.Store
	learning  *learning.Store
	notion    *notion.Client
}

// New creates a Server connected to the given connection pool.
func New(pool *pgxpool.Pool) *Server {
	embedClient := search.NewEmbeddingClient()
	return &Server{
		gtd:       gtd.NewStore(pool),
		workspace: workspace.NewStore(pool),
		decision:  decision.NewStore(pool),
		session:   session.NewStore(pool),
		knowledge: knowledge.NewStore(pool, embedClient),
		learning:  learning.NewStore(pool),
		notion:    notion.NewClient(),
	}
}

const mcpInstructions = `WAYNEBLACKTEA PERSONAL OS — USAGE PROTOCOL

## MANDATORY: Session Start
Call get_today_context at the start of EVERY session before doing anything else.
(Skip if MCP is unavailable or returns an error.)

## RECOMMENDED: When the question involves architecture, history, or past decisions
Call list_decisions first with the relevant repo_name. If empty or MCP returns an error, inspect code regardless.
list_decisions returning empty does NOT mean skip code inspection — always verify in code after checking DB.

## MANDATORY: When user confirms a decision ("好啊", "go", "開始", "start")
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
func (s *Server) MCPServer() *server.MCPServer {
	ms := server.NewMCPServer("wayneblacktea", "0.1.0",
		server.WithInstructions(mcpInstructions),
	)
	s.registerOnboardingTools(ms)
	s.registerContextTools(ms)
	s.registerGTDTools(ms)
	s.registerDecisionTools(ms)
	s.registerSessionTools(ms)
	s.registerKnowledgeTools(ms)
	s.registerLearningTools(ms)
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
