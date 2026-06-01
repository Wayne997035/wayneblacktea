package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerBehaviorRuleTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("propose_behavior_rule",
		mcp.WithDescription(
			"Proposes a new behavior rule with status='proposed'. "+
				"The rule enters the proposal queue and must be promoted via "+
				"apply_behavior_rules (outcome='success') before it becomes 'active'. "+
				"Use to encode lessons from reflections or outcomes as actionable rules.",
		),
		mcp.WithString("condition",
			mcp.Required(),
			mcp.Description("When-clause describing the condition that triggers this rule. Required.")),
		mcp.WithString("action",
			mcp.Required(),
			mcp.Description("What action to take when the condition is met. Required.")),
		mcp.WithString("source_type",
			mcp.Required(),
			mcp.Description("Source of the rule. One of: reflection, outcome, manual.")),
		mcp.WithString("source_id",
			mcp.Description("UUID of the source entity (reflection or outcome ID). Optional.")),
		mcp.WithNumber("confidence",
			mcp.Description("Initial confidence score between 0.0 and 1.0. Defaults to 0.50 when absent or out of range.")),
	), s.handleProposeBehaviorRule)

	ms.AddTool(mcp.NewTool("list_behavior_rules",
		mcp.WithDescription(
			"Lists persisted behavior rules, ordered by creation time descending. "+
				"Use status filter to view only proposed/active/rejected/deprecated rules.",
		),
		mcp.WithString("status",
			mcp.Description("Filter by status. One of: proposed, active, rejected, deprecated. Empty = all.")),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of rules to return. Default: 20, max: 100.")),
	), s.handleListBehaviorRules)

	ms.AddTool(mcp.NewTool("apply_behavior_rules",
		mcp.WithDescription(
			"Applies an outcome to a behavior rule, adjusting confidence and conditionally "+
				"transitioning status. outcome='success' on a proposed rule transitions it to 'active' "+
				"and increments confidence by 0.05 (capped at 1.00). outcome='failure' decrements "+
				"confidence by 0.10 (floored at 0.00) without changing status.",
		),
		mcp.WithString("rule_id",
			mcp.Required(),
			mcp.Description("UUID of the behavior rule to update. Required.")),
		mcp.WithString("outcome",
			mcp.Required(),
			mcp.Description("Outcome to apply. One of: success, failure. Required.")),
	), s.handleApplyBehaviorRules)

	ms.AddTool(mcp.NewTool("deprecate_behavior_rule",
		mcp.WithDescription(
			"Sets a behavior rule's status to 'deprecated'. Idempotent: "+
				"already-deprecated rules return success without error. "+
				"Deprecated rules are retained for 365 days then pruned by the daily TTL job.",
		),
		mcp.WithString("rule_id",
			mcp.Required(),
			mcp.Description("UUID of the behavior rule to deprecate. Required.")),
	), s.handleDeprecateBehaviorRule)
}

// handleProposeBehaviorRule creates a new behavior rule with status='proposed'.
func (s *Server) handleProposeBehaviorRule(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	condition := stringArg(args, "condition")
	if condition == "" {
		return mcp.NewToolResultError("condition is required"), nil
	}

	action := stringArg(args, "action")
	if action == "" {
		return mcp.NewToolResultError("action is required"), nil
	}

	sourceType := stringArg(args, "source_type")
	if !behaviorrule.AllowedSourceTypes[sourceType] {
		return mcp.NewToolResultError(fmt.Sprintf(
			"invalid source_type %q; must be one of: reflection, outcome, manual", sourceType,
		)), nil
	}

	confidence := float64(numberArg(args, "confidence"))
	if confidence <= 0 || confidence > 1.0 {
		confidence = 0.50
	}

	params := behaviorrule.CreateParams{
		WorkspaceID: s.workspaceUUID(),
		Condition:   condition,
		Action:      action,
		SourceType:  sourceType,
		Confidence:  confidence,
	}

	srcIDStr := stringArg(args, "source_id")
	if srcIDStr != "" {
		srcID, err := uuid.Parse(srcIDStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("source_id: invalid UUID: %v", err)), nil
		}
		params.SourceID = &srcID
	}

	r, err := s.behaviorRule.Propose(ctx, params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("proposing behavior rule: %v", err)), nil
	}
	return jsonText(r)
}

// handleListBehaviorRules returns behavior rules, optionally filtered by status.
func (s *Server) handleListBehaviorRules(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	params := behaviorrule.ListParams{
		WorkspaceID: s.workspaceUUID(),
	}

	if st := stringArg(args, "status"); st != "" {
		if !behaviorrule.AllowedStatuses[st] {
			return mcp.NewToolResultError(fmt.Sprintf(
				"invalid status %q; must be one of: proposed, active, rejected, deprecated", st,
			)), nil
		}
		params.Status = &st
	}

	limit := int(numberArg(args, "limit"))
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	params.Limit = limit

	rules, err := s.behaviorRule.List(ctx, params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("listing behavior rules: %v", err)), nil
	}
	if rules == nil {
		rules = []*behaviorrule.BehaviorRule{}
	}
	return jsonText(rules)
}

// handleApplyBehaviorRules applies an outcome to a behavior rule.
func (s *Server) handleApplyBehaviorRules(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	ruleIDStr := stringArg(args, "rule_id")
	if ruleIDStr == "" {
		return mcp.NewToolResultError("rule_id is required"), nil
	}
	ruleID, err := uuid.Parse(ruleIDStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("rule_id: invalid UUID: %v", err)), nil
	}

	outcome := stringArg(args, "outcome")
	if outcome != "success" && outcome != "failure" {
		return mcp.NewToolResultError("outcome must be 'success' or 'failure'"), nil
	}

	r, err := s.behaviorRule.ApplyOutcome(ctx, ruleID, outcome)
	if err != nil {
		if errors.Is(err, behaviorrule.ErrNotFound) {
			return mcp.NewToolResultError("behavior rule not found"), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("applying outcome: %v", err)), nil
	}
	return jsonText(r)
}

// handleDeprecateBehaviorRule sets a behavior rule's status to 'deprecated'.
func (s *Server) handleDeprecateBehaviorRule(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	ruleIDStr := stringArg(args, "rule_id")
	if ruleIDStr == "" {
		return mcp.NewToolResultError("rule_id is required"), nil
	}
	ruleID, err := uuid.Parse(ruleIDStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("rule_id: invalid UUID: %v", err)), nil
	}

	r, err := s.behaviorRule.Deprecate(ctx, ruleID)
	if err != nil {
		if errors.Is(err, behaviorrule.ErrNotFound) {
			return mcp.NewToolResultError("behavior rule not found"), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("deprecating behavior rule: %v", err)), nil
	}
	return jsonText(r)
}
