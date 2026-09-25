package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// statusSlugMaxLen is the slug length limit at the generate_project_status
// MCP boundary. Slug flows into a Haiku prompt (see snapshot/generator.go); a
// free-text slug containing newlines or "[END UNTRUSTED]" could break out of
// the boundary block and inject instructions into the model context
// (security audit C-2). The character gate is the workspace repo name rule
// (validator.ValidRepoPathMax): every boundary marker in
// internal/safetext/boundary_markers.go needs '=', '[' or whitespace, and
// the rule admits none of them.
const statusSlugMaxLen = 64

// statusSlugMessage is the rejection text for a slug outside the rule.
var statusSlugMessage = fmt.Sprintf("slug must be at most %d characters: %s",
	statusSlugMaxLen, validator.RepoPathSegmentRule)

// statusFieldMaxRunes bounds SprintSummary/GapAnalysis/PendingSummary on
// read — U13. These are Haiku-generated but
// cached in the snapshot store and read back on subsequent calls
// (from_cache=true) — the U13 inventory classifies this as "stored on
// behalf of an LLM, read back into an LLM context" even though the content
// is AI-summarized rather than verbatim: a forged marker in a source
// decision's rationale could still survive summarization verbatim. Sized
// like wrapUntrustedTask's gtdBodyMaxRunes (tools_gtd.go).
const statusFieldMaxRunes = gtdBodyMaxRunes

func (s *Server) registerStatusTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"generate_project_status",
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
	// Reject slugs that could break out of the [BEGIN UNTRUSTED] boundary
	// in the Haiku snapshot prompt. Same segment rule as upsert_project_arch's
	// validateArchSlug (tools_arch.go) and every repo_name column
	// ([F0925-29]). The length cap differs on purpose: 64 here is this
	// tool's own Haiku-prompt budget, not upsert_project_arch's maxSlugLen=128.
	if !validator.ValidRepoPathMax(slug, statusSlugMaxLen) {
		return mcp.NewToolResultError(statusSlugMessage), nil
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
		return storeErrorResult("generating status snapshot", err), nil
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
		SprintSummary:  clipSafe(snap.SprintSummary, statusFieldMaxRunes),
		GapAnalysis:    clipSafe(snap.GapAnalysis, statusFieldMaxRunes),
		SotaCatchupPct: snap.SotaCatchupPct,
		PendingSummary: clipSafe(snap.PendingSummary, statusFieldMaxRunes),
		Source:         snap.Source,
		FromCache:      fromCache,
	})
}
