package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerOnboardingTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("initial_instructions",
		mcp.WithDescription(
			"Returns the complete usage protocol for this MCP server. "+
				"Call at session start after get_today_context for full workflow guidance.",
		),
	), s.handleInitialInstructions)
}

func (s *Server) handleInitialInstructions(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText(mcpInstructions), nil
}
