package mcp

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerBehaviorRuleTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"propose_behavior_rule",
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

	ms.AddTool(mcp.NewTool(
		"list_behavior_rules",
		mcp.WithDescription(
			"Lists persisted behavior rules, ordered by creation time descending. "+
				"Use status filter to view only proposed/active/rejected/deprecated rules.",
		),
		mcp.WithString("status",
			mcp.Description("Filter by status. One of: proposed, active, rejected, deprecated. Empty = all.")),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of rules to return. Default: 20, max: 100.")),
	), s.handleListBehaviorRules)

	ms.AddTool(mcp.NewTool(
		"apply_behavior_rules",
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

	ms.AddTool(mcp.NewTool(
		"deprecate_behavior_rule",
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

// behaviorRuleTextMaxRunes bounds Condition/Action on read — U13
// (2026-08-20-mcp-surface-spec.md). sanitizeRuleText already caps write-time
// length at 2000 runes (see the two sanitizeRuleText(..., 2000) calls
// below), so this read-time bound is length-equal, not tighter; what it adds
// is the boundary-marker neutralisation clipSafe performs that
// sanitizeRuleText does not.
const behaviorRuleTextMaxRunes = 2000

// wrapUntrustedBehaviorRule returns a copy of r with Condition/Action
// clipSafe'd (tools_context.go). Mirrors wrapUntrustedTask's (tools_gtd.go)
// copy-not-mutate contract. nil in, nil out.
func wrapUntrustedBehaviorRule(r *behaviorrule.BehaviorRule) *behaviorrule.BehaviorRule {
	if r == nil {
		return nil
	}
	out := *r
	out.Condition = clipSafe(r.Condition, behaviorRuleTextMaxRunes)
	out.Action = clipSafe(r.Action, behaviorRuleTextMaxRunes)
	return &out
}

// wrapUntrustedBehaviorRules maps wrapUntrustedBehaviorRule over a slice,
// preserving nil-vs-empty (list_behavior_rules already guards nil -> [] at
// its own call site before this runs).
func wrapUntrustedBehaviorRules(rules []*behaviorrule.BehaviorRule) []*behaviorrule.BehaviorRule {
	out := make([]*behaviorrule.BehaviorRule, len(rules))
	for i, r := range rules {
		out[i] = wrapUntrustedBehaviorRule(r)
	}
	return out
}

// handleProposeBehaviorRule creates a new behavior rule with status='proposed'.
func (s *Server) handleProposeBehaviorRule(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.behaviorRule == nil {
		return mcp.NewToolResultError("behavior_rule store not configured"), nil
	}
	args := req.GetArguments()

	condition := stringArg(args, "condition")
	if condition == "" {
		return mcp.NewToolResultError("condition is required"), nil
	}
	var err error
	condition, err = sanitizeRuleText(condition, 2000)
	if err != nil {
		return inputErrorResult("condition", err), nil
	}

	action := stringArg(args, "action")
	if action == "" {
		return mcp.NewToolResultError("action is required"), nil
	}
	action, err = sanitizeRuleText(action, 2000)
	if err != nil {
		return inputErrorResult("action", err), nil
	}

	sourceType := stringArg(args, "source_type")
	if !behaviorrule.AllowedSourceTypes[sourceType] {
		return mcp.NewToolResultError(fmt.Sprintf(
			"invalid source_type %q; must be one of: reflection, outcome, manual", sourceType,
		)), nil
	}

	confidence := floatArg(args, "confidence")
	if math.IsNaN(confidence) || math.IsInf(confidence, 0) {
		return mcp.NewToolResultError("confidence must be a finite number"), nil
	}
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
			return inputErrorResult("source_id: invalid UUID", err), nil
		}
		params.SourceID = &srcID
	}

	r, err := s.behaviorRule.Propose(ctx, params)
	if err != nil {
		return storeErrorResult("proposing behavior rule", err), nil
	}
	return jsonText(wrapUntrustedBehaviorRule(r))
}

// handleListBehaviorRules returns behavior rules, optionally filtered by status.
func (s *Server) handleListBehaviorRules(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.behaviorRule == nil {
		return mcp.NewToolResultError("behavior_rule store not configured"), nil
	}
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
		return storeErrorResult("listing behavior rules", err), nil
	}
	if rules == nil {
		rules = []*behaviorrule.BehaviorRule{}
	}
	return jsonText(wrapUntrustedBehaviorRules(rules))
}

// handleApplyBehaviorRules applies an outcome to a behavior rule.
func (s *Server) handleApplyBehaviorRules(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.behaviorRule == nil {
		return mcp.NewToolResultError("behavior_rule store not configured"), nil
	}
	args := req.GetArguments()

	ruleIDStr := stringArg(args, "rule_id")
	if ruleIDStr == "" {
		return mcp.NewToolResultError("rule_id is required"), nil
	}
	ruleID, err := uuid.Parse(ruleIDStr)
	if err != nil {
		return inputErrorResult("rule_id: invalid UUID", err), nil
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
		return storeErrorResult("applying outcome", err), nil
	}
	return jsonText(wrapUntrustedBehaviorRule(r))
}

// sanitizeRuleText validates and truncates rule text fields (condition, action).
// It rejects strings containing null bytes or ASCII control characters (other
// than tab), and enforces a maximum rune length per backend-security-design.md §5.4.
func sanitizeRuleText(s string, maxRunes int) (string, error) {
	for _, r := range s {
		if r == '\x00' || (r < 0x20 && r != '\t') {
			return "", fmt.Errorf("text contains forbidden control character")
		}
	}
	runes := []rune(s)
	if len(runes) > maxRunes {
		return "", fmt.Errorf("text exceeds %d characters", maxRunes)
	}
	return s, nil
}

// handleDeprecateBehaviorRule sets a behavior rule's status to 'deprecated'.
func (s *Server) handleDeprecateBehaviorRule(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.behaviorRule == nil {
		return mcp.NewToolResultError("behavior_rule store not configured"), nil
	}
	args := req.GetArguments()

	ruleIDStr := stringArg(args, "rule_id")
	if ruleIDStr == "" {
		return mcp.NewToolResultError("rule_id is required"), nil
	}
	ruleID, err := uuid.Parse(ruleIDStr)
	if err != nil {
		return inputErrorResult("rule_id: invalid UUID", err), nil
	}

	r, err := s.behaviorRule.Deprecate(ctx, ruleID)
	if err != nil {
		if errors.Is(err, behaviorrule.ErrNotFound) {
			return mcp.NewToolResultError("behavior rule not found"), nil
		}
		return storeErrorResult("deprecating behavior rule", err), nil
	}
	return jsonText(wrapUntrustedBehaviorRule(r))
}
