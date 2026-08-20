package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerSessionTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"set_session_handoff",
		mcp.WithDescription("CALL when user says tomorrow/next time/later. Records what to continue in next session."),
		mcp.WithString("intent", mcp.Description("What to continue next session"), mcp.Required()),
		mcp.WithString("repo_name", mcp.Description("Repository being worked on")),
		mcp.WithString("context_summary", mcp.Description("Current state and relevant context")),
		mcp.WithString("project_id", mcp.Description("Active project UUID")),
		mcp.WithString("next_actions", mcp.Description(
			`Optional JSON array of next-action objects. Each object: `+
				`{"step":1,"title":"<short>","command":"<optional>","expected":"<optional>","status":"pending","ref_task_id":"<optional uuid>"}`,
		)),
	), s.handleSetSessionHandoff)

	ms.AddTool(mcp.NewTool(
		"resolve_handoff",
		mcp.WithDescription("Marks the pending session handoff as resolved (work resumed)."),
		mcp.WithString("handoff_id", mcp.Description("Handoff UUID to resolve"), mcp.Required()),
	), s.handleResolveHandoff)

	ms.AddTool(mcp.NewTool(
		"mark_next_action_done",
		mcp.WithDescription("Sets next_actions[step].status = 'done' for the given handoff. "+
			"Only works on handoffs that belong to the caller's workspace."),
		mcp.WithString("handoff_id", mcp.Description("Session handoff UUID"), mcp.Required()),
		mcp.WithNumber("step", mcp.Description("Step number (integer) to mark done"), mcp.Required()),
	), s.handleMarkNextActionDone)
}

func (s *Server) handleSetSessionHandoff(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	intent := stringArg(args, "intent")
	if intent == "" {
		return mcp.NewToolResultError("intent is required"), nil
	}

	contextSummary := stringArg(args, "context_summary")
	if reason := checkHandoffNoise(intent, contextSummary); reason != "" {
		return mcp.NewToolResultError("invalid params: " + reason), nil
	}

	// repo_name goes through the same checkCommandField gate as
	// next_actions.title/command/expected below (PR #157 round-2 security
	// review m-R2): before this check, repo_name was the only free-text field
	// of this handoff that accepted raw control characters — newline, ESC,
	// NUL, and U+2028 all passed write-time validation unmodified. It renders
	// in the same resource payload as those fields (resources.go
	// handoffResourceRepoNameMaxRunes doc comment) and carries the same
	// prompt-injection risk, so it must be rejected here rather than merely
	// clipped at read time.
	repoName := stringArg(args, "repo_name")
	if reason := checkCommandField("repo_name", repoName); reason != "" {
		return mcp.NewToolResultError("invalid params: " + reason), nil
	}

	p := session.HandoffParams{
		Intent:         intent,
		RepoName:       repoName,
		ContextSummary: contextSummary,
	}
	if raw := stringArg(args, "project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError(errMsgInvalidProjectIDUUID), nil
		}
		p.ProjectID = &id
	}

	// Parse optional next_actions JSON array.
	if raw := stringArg(args, "next_actions"); raw != "" {
		actions, errMsg := parseAndValidateNextActions(raw)
		if errMsg != "" {
			return mcp.NewToolResultError(errMsg), nil
		}
		p.NextActions = actions
	}

	h, err := s.session.SetHandoff(ctx, p)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("setting handoff: %v", err)), nil
	}
	return jsonText(buildPendingHandoffView(h))
}

func (s *Server) handleResolveHandoff(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	id, errResult := requireUUIDArg(args, "handoff_id", "invalid handoff_id UUID")
	if errResult != nil {
		return errResult, nil
	}

	if err := s.session.Resolve(ctx, id); errors.Is(err, session.ErrNotFound) {
		return mcp.NewToolResultError("handoff not found"), nil
	} else if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolving handoff: %v", err)), nil
	}

	return mcp.NewToolResultText("handoff resolved"), nil
}

func (s *Server) handleMarkNextActionDone(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	handoffID, errResult := requireUUIDArg(args, "handoff_id", "invalid handoff_id UUID")
	if errResult != nil {
		return errResult, nil
	}

	// requireIntArg (server.go, U12) rejects a fractional step (e.g. 2.5)
	// instead of numberArg's old silent int32(v) truncation to 2.
	stepN, errResult := requireIntArg(args, "step")
	if errResult != nil {
		return errResult, nil
	}
	if stepN < 0 || stepN > maxNextActionItems {
		return mcp.NewToolResultError(fmt.Sprintf("step out of range: must be 0-%d", maxNextActionItems)), nil
	}
	step := int(stepN)

	h, err := s.session.MarkNextActionDone(ctx, handoffID, step)
	if errors.Is(err, session.ErrNotFound) {
		return mcp.NewToolResultError("handoff not found or step does not exist"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marking next action done: %v", err)), nil
	}
	return jsonText(buildHardenedHandoffView(h))
}

// buildHardenedHandoffView renders h through the same clip + fence +
// neutralise + byte-budget treatment the wayneblacktea://session/handoff/latest
// resource applies (resources.go handleResourceHandoffLatest) — mirrored here
// field-for-field rather than shared via a common helper, because
// resources.go is out of scope for this change (PR #158 round-2 security
// review dispatch); every constant and helper called below (clipSafe,
// clipAndFenceStoredContext, appendNextActionsWithinByteBudget,
// storedDataNotice, handoffResource*MaxRunes/Bytes) is the existing one that
// file already defines, not a newly chosen number.
//
// handleMarkNextActionDone is the only caller. Its caller supplies only
// handoff_id + step — unlike handleSetSessionHandoff, which echoes back the
// text the SAME turn just wrote (buildPendingHandoffView's doc comment,
// tools_context.go), mark_next_action_done's content was written by a
// possibly earlier, possibly untrusted session, so it gets no more trust than
// the read-only resource does.
//
// h must be non-nil — handleMarkNextActionDone has already returned on
// session.ErrNotFound before reaching this call.
func buildHardenedHandoffView(h *db.SessionHandoff) handoffResource {
	view := buildPendingHandoffView(h)
	id := h.ID
	// Computed from the FULL decoded list, BEFORE the byte-budget truncation
	// below — same ordering, same reason, as the resource
	// (handoffResourceNextActionsMaxBytes doc comment, resources.go).
	nextActionsTotal := len(view.NextActions)
	out := handoffResource{
		HandoffPresent:   true,
		ID:               &id,
		StoredDataNotice: storedDataNotice,
		// clipSafe, not textValue: repo_name rides in the SAME payload as the
		// fenced Intent/ContextSummary below, so an un-neutralised marker here
		// forges an escape for those fences (boundary_markers.go).
		RepoName: clipSafe(textValue(h.RepoName), handoffResourceRepoNameMaxRunes),
		// Fenced, not merely clipped: same threat model as the resource — the
		// reader has earned no trust on this content.
		Intent:           clipAndFenceStoredContext(h.Intent, handoffResourceIntentMaxRunes),
		ContextSummary:   clipAndFenceStoredContext(textValue(h.ContextSummary), handoffResourceSummaryMaxRunes),
		NextActionsTotal: &nextActionsTotal,
	}
	if h.CreatedAt.Valid {
		out.CreatedAt = h.CreatedAt.Time.UTC().Format(time.RFC3339)
	}
	if len(view.NextActions) > 0 {
		out.NextActions = appendNextActionsWithinByteBudget(view.NextActions, handoffResourceNextActionsMaxBytes)
	}
	return out
}

const (
	maxNextActionItems    = 50
	maxNextActionFieldLen = 500
)

// nextActionControlCharFields lists the three free-text fields of a
// NextAction that must reject control characters via checkCommandField —
// title/command/expected. title was the one field of the four (the fourth,
// ref_task_id, is UUID-validated separately) that skipped this check (PR #157
// security review M-2): command and expected both reject embedded newlines so
// a prompt-injected agent cannot smuggle a second instruction line in through
// them (CheckCommandField's doc comment), and the same threat applies to
// title with no principled reason it was exempt. Iterating this list (rather
// than three unrolled if-blocks) is also what keeps
// validateNextActionFields under the project's cyclomatic-complexity gate.
func nextActionControlCharFields(a session.NextAction) []struct{ name, value string } {
	return []struct{ name, value string }{
		{"title", a.Title},
		{"command", a.Command},
		{"expected", a.Expected},
	}
}

// validateNextActionFields checks one NextAction's field-length, control-char,
// ref_task_id and status constraints, returning a bare (index-free) reason so
// parseAndValidateNextActions can attach "next_actions[i]: " once at the call
// site. Extracted from parseAndValidateNextActions to keep that function
// under the project's cyclomatic-complexity gate.
func validateNextActionFields(a session.NextAction) string {
	if a.Title == "" {
		return "title is required"
	}
	// F8 (2026-08-20-mcp-surface-spec.md U10): step previously had NO range
	// check at write time, while mark_next_action_done's own step param was
	// already bounded to 0-maxNextActionItems — a step value written here
	// outside that range could never be marked done later. Reusing the same
	// constant closes that mismatch instead of introducing a second number.
	if a.Step < 0 || a.Step > maxNextActionItems {
		return fmt.Sprintf("step must be between 0 and %d", maxNextActionItems)
	}
	for _, f := range nextActionControlCharFields(a) {
		if len([]rune(f.value)) > maxNextActionFieldLen {
			return fmt.Sprintf("%s exceeds %d characters", f.name, maxNextActionFieldLen)
		}
		if reason := checkCommandField(f.name, f.value); reason != "" {
			return reason
		}
	}
	if a.RefTaskID != nil && *a.RefTaskID != "" {
		if _, err := uuid.Parse(*a.RefTaskID); err != nil {
			return "ref_task_id must be a valid UUID"
		}
	}
	switch a.Status {
	case session.NextActionPending, session.NextActionDone, session.NextActionSkipped, "":
	default:
		return "status must be pending, done, or skipped"
	}
	return ""
}

// parseAndValidateNextActions parses a JSON array of NextAction objects,
// enforces count/field-length/UUID caps, and defaults empty Status to pending.
// Returns the validated slice and an empty errMsg on success.
func parseAndValidateNextActions(raw string) ([]session.NextAction, string) {
	var actions []session.NextAction
	if err := json.Unmarshal([]byte(raw), &actions); err != nil {
		return nil, "invalid next_actions: must be a valid JSON array of next-action objects"
	}
	if len(actions) > maxNextActionItems {
		return nil, fmt.Sprintf("next_actions: at most %d items allowed", maxNextActionItems)
	}
	// F8: no uniqueness check previously existed either — two items sharing
	// the same step meant mark_next_action_done's "actions[i].Step == step"
	// scan (session/store.go MarkNextActionDone) would silently only ever
	// reach the first match, and the second item could never be marked done
	// on its own.
	seenSteps := make(map[int]bool, len(actions))
	for i, a := range actions {
		if reason := validateNextActionFields(a); reason != "" {
			return nil, fmt.Sprintf("next_actions[%d]: %s", i, reason)
		}
		if seenSteps[a.Step] {
			return nil, fmt.Sprintf("next_actions[%d]: duplicate step %d", i, a.Step)
		}
		seenSteps[a.Step] = true
		if actions[i].Status == "" {
			actions[i].Status = session.NextActionPending
		}
	}
	return actions, ""
}
