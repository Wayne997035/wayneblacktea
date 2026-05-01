package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Wayne997035/wayneblacktea/internal/arch"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	maxSlugLen    = 128
	maxSummaryLen = 8000
	maxFileMapRaw = 128 * 1024 // 128 KB
)

func (s *Server) registerArchTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("upsert_project_arch",
		mcp.WithDescription(
			"Store or refresh the architecture snapshot for a project. "+
				"Call after reading 3+ internal/ files from a project. "+
				"slug is the repo name (e.g. \"wayneblacktea\"), "+
				"summary is a one-paragraph human-readable architecture description, "+
				"file_map is a JSON object mapping file paths to their purpose, "+
				"last_commit_sha is the current HEAD SHA for staleness detection.",
		),
		mcp.WithString("slug", mcp.Description("Repository/project identifier (unique key)"), mcp.Required()),
		mcp.WithString("summary", mcp.Description("Human-readable architecture description"), mcp.Required()),
		mcp.WithString("file_map", mcp.Description(`JSON object mapping file path to purpose`)),
		mcp.WithString("last_commit_sha", mcp.Description("Current git HEAD SHA (run git rev-parse HEAD)")),
	), s.handleUpsertProjectArch)

	ms.AddTool(mcp.NewTool("get_project_arch",
		mcp.WithDescription(
			"Retrieve the stored architecture snapshot for a project. "+
				"Returns the snapshot with a stale field; compare last_commit_sha "+
				"with `git rev-parse HEAD` to determine if the snapshot is up to date. "+
				"Returns an error when no snapshot has been stored yet.",
		),
		mcp.WithString("slug", mcp.Description("Repository/project identifier"), mcp.Required()),
	), s.handleGetProjectArch)
}

func (s *Server) handleUpsertProjectArch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	slug := stringArg(args, "slug")
	if slug == "" {
		return mcp.NewToolResultError("slug is required"), nil
	}

	summary := stringArg(args, "summary")
	if summary == "" {
		return mcp.NewToolResultError("summary is required"), nil
	}

	if len(slug) > maxSlugLen {
		return mcp.NewToolResultError(fmt.Sprintf("slug too long (max %d chars)", maxSlugLen)), nil
	}
	if len(summary) > maxSummaryLen {
		return mcp.NewToolResultError(fmt.Sprintf("summary too long (max %d chars)", maxSummaryLen)), nil
	}
	rawFileMap := stringArg(args, "file_map")
	if len(rawFileMap) > maxFileMapRaw {
		return mcp.NewToolResultError(fmt.Sprintf("file_map too large (max %d bytes)", maxFileMapRaw)), nil
	}

	fileMap := map[string]string{}
	if rawFileMap != "" {
		if err := json.Unmarshal([]byte(rawFileMap), &fileMap); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("file_map must be a valid JSON object: %v", err)), nil
		}
	}

	snap, err := s.arch.UpsertSnapshot(ctx, arch.UpsertParams{
		Slug:          slug,
		Summary:       summary,
		FileMap:       fileMap,
		LastCommitSHA: stringArg(args, "last_commit_sha"),
	})
	if err != nil {
		slog.Error("upsert_project_arch: store error", "err", err)
		return mcp.NewToolResultError("failed to store architecture snapshot"), nil
	}

	return jsonText(snap)
}

func (s *Server) handleGetProjectArch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	slug := stringArg(args, "slug")
	if slug == "" {
		return mcp.NewToolResultError("slug is required"), nil
	}

	snap, err := s.arch.GetSnapshot(ctx, slug)
	if err != nil {
		if errors.Is(err, arch.ErrNotFound) {
			return mcp.NewToolResultError(fmt.Sprintf("no architecture snapshot found for %q — call upsert_project_arch first", slug)), nil
		}
		slog.Error("get_project_arch: store error", "err", err)
		return mcp.NewToolResultError("failed to retrieve architecture snapshot"), nil
	}

	// stale field is set false by the store; callers should compare
	// snap.last_commit_sha with `git rev-parse HEAD` themselves.
	snap.Stale = false

	return jsonText(snap)
}
