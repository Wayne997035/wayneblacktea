package mcp

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Read-time bounds for procedural.ProceduralMemory's free-text fields,
// applied by wrapUntrustedProceduralMemory before jsonText — U13
// (2026-08-20-mcp-surface-spec.md). proceduralTitleMaxRunes/
// proceduralWhenToUseMaxRunes/proceduralApproachMaxRunes mirror
// handleAddProcedural's write-time caps (200/2000/20000 runes).
// proceduralListItemMaxRunes bounds ToolsUsed/FilesTouched, which have no
// write-time per-item cap (only the raw comma-separated string is length
// — implicitly — capped via approach_md/when_to_use's own caps, not its
// own) — read-time-only backstop against marker-stuffing, same rationale as
// decisionBodyMaxRunes (tools_decision.go).
//
// The content here is dispatch-flagged as especially injection-shaped:
// add_procedural/query_procedural/mark_procedural_used store literal
// step-by-step approach text (ApproachMD is explicitly "Markdown-formatted
// step-by-step approach") — exactly the shape a forged marker plus
// injected instruction would want to hide inside
// (backend-security-design.md §2.1).
const (
	proceduralTitleMaxRunes     = 200
	proceduralWhenToUseMaxRunes = 2000
	proceduralApproachMaxRunes  = 20000
	proceduralListItemMaxRunes  = 2000
)

// wrapUntrustedProceduralMemory returns a copy of m with every free-text
// field clipSafe'd (bounded + boundary-marker-neutralised) — U13. Mirrors
// wrapUntrustedTask/wrapUntrustedDecision's copy-not-mutate contract. nil
// in, nil out.
//
// ID, WorkspaceID, RepoName, ProjectID, SuccessCount, LastUsedAt, CreatedAt
// are left untouched — none is free text a caller authored. RepoName is
// validator-gated at every write path in this codebase, same as
// wrapUntrustedDecision's rationale for its own RepoName field
// (tools_decision.go).
func wrapUntrustedProceduralMemory(m *procedural.ProceduralMemory) *procedural.ProceduralMemory {
	if m == nil {
		return nil
	}
	out := *m
	out.Title = clipSafe(m.Title, proceduralTitleMaxRunes)
	out.WhenToUse = clipSafe(m.WhenToUse, proceduralWhenToUseMaxRunes)
	out.ApproachMD = clipSafe(m.ApproachMD, proceduralApproachMaxRunes)
	out.ToolsUsed = clipSafeSlice(m.ToolsUsed, proceduralListItemMaxRunes)
	out.FilesTouched = clipSafeSlice(m.FilesTouched, proceduralListItemMaxRunes)
	return &out
}

// wrapUntrustedProceduralMemories maps wrapUntrustedProceduralMemory over a
// value slice (not pointers — procedural.Store returns
// []procedural.ProceduralMemory), preserving order. Callers already
// normalise nil results to []procedural.ProceduralMemory{} before calling
// this (list tools MUST return [] not null).
func wrapUntrustedProceduralMemories(memories []procedural.ProceduralMemory) []procedural.ProceduralMemory {
	out := make([]procedural.ProceduralMemory, len(memories))
	for i := range memories {
		out[i] = *wrapUntrustedProceduralMemory(&memories[i])
	}
	return out
}

// recallItemBodyMaxRunes bounds the free-text fields recall's semantic and
// atoms branches neutralize below (neutralizeRecallKnowledgeItems,
// neutralizeRecallAtoms). Generous read-time-only backstop, same rationale
// as decisionBodyMaxRunes (tools_decision.go); recall caps every branch's
// result count at 5, so this is a marker-stuffing defence, not a
// token-diet measure.
const recallItemBodyMaxRunes = 20000

// neutralizeRecallKnowledgeItems neutralizes the free-text Title/Content
// fields of the knowledge items recall's semantic branch returns.
//
// Named/scoped to this file rather than tools_knowledge.go (which owns the
// rest of db.KnowledgeItem's PENDING inventory rows — add_knowledge,
// search_knowledge, list_knowledge) for the same parallel-dispatch
// collision-avoidance reason this file's own now-merged clip-loop helper
// used to carry in its doc comment (ticket ff812f80 consolidated it into
// clipSafeSlice): this handler's call site (handleRecall, tools_procedural.go)
// is not part of that file's assignment, but returns the identical stored
// struct.
func neutralizeRecallKnowledgeItems(items []db.KnowledgeItem) []db.KnowledgeItem {
	out := make([]db.KnowledgeItem, len(items))
	for i, it := range items {
		cp := it
		cp.Title = clipSafe(it.Title, recallItemBodyMaxRunes)
		cp.Content = clipSafe(it.Content, recallItemBodyMaxRunes)
		out[i] = cp
	}
	return out
}

// neutralizeRecallAtoms neutralizes the free-text Content field of atoms
// recall's atoms branch returns. Keywords/Tags are left untouched to match
// tools_atom.go's own PENDING inventory scoping for this type (Content
// only) — this file's recall handler reaches the same atom.Atom struct
// through a different call site than that file's traverse_atoms/
// search_atoms.
func neutralizeRecallAtoms(atoms []atom.Atom) []atom.Atom {
	out := make([]atom.Atom, len(atoms))
	for i, a := range atoms {
		cp := a
		cp.Content = clipSafe(a.Content, recallItemBodyMaxRunes)
		out[i] = cp
	}
	return out
}

func (s *Server) registerProceduralTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"add_procedural",
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

	ms.AddTool(mcp.NewTool(
		"query_procedural",
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

	ms.AddTool(mcp.NewTool(
		"mark_procedural_used",
		mcp.WithDescription(
			"Increments the success_count of a procedural memory and sets last_used_at. "+
				"Call after successfully applying a procedural memory to reinforce its ranking.",
		),
		mcp.WithString("id",
			mcp.Description("UUID of the procedural memory"),
			mcp.Required()),
	), s.handleMarkProceduralUsed)

	ms.AddTool(mcp.NewTool(
		"recall",
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
					"Default: all three.",
			)),
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
	approachMD := stringArg(args, "approach_md")
	if len([]rune(approachMD)) > 20000 {
		return mcp.NewToolResultError("approach_md exceeds 20000 character limit"), nil
	}

	// title is short/single-line — reject embedded control chars/newlines
	// outright. when_to_use and approach_md are long-form (approach_md is
	// explicitly markdown) so newlines are semantic content and stay
	// allowed; only NUL/ANSI/other control bytes are rejected. None of the
	// three is silently modified — bad input is a hard error.
	var ccErr error
	title, ccErr = sanitize.RejectControlChars(title, 200, false)
	if ccErr != nil {
		return inputErrorResult("title", ccErr), nil
	}
	whenToUse, ccErr = sanitize.RejectControlChars(whenToUse, 2000, true)
	if ccErr != nil {
		return inputErrorResult("when_to_use", ccErr), nil
	}
	approachMD, ccErr = sanitize.RejectControlChars(approachMD, 20000, true)
	if ccErr != nil {
		return inputErrorResult("approach_md", ccErr), nil
	}

	p := procedural.AddParams{
		Title:        title,
		WhenToUse:    whenToUse,
		ApproachMD:   approachMD,
		RepoName:     stringArg(args, "repo_name"),
		ToolsUsed:    splitCSV(stringArg(args, "tools_used")),
		FilesTouched: splitCSV(stringArg(args, "files_touched")),
	}
	if wsID := s.workspaceUUID(); wsID != nil {
		p.WorkspaceID = wsID
	}

	mem, err := s.procedural.Add(ctx, p)
	if err != nil {
		return storeErrorResult("adding procedural memory", err), nil
	}
	s.launchAtomize("procedural_memories", mem.ID, mem.Title+" "+mem.WhenToUse+" "+mem.ApproachMD)
	return jsonText(wrapUntrustedProceduralMemory(mem))
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
		return storeErrorResult("querying procedural memories", err), nil
	}
	if results == nil {
		results = []procedural.ProceduralMemory{}
	}
	return jsonText(wrapUntrustedProceduralMemories(results))
}

// handleMarkProceduralUsed increments a procedural memory's success count.
func (s *Server) handleMarkProceduralUsed(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	id, errResult := requireUUIDArg(args, "id", "invalid id UUID")
	if errResult != nil {
		return errResult, nil
	}

	mem, err := s.procedural.MarkUsed(ctx, id)
	if errors.Is(err, procedural.ErrNotFound) {
		return mcp.NewToolResultError("procedural memory not found"), nil
	}
	if err != nil {
		return storeErrorResult("marking procedural memory used", err), nil
	}
	return jsonText(wrapUntrustedProceduralMemory(mem))
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
		result["procedural"] = wrapUntrustedProceduralMemories(proc)
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
	return neutralizeRecallAtoms(atoms)
}

// recallEpisodic retrieves the latest session handoff and filters by query.
// Returns a slice so the JSON output is always an array.
//
// The returned element is a safeSessionHandoff (session_handoff_safe.go), not
// the raw *db.SessionHandoff — PR #158 round-2 security review found this
// branch returning the raw row verbatim (including the Embedding blob and
// unfenced Intent/ContextSummary text with no defence against forged
// boundary markers), reachable by ANY session via recall with no handoff_id
// required. rawIntent/rawContextSummary are the type's designated escape
// hatch for exactly this kind of internal, non-serializing comparison; the
// value actually returned to the caller is the safe wrapper, so its JSON
// encoding always goes through safeSessionHandoff.MarshalJSON's hardened
// view, however it ends up nested in the response.
func recallEpisodic(ctx context.Context, s *Server, query string) []any {
	if s.session == nil {
		return []any{}
	}
	h, err := s.session.LatestHandoff(ctx)
	if err != nil {
		// ErrNotFound is normal when no handoff exists yet.
		return []any{}
	}
	safe := newSafeSessionHandoff(h)
	// Filter: only include if summary_text or intent contains the query.
	qLower := strings.ToLower(query)
	if strings.Contains(strings.ToLower(safe.rawContextSummary()), qLower) ||
		strings.Contains(strings.ToLower(safe.rawIntent()), qLower) {
		return []any{safe}
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
	return neutralizeRecallKnowledgeItems(items)
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
			// wrapUntrustedDecision (tools_decision.go) — reused directly
			// since it already exists in this package, unlike the
			// file-local helpers above (no collision risk: it's an
			// already-committed Phase A helper, not something a parallel
			// Phase B branch might independently reinvent).
			filtered = append(filtered, *wrapUntrustedDecision(&d))
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
