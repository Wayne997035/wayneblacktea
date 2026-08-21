package mcp

import (
	"context"
	"log/slog"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const maxListDecisionsLimit = 100

// Read-time bounds for db.Decision's free-text fields, applied by
// wrapUntrustedDecision before jsonText — U13 (2026-08-20-mcp-surface-
// spec.md). log_decision/list_decisions register no mcp.MaxLength on any of
// these fields today (checkDecisionNoise below only screens for tag-noise,
// not length), so these bounds exist purely to stop marker-stuffing /
// pathological-growth content from reaching an unbounded read. They are
// intentionally generous — this is a full-record read, not the
// session-start token-diet payload get_today_context enforces via
// goalTitleMaxRunes/projectTitleMaxRunes/taskTitleMaxRunes (tools_context.go)
// — legitimate content should never hit these caps.
const (
	decisionTitleMaxRunes = 500
	decisionBodyMaxRunes  = 20000
)

// wrapUntrustedDecision returns a copy of d with every free-text field
// clipSafe'd (bounded + boundary-marker-neutralised) — U13. Mirrors
// wrapUntrustedArchSnapshot's copy-not-mutate contract (tools_arch.go): the
// caller's row (and any cache holding it) must not end up with
// fence/neutralisation baked into its stored text. nil in, nil out.
//
// ID, ProjectID, RepoName, CreatedAt, WorkspaceID, Embedding, TaskID and the
// embedding-provenance fields are left untouched — none of them is free text
// an LLM authored, so none carries injection risk. RepoName specifically is
// validator.IsValidRepoName-gated at every write path in this codebase
// ([a-zA-Z0-9_.-]{1,100}), which forecloses embedding a marker string in it.
func wrapUntrustedDecision(d *db.Decision) *db.Decision {
	if d == nil {
		return nil
	}
	out := *d
	out.Title = clipSafe(d.Title, decisionTitleMaxRunes)
	out.Context = clipSafe(d.Context, decisionBodyMaxRunes)
	out.Decision = clipSafe(d.Decision, decisionBodyMaxRunes)
	out.Rationale = clipSafe(d.Rationale, decisionBodyMaxRunes)
	if d.Alternatives.Valid {
		out.Alternatives.String = clipSafe(d.Alternatives.String, decisionBodyMaxRunes)
	}
	return &out
}

// wrapUntrustedDecisions maps wrapUntrustedDecision over a slice, preserving
// element order and a nil/empty distinction is not needed here — every
// caller of this helper already normalises decisions to a non-nil, possibly
// empty slice before calling it (list tools MUST return [] not null).
func wrapUntrustedDecisions(decisions []db.Decision) []db.Decision {
	out := make([]db.Decision, len(decisions))
	for i := range decisions {
		out[i] = *wrapUntrustedDecision(&decisions[i])
	}
	return out
}

func (s *Server) registerDecisionTools(ms *server.MCPServer) {
	// The "CALL when ... confirmed" wording below is calling-convention
	// guidance for the LLM, not a security control — log_decision has no
	// confirmation gate of its own, so it cannot verify that a human
	// actually said go/start/好啊 (security review round 2, M-2(a); real
	// gate tracked at GTD task 41ef0520-ad5a-4aa0-805b-4ba13ba927fd). See
	// decision.Source's doc comment for the broader provenance caveat.
	ms.AddTool(mcp.NewTool(
		"log_decision",
		mcp.WithDescription(
			"CALL when the user signals go-ahead on a technical decision (e.g. says go/start/好啊) — "+
				"this is a usage convention, not a verified confirmation (the tool has no confirmation "+
				"gate of its own). Records architectural and design decisions with context and rationale.",
		),
		mcp.WithString("title", mcp.Description("Short decision title"), mcp.Required()),
		mcp.WithString("context", mcp.Description("What problem or situation prompted this decision"), mcp.Required()),
		mcp.WithString("decision", mcp.Description("What was decided"), mcp.Required()),
		mcp.WithString("rationale", mcp.Description("Why this decision was made"), mcp.Required()),
		mcp.WithString("repo_name", mcp.Description("Repository this decision relates to")),
		mcp.WithString("project_id", mcp.Description("Project UUID this decision relates to")),
		mcp.WithString("task_id", mcp.Description("Task UUID this decision relates to")),
		mcp.WithString("alternatives", mcp.Description("Other options that were considered")),
	), s.handleLogDecision)

	ms.AddTool(mcp.NewTool(
		"list_decisions",
		mcp.WithDescription(
			"CALL BEFORE scanning code — check if the answer is already stored. "+
				"Returns manual decisions by default; set include_auto=true to also include "+
				"system-inferred decisions. Filtered by repo_name or project (project wins if both given).",
		),
		mcp.WithString("repo_name", mcp.Description("Filter by repository name")),
		mcp.WithString("project_id", mcp.Description("Filter by project UUID (wins over repo_name if both given)")),
		mcp.WithBoolean("include_auto", mcp.Description("Include system-inferred (auto) decisions in addition to manual ones. Default false.")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 20, max 100)")),
	), s.handleListDecisions)
}

func (s *Server) handleLogDecision(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	title := stringArg(args, "title")
	decCtx := stringArg(args, "context")
	dec := stringArg(args, "decision")
	rationale := stringArg(args, "rationale")
	if title == "" || decCtx == "" || dec == "" || rationale == "" {
		return mcp.NewToolResultError("title, context, decision and rationale are required"), nil
	}

	if reason := checkDecisionNoise(title, decCtx, dec, rationale); reason != "" {
		return mcp.NewToolResultError("invalid params: " + reason), nil
	}

	p := decision.LogParams{
		Title:          title,
		Context:        decCtx,
		Decision:       dec,
		Rationale:      rationale,
		RepoName:       stringArg(args, "repo_name"),
		Alternatives:   stringArg(args, "alternatives"),
		Source:         decision.SourceManual,
		ActorSessionID: s.auditSessionID(ctx),
		// ConfirmedByHuman is deliberately left at its false zero value: this
		// tool has no confirmation gate of its own (see its description
		// above — "the tool has no confirmation gate of its own"), so
		// calling it does not prove a human confirmed anything. true is
		// reserved for a future real human-confirmation gate (GTD 41ef0520);
		// no MCP path is entitled to set it yet.
	}
	if raw := stringArg(args, "project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError(errMsgInvalidProjectIDUUID), nil
		}
		p.ProjectID = &id
	}
	if raw := stringArg(args, "task_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError(errMsgInvalidTaskIDUUID), nil
		}
		p.TaskID = &id
	}

	d, err := s.decision.Log(ctx, p)
	if err != nil {
		return storeErrorResult("logging decision", err), nil
	}
	s.launchAtomize("decisions", d.ID, d.Decision+" "+d.Rationale)
	return jsonText(wrapUntrustedDecision(d))
}

// handleListDecisions implements the P3.0a Stage B truth table:
//   - invalid project_id UUID -> tool error, store never called
//   - project_id and repo_name both given -> project wins (repo_name dropped)
//   - repo_name only -> filtered by repo
//   - neither -> workspace-wide
//   - project not owned / nonexistent -> [] (store's workspace scoping and
//     the WHERE project_id = ? predicate both fail closed, no error)
//   - include_auto omitted or non-bool -> boolArg fails closed to false
//   - include_auto=true -> manual + auto
func (s *Server) handleListDecisions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	limit := numberArg(args, "limit")
	if limit <= 0 {
		limit = 20
	}
	if limit > maxListDecisionsLimit {
		slog.Warn("list_decisions limit clamped", "requested", limit, "max", maxListDecisionsLimit)
		limit = maxListDecisionsLimit
	}

	p := decision.ListParams{
		RepoName:    stringArg(args, "repo_name"),
		IncludeAuto: boolArg(args, "include_auto"),
		Limit:       limit,
	}
	if raw := stringArg(args, "project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError(errMsgInvalidProjectIDUUID), nil
		}
		p.ProjectID = &id
		p.RepoName = "" // project wins over repo_name when both are given
	}

	decisions, err := s.decision.List(ctx, p)
	if err != nil {
		return storeErrorResult("loading decisions", err), nil
	}
	if decisions == nil {
		decisions = []db.Decision{} // list tools MUST return [] not null — a nil slice serializes to JSON null
	}
	return jsonText(wrapUntrustedDecisions(decisions))
}
