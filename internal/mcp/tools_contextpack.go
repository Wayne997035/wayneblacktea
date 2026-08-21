package mcp

import (
	"context"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/contextpack"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	maxObjectiveRunes    = 2000
	maxFilesTouched      = 50
	maxFileTouchedRune   = 500
	defaultBudgetChars   = 12000
	minBudgetChars       = 1000
	maxBudgetChars       = 50000
	maxIncludeTypes      = 32
	maxIncludeTypesRunes = 200
)

// knownContextPackTypes is the include_types allowlist from the assemble_context
// spec (docs/wayneblacktea-2.0-development-prompt.md:260). Values outside this
// set are silently ignored rather than rejected — include_types narrows an
// already-safe default (everything), so an unrecognised value cannot widen
// access or trigger unintended retrieval.
var knownContextPackTypes = map[string]bool{
	"semantic":   true,
	"episodic":   true,
	"procedural": true,
	"rules":      true,
	"skills":     true,
	"atoms":      true,
	"outcomes":   true,
}

func (s *Server) registerContextPackTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"assemble_context",
		mcp.WithDescription(
			"Assemble a ranked, budget-bounded context pack for the current task from stored "+
				"memory — decisions, knowledge, procedural how-tos, atoms, outcomes, and related "+
				"session history — trimmed to fit a character budget. Call before starting a "+
				"non-trivial task to get a compact brief instead of re-reading the whole workspace.",
		),
		mcp.WithString("objective",
			mcp.Description("What the caller is trying to accomplish; drives relevance scoring"),
			mcp.Required(), mcp.MaxLength(maxObjectiveRunes)),
		mcp.WithString("repo_name",
			mcp.Description("Optional: scope retrieval to this repository/project slug")),
		mcp.WithString("project_id",
			mcp.Description("Optional: scope retrieval to this project UUID")),
		mcp.WithString("task_id",
			mcp.Description("Optional: scope retrieval to this task UUID")),
		mcp.WithString("branch_name",
			mcp.Description("Optional: current git branch, used as a relevance signal")),
		mcp.WithArray("files_touched",
			mcp.Description("Optional: file paths already touched in this task (max 50 entries, 500 chars each; excess dropped)"),
			mcp.Items(map[string]any{"type": "string"}),
			mcp.MaxItems(maxFilesTouched)),
		mcp.WithNumber("budget_chars",
			mcp.Description("Character budget for the assembled pack (default 12000, clamped to 1000-50000)"),
			mcp.DefaultNumber(defaultBudgetChars),
			mcp.Min(minBudgetChars), mcp.Max(maxBudgetChars)),
		mcp.WithArray("include_types",
			mcp.Description(
				"Optional: restrict to these memory types (semantic, episodic, procedural, rules, "+
					"skills, atoms, outcomes); unrecognised values are ignored",
			),
			mcp.Items(map[string]any{"type": "string"})),
		mcp.WithBoolean("persist",
			mcp.Description("Reserved for Phase 2 (persisting the pack for outcome linkage); MUST be false today"),
			mcp.DefaultBool(false)),
	), s.handleAssembleContext)
}

// handleAssembleContext validates the request, wires it to the contextpack
// Assembler, and returns the assembled Pack. persist is rejected outright —
// Phase 2 will add the write path once outcome-linkage lands.
func (s *Server) handleAssembleContext(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	objective := stripControlChars(stringArg(args, "objective"))
	if objective == "" || len([]rune(objective)) > maxObjectiveRunes {
		return mcp.NewToolResultError("objective is required and must be <=2000 chars"), nil
	}

	var projectID *uuid.UUID
	if raw := stringArg(args, "project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError(errMsgInvalidProjectIDUUID), nil
		}
		projectID = &id
	}

	var taskID *uuid.UUID
	if raw := stringArg(args, "task_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError(errMsgInvalidTaskIDUUID), nil
		}
		taskID = &id
	}

	if boolArg(args, "persist") {
		return mcp.NewToolResultError("persist=true is reserved for Phase 2"), nil
	}

	if s.contextAssembler == nil {
		return mcp.NewToolResultError("context assembler is not configured"), nil
	}

	cpReq := contextpack.Request{
		Objective:    objective,
		RepoName:     stringArg(args, "repo_name"),
		ProjectID:    projectID,
		TaskID:       taskID,
		BranchName:   stringArg(args, "branch_name"),
		FilesTouched: stringArrayArg(args, "files_touched", maxFilesTouched, maxFileTouchedRune),
		BudgetChars:  clampBudgetChars(numberArg(args, "budget_chars")),
		IncludeTypes: filterKnownContextPackTypes(
			stringArrayArg(args, "include_types", maxIncludeTypes, maxIncludeTypesRunes),
		),
		Persist: false,
	}

	pack, err := s.contextAssembler.Assemble(ctx, cpReq)
	if err != nil {
		return storeErrorResult("assembling context", err), nil
	}

	// contextpack.Pack carries its own snake_case json tags (the wire
	// contract at docs/wayneblacktea-2.0-development-prompt.md:265-291), so
	// no wrapper struct is needed here.
	//
	// U13: Pack.Items[].Summary aggregates summaries pulled from decisions,
	// knowledge, procedural, skills, outcomes, reflection, behaviorrule and
	// session read ports — none of which neutralise on the way in, so this is
	// the single choke point where a forged boundary marker stored in any of
	// those domains would reach the model. wrapUntrustedContextPack lives in
	// tools_worksession.go (start_work returns the same contextpack.Pack);
	// both call sites share it so the two paths cannot drift apart.
	return jsonText(wrapUntrustedContextPack(pack))
}

// clampBudgetChars applies the assemble_context default/clamp rule: unset or
// non-positive falls back to defaultBudgetChars, then the result is bounded
// to [minBudgetChars, maxBudgetChars] regardless of what the caller asked
// for (resource guard against an LLM requesting an unbounded pack).
func clampBudgetChars(raw int32) int {
	v := int(raw)
	if v <= 0 {
		v = defaultBudgetChars
	}
	if v < minBudgetChars {
		v = minBudgetChars
	}
	if v > maxBudgetChars {
		v = maxBudgetChars
	}
	return v
}

// filterKnownContextPackTypes drops any include_types entry not in
// knownContextPackTypes. nil in, nil out (contextpack.Request.IncludeTypes
// nil means "no restriction", same as an empty slice).
func filterKnownContextPackTypes(types []string) []string {
	if len(types) == 0 {
		return nil
	}
	out := make([]string, 0, len(types))
	for _, t := range types {
		if knownContextPackTypes[t] {
			out = append(out, t)
		}
	}
	return out
}

// stringArrayArg extracts a string-array argument, keeping only elements
// that are actually strings. maxCount caps the number of elements kept (0 =
// unlimited); maxRunes drops any single element longer than that many runes
// (0 = unlimited) rather than failing the whole request. Bounds adversarial
// LLM-supplied array input before it reaches the Assembler
// (backend-security-design.md §2.1).
func stringArrayArg(args map[string]any, key string, maxCount, maxRunes int) []string {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range raw {
		str, ok := v.(string)
		if !ok {
			continue
		}
		if maxRunes > 0 && len([]rune(str)) > maxRunes {
			continue
		}
		out = append(out, str)
		if maxCount > 0 && len(out) >= maxCount {
			break
		}
	}
	return out
}

// stripControlChars removes NUL and other C0 control characters (except tab
// and newline, which are legitimate in a free-text objective) before length
// validation, per backend-security-design.md §2.1 adversarial-input handling.
func stripControlChars(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == 0 || (r < 0x20 && r != '\t' && r != '\n') || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
