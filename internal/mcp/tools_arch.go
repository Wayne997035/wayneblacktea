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
	ms.AddTool(mcp.NewTool(
		"upsert_project_arch",
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
		mcp.WithString("file_map", mcp.Description(
			`JSON object mapping file path to purpose. Omit this field entirely to leave `+
				`the stored file_map untouched (e.g. when you only read a couple of changed `+
				`files and don't have the full picture) — pass "{}" to explicitly clear it.`,
		)),
		mcp.WithString("last_commit_sha", mcp.Description("Current git HEAD SHA (run git rev-parse HEAD)")),
	), s.handleUpsertProjectArch)

	ms.AddTool(mcp.NewTool(
		"get_project_arch",
		mcp.WithDescription(
			"Retrieve the stored architecture snapshot for a project. "+
				"Returns the snapshot with a stale field; compare last_commit_sha "+
				"with `git rev-parse HEAD` to determine if the snapshot is up to date. "+
				"Returns an error when no snapshot has been stored yet.",
		),
		mcp.WithString("slug", mcp.Description("Repository/project identifier"), mcp.Required()),
		mcp.WithBoolean("include_file_map", mcp.Description(
			"Include the file_map in the response (default false). file_map is the largest "+
				"field in this tool's response; call with include_file_map=true only when you "+
				"need the path→purpose index.",
		)),
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
	// fileMap is patch semantics (security review PR #157 M-3): the
	// "file_map" key must be entirely ABSENT from args to mean "leave the
	// stored value untouched". args["file_map"] (not stringArg, which
	// collapses "missing" and "present but not a string" into the same
	// "") is what makes "absent" and "present-but-empty" distinguishable —
	// a present key, even "" or "{}", is an explicit instruction and
	// REPLACES the stored map, including clearing it to {}. This matters
	// because the core protocol is read-then-write (get_project_arch ->
	// read changed files -> upsert_project_arch) and get_project_arch
	// defaults to omitting file_map (W2); an agent that follows the
	// protocol literally and never saw the existing map must not be able
	// to wipe it just by not mentioning it.
	var fileMap *map[string]string
	if rawVal, present := args["file_map"]; present {
		rawFileMap, ok := rawVal.(string)
		if !ok {
			return mcp.NewToolResultError("file_map must be a JSON string"), nil
		}
		if len(rawFileMap) > maxFileMapRaw {
			return mcp.NewToolResultError(fmt.Sprintf("file_map too large (max %d bytes)", maxFileMapRaw)), nil
		}
		m := map[string]string{}
		if rawFileMap != "" {
			if err := json.Unmarshal([]byte(rawFileMap), &m); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("file_map must be a valid JSON object: %v", err)), nil
			}
		}
		fileMap = &m
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

	includeFileMap := boolArg(args, "include_file_map")
	return jsonText(wrapUntrustedArchSnapshot(snap, includeFileMap))
}

// wrapUntrustedArchSnapshot returns a copy of snap with its untrusted free
// text made safe to read back into an LLM context:
//
//   - Summary is neutralised against forged boundary markers AND fenced. It is
//     an assistant's prose description of a repository, so a payload planted
//     in any file that assistant read (a README, a vendored dependency, an
//     outside contributor's PR) can reach it through upsert_project_arch,
//     which stores the text with no injection filtering.
//   - FileMap keys and values are neutralised but NOT fenced: they are short
//     path -> purpose pairs, and fencing each of up to 128 KB worth of entries
//     would bury the map in markers. Stripping the marker text is what stops
//     one of them faking a boundary for the fenced Summary above.
//
// includeFileMap gates FileMap entirely: when false (the get_project_arch
// default), the neutralisation loop is skipped and out.FileMap is left nil —
// file_map is the largest field in this response, and the caller did not ask
// for it (W2, token-diet). When true, the field is populated exactly as
// before this option existed.
//
// The core protocol asks for get_project_arch at session start, so this is on
// the automatic path — the same reason the identically-worded fence existed
// while the snapshot was embedded in get_today_context (PR #156 reviewer M1 /
// security review M-3). A nil snapshot is returned unchanged.
func wrapUntrustedArchSnapshot(snap *arch.Snapshot, includeFileMap bool) *arch.Snapshot {
	if snap == nil {
		return nil
	}
	out := *snap
	out.Summary = fenceArchSummary(snap.Summary)
	out.FileMap = nil
	if includeFileMap && len(snap.FileMap) > 0 {
		fileMap := make(map[string]string, len(snap.FileMap))
		for path, purpose := range snap.FileMap {
			fileMap[neutralizeBoundaryMarkers(path)] = neutralizeBoundaryMarkers(purpose)
		}
		out.FileMap = fileMap
	}
	return &out
}
