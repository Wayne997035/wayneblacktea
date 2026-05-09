package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerProceduralTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("add_procedural",
		mcp.WithDescription(
			"Saves a reusable how-to memory: title, when to use it, markdown approach, "+
				"tools used, and files typically touched. Call after completing a complex task "+
				"to capture the approach for future reuse.",
		),
		mcp.WithString("title",
			mcp.Description("Short descriptive title of the procedural memory"),
			mcp.Required(), mcp.MaxLength(200)),
		mcp.WithString("when_to_use",
			mcp.Description("Describe the situation or trigger that warrants this approach"),
			mcp.Required(), mcp.MaxLength(2000)),
		mcp.WithString("approach_md",
			mcp.Description("Markdown-formatted step-by-step approach or decision record"),
			mcp.MaxLength(20000)),
		mcp.WithString("repo_name",
			mcp.Description("Repository or project slug this memory belongs to")),
		mcp.WithString("tools_used",
			mcp.Description("Comma-separated list of tools used (e.g. 'Bash,Read,Edit')")),
		mcp.WithString("files_touched",
			mcp.Description("Comma-separated list of files or patterns typically touched")),
	), s.handleAddProcedural)

	ms.AddTool(mcp.NewTool("query_procedural",
		mcp.WithDescription(
			"Returns procedural memories matching keywords. Searches title, when_to_use, "+
				"and approach_md. Results ordered by success_count DESC so the most-proven "+
				"approaches surface first.",
		),
		mcp.WithString("keywords",
			mcp.Description("Free-text keywords to search across title, when_to_use, and approach_md"),
			mcp.Required()),
		mcp.WithString("repo_name",
			mcp.Description("Optional: filter by repository/project slug")),
		mcp.WithNumber("limit",
			mcp.Description("Max results to return (default 10, max 20)")),
	), s.handleQueryProcedural)

	ms.AddTool(mcp.NewTool("mark_procedural_used",
		mcp.WithDescription(
			"Increments the success_count of a procedural memory and sets last_used_at. "+
				"Call after successfully applying a procedural memory to reinforce its ranking.",
		),
		mcp.WithString("id",
			mcp.Description("UUID of the procedural memory"),
			mcp.Required()),
	), s.handleMarkProceduralUsed)

	ms.AddTool(mcp.NewTool("recall",
		mcp.WithDescription(
			"Unified cross-type memory search. Searches episodic (recent session handoffs), "+
				"semantic (knowledge + decisions), and procedural memories simultaneously. "+
				"Use for broad 'what do I know about X' queries.",
		),
		mcp.WithString("query",
			mcp.Description("Search query to run across all memory types"),
			mcp.Required()),
		mcp.WithString("types",
			mcp.Description(
				"Comma-separated memory types to search: episodic, semantic, procedural. "+
					"Default: all three.")),
	), s.handleRecall)
}

// handleAddProcedural creates a new procedural memory.
func (s *Server) handleAddProcedural(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	title := stringArg(args, "title")
	whenToUse := stringArg(args, "when_to_use")
	if title == "" {
		return mcp.NewToolResultError("title is required"), nil
	}
	if len([]rune(title)) > 200 {
		return mcp.NewToolResultError("title exceeds 200 character limit"), nil
	}
	if whenToUse == "" {
		return mcp.NewToolResultError("when_to_use is required"), nil
	}
	if len([]rune(whenToUse)) > 2000 {
		return mcp.NewToolResultError("when_to_use exceeds 2000 character limit"), nil
	}
	if approachMD := stringArg(args, "approach_md"); len([]rune(approachMD)) > 20000 {
		return mcp.NewToolResultError("approach_md exceeds 20000 character limit"), nil
	}

	p := procedural.AddParams{
		Title:        title,
		WhenToUse:    whenToUse,
		ApproachMD:   stringArg(args, "approach_md"),
		RepoName:     stringArg(args, "repo_name"),
		ToolsUsed:    splitCSV(stringArg(args, "tools_used")),
		FilesTouched: splitCSV(stringArg(args, "files_touched")),
	}
	if wsID := s.workspaceUUID(); wsID != nil {
		p.WorkspaceID = wsID
	}

	mem, err := s.procedural.Add(ctx, p)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("adding procedural memory: %v", err)), nil
	}
	s.launchAtomize("procedural_memories", mem.ID, mem.Title+" "+mem.WhenToUse+" "+mem.ApproachMD)
	return jsonText(mem)
}

// handleQueryProcedural searches procedural memories.
func (s *Server) handleQueryProcedural(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	keywords := stringArg(args, "keywords")
	if keywords == "" {
		return mcp.NewToolResultError("keywords is required"), nil
	}

	limit := int(numberArg(args, "limit"))
	if limit <= 0 {
		limit = 10
	}
	if limit > 20 {
		limit = 20
	}

	f := procedural.QueryFilter{
		Keywords: keywords,
		RepoName: stringArg(args, "repo_name"),
		Limit:    limit,
	}
	if wsID := s.workspaceUUID(); wsID != nil {
		f.WorkspaceID = wsID
	}

	results, err := s.procedural.Query(ctx, f)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("querying procedural memories: %v", err)), nil
	}
	if results == nil {
		results = []procedural.ProceduralMemory{}
	}
	return jsonText(results)
}

// handleMarkProceduralUsed increments a procedural memory's success count.
func (s *Server) handleMarkProceduralUsed(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	rawID := stringArg(args, "id")
	if rawID == "" {
		return mcp.NewToolResultError("id is required"), nil
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		return mcp.NewToolResultError("invalid id UUID"), nil
	}

	mem, err := s.procedural.MarkUsed(ctx, id)
	if errors.Is(err, procedural.ErrNotFound) {
		return mcp.NewToolResultError("procedural memory not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marking procedural memory used: %v", err)), nil
	}
	return jsonText(mem)
}

// handleRecall performs a unified cross-type memory search.
func (s *Server) handleRecall(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	query := stringArg(args, "query")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}

	// Determine which types to search.
	wantEpisodic := true
	wantSemantic := true
	wantProcedural := true
	if typesRaw := stringArg(args, "types"); typesRaw != "" {
		wantEpisodic = false
		wantSemantic = false
		wantProcedural = false
		for _, t := range splitCSV(typesRaw) {
			switch strings.TrimSpace(t) {
			case "episodic":
				wantEpisodic = true
			case "semantic":
				wantSemantic = true
			case "procedural":
				wantProcedural = true
			}
		}
	}

	result := map[string]any{}

	// Episodic: search recent session handoffs by text contains.
	// session.StoreIface has no text-search method; use LatestHandoff and
	// return it if the summary contains the query.
	if wantEpisodic {
		episodic := recallEpisodic(ctx, s, query)
		result["episodic"] = episodic
	}

	// Semantic: knowledge Search + recent decisions filtered by query.
	if wantSemantic {
		knowledgeResults := recallKnowledge(ctx, s, query)
		decisionResults := recallDecisions(ctx, s, query)
		result["semantic"] = map[string]any{
			"knowledge": knowledgeResults,
			"decisions": decisionResults,
		}
	}

	// Procedural: query procedural memories by keyword.
	if wantProcedural {
		f := procedural.QueryFilter{
			Keywords: query,
			Limit:    5,
		}
		if wsID := s.workspaceUUID(); wsID != nil {
			f.WorkspaceID = wsID
		}
		proc, err := s.procedural.Query(ctx, f)
		if err != nil {
			slog.Warn("recall: procedural query failed", "err", err)
			proc = []procedural.ProceduralMemory{}
		}
		if proc == nil {
			proc = []procedural.ProceduralMemory{}
		}
		result["procedural"] = proc
	}

	// Atoms: search memory_atoms by keyword.
	result["atoms"] = recallAtoms(ctx, s, query)

	return jsonText(result)
}

// recallAtoms searches memory atoms and returns a slice (never nil).
func recallAtoms(ctx context.Context, s *Server, query string) []atom.Atom {
	if s.atom == nil {
		return []atom.Atom{}
	}
	atoms, err := s.atom.Search(ctx, s.workspaceUUID(), query, 5)
	if err != nil {
		slog.Warn("recall: atom search failed", "err", err)
		return []atom.Atom{}
	}
	if atoms == nil {
		return []atom.Atom{}
	}
	return atoms
}

// recallEpisodic retrieves the latest session handoff and filters by query.
// Returns a slice so the JSON output is always an array.
func recallEpisodic(ctx context.Context, s *Server, query string) []any {
	if s.session == nil {
		return []any{}
	}
	h, err := s.session.LatestHandoff(ctx)
	if err != nil {
		// ErrNotFound is normal when no handoff exists yet.
		return []any{}
	}
	// Filter: only include if summary_text or intent contains the query.
	qLower := strings.ToLower(query)
	if strings.Contains(strings.ToLower(h.ContextSummary.String), qLower) ||
		strings.Contains(strings.ToLower(h.Intent), qLower) {
		return []any{h}
	}
	return []any{}
}

// recallKnowledge searches knowledge items and falls back to empty on error.
func recallKnowledge(ctx context.Context, s *Server, query string) any {
	if s.knowledge == nil {
		return []any{}
	}
	items, err := s.knowledge.Search(ctx, query, 5)
	if err != nil {
		slog.Warn("recall: knowledge search failed", "err", err)
		return []any{}
	}
	if items == nil {
		return []any{}
	}
	return items
}

// recallDecisions fetches the most recent decisions and filters by query.
func recallDecisions(ctx context.Context, s *Server, query string) any {
	if s.decision == nil {
		return []any{}
	}
	decisions, err := s.decision.All(ctx, 20)
	if err != nil {
		slog.Warn("recall: decision list failed", "err", err)
		return []any{}
	}
	if decisions == nil {
		return []any{}
	}
	qLower := strings.ToLower(query)
	var filtered []any
	for _, d := range decisions {
		if strings.Contains(strings.ToLower(d.Decision), qLower) ||
			strings.Contains(strings.ToLower(d.Title), qLower) ||
			strings.Contains(strings.ToLower(d.RepoName.String), qLower) ||
			strings.Contains(strings.ToLower(d.Rationale), qLower) {
			filtered = append(filtered, d)
		}
		if len(filtered) >= 5 {
			break
		}
	}
	if filtered == nil {
		return []any{}
	}
	return filtered
}

// splitCSV splits a comma-separated string into a trimmed, non-empty slice.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
