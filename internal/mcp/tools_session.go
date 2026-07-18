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
		if a.Title == "" {
			return nil, fmt.Sprintf("next_actions[%d]: title is required", i)
		}
		if len([]rune(a.Title)) > maxNextActionFieldLen {
			return nil, fmt.Sprintf("next_actions[%d]: title exceeds %d characters", i, maxNextActionFieldLen)
		}
		if len([]rune(a.Command)) > maxNextActionFieldLen {
			return nil, fmt.Sprintf("next_actions[%d]: command exceeds %d characters", i, maxNextActionFieldLen)
		}
		if reason := checkCommandField("command", a.Command); reason != "" {
			return nil, fmt.Sprintf("next_actions[%d]: %s", i, reason)
		}
		if len([]rune(a.Expected)) > maxNextActionFieldLen {
			return nil, fmt.Sprintf("next_actions[%d]: expected exceeds %d characters", i, maxNextActionFieldLen)
		}
		if reason := checkCommandField("expected", a.Expected); reason != "" {
			return nil, fmt.Sprintf("next_actions[%d]: %s", i, reason)
		}
		if a.RefTaskID != nil && *a.RefTaskID != "" {
			if _, err := uuid.Parse(*a.RefTaskID); err != nil {
				return nil, fmt.Sprintf("next_actions[%d]: ref_task_id must be a valid UUID", i)
			}
		}
		switch a.Status {
		case session.NextActionPending, session.NextActionDone, session.NextActionSkipped, "":
		default:
			return nil, fmt.Sprintf("next_actions[%d]: status must be pending, done, or skipped", i)
		}
		if actions[i].Status == "" {
			actions[i].Status = session.NextActionPending
		}
	}
	return actions, ""
}
