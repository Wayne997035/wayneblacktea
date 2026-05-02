package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerStatusTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("generate_project_status",
		mcp.WithDescription(
			"Returns a Haiku-generated status snapshot for the given project slug "+
				"(sprint_summary, gap_analysis, sota_catchup_pct, pending_summary). "+
				"Cached for 24 h; use force_refresh=true to regenerate immediately. "+
				"CALL generate_project_status instead of re-reading 100+ decisions manually.",
		),
		mcp.WithString("slug", mcp.Description("Project slug (e.g. 'wayneblacktea')"), mcp.Required()),
		mcp.WithBoolean("force_refresh", mcp.Description("Force regeneration even if a fresh snapshot exists")),
	), s.handleGenerateProjectStatus)
}

func (s *Server) handleGenerateProjectStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	slug := stringArg(args, "slug")
	if slug == "" {
		return mcp.NewToolResultError("slug is required"), nil
	}

	forceRefresh, _ := args["force_refresh"].(bool)

	if s.snapshotStore == nil || s.snapshotGen == nil {
		return mcp.NewToolResultError("snapshot feature not configured (CLAUDE_API_KEY required)"), nil
	}

	wsID := s.workspaceUUID()

	snap, fromCache, err := snapshot.EnsureSnapshot(
		ctx, slug, forceRefresh,
		s.snapshotStore, s.snapshotGen,
		s.decision, s.gtd,
		wsID,
	)
	if err != nil {
		slog.Warn("generate_project_status: failed", "slug", slug, "err", err)
		return mcp.NewToolResultError(fmt.Sprintf("generating status snapshot: %v", err)), nil
	}

	type response struct {
		Slug           string `json:"slug"`
		GeneratedAt    string `json:"generated_at"`
		SprintSummary  string `json:"sprint_summary"`
		GapAnalysis    string `json:"gap_analysis"`
		SotaCatchupPct int    `json:"sota_catchup_pct"`
		PendingSummary string `json:"pending_summary"`
		Source         string `json:"source"`
		FromCache      bool   `json:"from_cache"`
	}

	return jsonText(response{
		Slug:           snap.Slug,
		GeneratedAt:    snap.GeneratedAt.Format("2006-01-02T15:04:05Z"),
		SprintSummary:  snap.SprintSummary,
		GapAnalysis:    snap.GapAnalysis,
		SotaCatchupPct: snap.SotaCatchupPct,
		PendingSummary: snap.PendingSummary,
		Source:         snap.Source,
		FromCache:      fromCache,
	})
}
