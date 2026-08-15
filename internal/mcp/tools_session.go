package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

	p := session.HandoffParams{
		Intent:         intent,
		RepoName:       stringArg(args, "repo_name"),
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

	stepN := numberArg(args, "step")
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
	return jsonText(buildPendingHandoffView(h))
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
	for i, a := range actions {
		if reason := validateNextActionFields(a); reason != "" {
			return nil, fmt.Sprintf("next_actions[%d]: %s", i, reason)
		}
		if actions[i].Status == "" {
			actions[i].Status = session.NextActionPending
		}
	}
	return actions, ""
}
