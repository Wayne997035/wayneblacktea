package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// completionCandidateStore is the narrow store interface used by MCP dashboard tools.
type completionCandidateStore interface {
	DetectAndUpsert(ctx context.Context, p completioncandidate.DetectParams) ([]completioncandidate.Candidate, error)
	ListPendingCandidates(ctx context.Context, workspaceID *uuid.UUID) ([]completioncandidate.Candidate, error)
}

// completioncandidate.Store must satisfy completionCandidateStore (checked at call sites).

// registerDashboardTools registers the dashboard-automation MCP tools.
func (s *Server) registerDashboardTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("detect_completion_candidates",
		mcp.WithDescription(
			"Scans tasks and activity_log to surface tasks that appear done but GTD status is "+
				"still pending/in_progress. Read-only for tasks — writes to completion_candidates "+
				"table only. Returns candidate list.",
		),
		mcp.WithNumber("stale_threshold_hours",
			mcp.Description("Hours of inactivity to mark in_progress task as stale (default 24, range 1-168)"),
			mcp.Min(1),
			mcp.Max(168),
		),
		mcp.WithNumber("lookback_days",
			mcp.Description("Days of activity_log to scan (default 7, max 30)"),
			mcp.Min(1),
			mcp.Max(30),
		),
	), s.handleDetectCompletionCandidates)

	ms.AddTool(mcp.NewTool("reconcile_dashboard",
		mcp.WithDescription(
			"Runs all completion-candidate detection rules and returns a full automation-health "+
				"snapshot including stale tasks, candidates, proposal backlog, and missing-handoff "+
				"status. Does NOT mutate tasks.",
		),
	), s.handleReconcileDashboard)
}

// dashboardCandidateStore returns the completion candidate store. It may be nil
// when the completioncandidate domain is not wired. Tools check for nil and
// return an informative message.
func (s *Server) dashboardCandidateStore() completionCandidateStore {
	return s.completionCandidates
}

// handleDetectCompletionCandidates implements the detect_completion_candidates tool.
func (s *Server) handleDetectCompletionCandidates(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	staleHours := int(floatArg(args, "stale_threshold_hours"))
	if staleHours <= 0 {
		staleHours = 24
	}
	if staleHours > 168 {
		staleHours = 168
	}

	lookbackDays := int(floatArg(args, "lookback_days"))
	if lookbackDays <= 0 {
		lookbackDays = 7
	}
	if lookbackDays > 30 {
		lookbackDays = 30
	}

	store := s.dashboardCandidateStore()
	if store == nil {
		return mcp.NewToolResultText(`{"candidates":[],"message":"completion candidate store not configured"}`), nil
	}

	candidates, err := store.DetectAndUpsert(ctx, completioncandidate.DetectParams{
		WorkspaceID:    s.workspaceID,
		StaleThreshold: time.Duration(staleHours) * time.Hour,
		LookbackDays:   lookbackDays,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("detect_completion_candidates failed: %v", err)), nil
	}

	type candidateSummary struct {
		ID           string   `json:"id"`
		TaskID       string   `json:"task_id"`
		Reason       string   `json:"reason"`
		Confidence   string   `json:"confidence"`
		EvidenceRefs []string `json:"evidence_refs"`
		Status       string   `json:"status"`
		DetectedAt   string   `json:"detected_at"`
	}

	out := make([]candidateSummary, 0, len(candidates))
	for _, c := range candidates {
		refs := c.EvidenceRefs
		if refs == nil {
			refs = []string{}
		}
		out = append(out, candidateSummary{
			ID:           c.ID.String(),
			TaskID:       c.TaskID.String(),
			Reason:       string(c.Reason),
			Confidence:   string(c.Confidence),
			EvidenceRefs: refs,
			Status:       string(c.Status),
			DetectedAt:   c.DetectedAt.UTC().Format(time.RFC3339),
		})
	}

	return jsonText(map[string]any{
		"candidates": out,
		"count":      len(out),
	})
}

// handleReconcileDashboard implements the reconcile_dashboard tool.
func (s *Server) handleReconcileDashboard(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	store := s.dashboardCandidateStore()
	if store == nil {
		return mcp.NewToolResultText(`{"message":"completion candidate store not configured"}`), nil
	}

	// Run detection with defaults.
	candidates, err := store.DetectAndUpsert(ctx, completioncandidate.DetectParams{
		WorkspaceID:    s.workspaceID,
		StaleThreshold: 24 * time.Hour,
		LookbackDays:   7,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("reconcile_dashboard detection failed: %v", err)), nil
	}

	// List all pending.
	pending, err := store.ListPendingCandidates(ctx, s.workspaceID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("reconcile_dashboard list pending failed: %v", err)), nil
	}

	// Count by reason.
	staleCount := 0
	for _, c := range candidates {
		if c.Reason == completioncandidate.ReasonStaleInProgress {
			staleCount++
		}
	}

	// Proposal backlog.
	proposalBacklog := 0
	if s.proposal != nil {
		if proposals, pErr := s.proposal.ListPending(ctx); pErr == nil {
			proposalBacklog = len(proposals)
		}
	}

	// Missing handoff: check if latest handoff is unresolved and older than 48h.
	missingHandoff := false
	if s.session != nil {
		handoff, hErr := s.session.LatestHandoff(ctx)
		if hErr == nil && handoff != nil && !handoff.ResolvedAt.Valid {
			if handoff.CreatedAt.Valid && time.Since(handoff.CreatedAt.Time) > 48*time.Hour {
				missingHandoff = true
			}
		}
	}

	summary := fmt.Sprintf(
		"Automation health: %d candidates detected (%d stale in_progress), "+
			"%d pending proposals, missing_handoff=%v",
		len(candidates), staleCount, proposalBacklog, missingHandoff,
	)

	return jsonText(map[string]any{
		"summary":            summary,
		"detected_count":     len(candidates),
		"stale_in_progress":  staleCount,
		"pending_candidates": len(pending),
		"proposal_backlog":   proposalBacklog,
		"missing_handoff":    missingHandoff,
	})
}
