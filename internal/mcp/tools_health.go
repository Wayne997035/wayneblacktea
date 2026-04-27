package mcp

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/waynechen/wayneblacktea/internal/watchdog"
)

func (s *Server) registerHealthTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("system_health",
		mcp.WithDescription(
			"Returns a snapshot of the personal-OS state: in-progress task "+
				"count, stuck tasks (in_progress > 4h), pending proposals, "+
				"due reviews, recent MCP tool invocations. CALL when you want "+
				"to know if Claude has been forgetting to close out work.",
		),
		mcp.WithNumber("recent_calls", mcp.Description("How many recent tool calls to include (default 20)")),
		mcp.WithNumber("stuck_threshold_hours", mcp.Description("Tasks in_progress longer than this are flagged stuck (default 4)")),
	), s.handleSystemHealth)
}

// healthSnapshot is the JSON shape returned by system_health.
type healthSnapshot struct {
	GeneratedAt      time.Time           `json:"generated_at"`
	Workspace        string              `json:"workspace,omitempty"`
	Tasks            taskHealth          `json:"tasks"`
	PendingProposals proposalHealth      `json:"pending_proposals"`
	DueReviews       reviewHealth        `json:"due_reviews"`
	ToolCallSummary  map[string]int      `json:"tool_call_counts"`
	RecentCalls      []watchdog.ToolCall `json:"recent_calls"`
	ForgottenSignals []string            `json:"forgotten_signals,omitempty"`
}

type taskHealth struct {
	InProgress int      `json:"in_progress"`
	Stuck      int      `json:"stuck"`
	StuckIDs   []string `json:"stuck_ids,omitempty"`
}

type proposalHealth struct {
	Pending int `json:"pending"`
}

type reviewHealth struct {
	Due int `json:"due"`
}

func (s *Server) handleSystemHealth(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	recentN := int(numberArg(args, "recent_calls"))
	if recentN <= 0 {
		recentN = 20
	}
	stuckHours := int(numberArg(args, "stuck_threshold_hours"))
	if stuckHours <= 0 {
		stuckHours = 4
	}

	snap := healthSnapshot{
		GeneratedAt:     time.Now().UTC(),
		ToolCallSummary: s.watchdog.CountByTool(),
		RecentCalls:     s.watchdog.Recent(recentN),
	}
	snap.Workspace = workspaceIDString(s.gtd.WorkspaceID())

	tasks, err := s.gtd.Tasks(ctx, nil)
	if err == nil {
		stuckCutoff := time.Now().Add(-time.Duration(stuckHours) * time.Hour)
		for _, t := range tasks {
			if t.Status != "in_progress" {
				continue
			}
			snap.Tasks.InProgress++
			if t.UpdatedAt.Valid && t.UpdatedAt.Time.Before(stuckCutoff) {
				snap.Tasks.Stuck++
				snap.Tasks.StuckIDs = append(snap.Tasks.StuckIDs, t.ID.String())
			}
		}
	}

	if proposals, err := s.proposal.ListPending(ctx); err == nil {
		snap.PendingProposals.Pending = len(proposals)
	}

	if n, err := s.learning.CountDueReviews(ctx); err == nil {
		snap.DueReviews.Due = n
	}

	snap.ForgottenSignals = detectForgottenSignals(snap, s.watchdog)

	return jsonText(snap)
}

// detectForgottenSignals applies a few cheap heuristics to point out
// likely Claude omissions. Each signal is human-readable and short.
func detectForgottenSignals(snap healthSnapshot, w *watchdog.Watchdog) []string {
	var signals []string

	if snap.Tasks.Stuck > 0 {
		signals = append(signals,
			"There are stuck in-progress tasks. Claude likely forgot to call complete_task after finishing.")
	}

	if snap.PendingProposals.Pending >= 5 {
		signals = append(signals,
			"5+ pending proposals queued. Either ask Claude to confirm/reject them, or it stopped triaging.")
	}

	counts := snap.ToolCallSummary
	addTaskCount := counts["add_task"] + counts["confirm_plan"]
	completeCount := counts["complete_task"]
	if addTaskCount >= 3 && completeCount == 0 {
		signals = append(signals,
			"Several tasks added in this session but none completed. Are any actually done?")
	}

	// Decision logged without a session-start get_today_context call:
	// flag because it usually means Claude skipped MANDATORY session-start
	// recall. (Cosmetic-ish — only fire when there were enough decisions.)
	decisionCount := counts["log_decision"] + counts["confirm_plan"]
	if decisionCount >= 2 && w.LastSuccessful("get_today_context").IsZero() {
		signals = append(signals,
			"Decisions logged but get_today_context never called this session — Claude likely skipped session-start recall.")
	}

	return signals
}

// workspaceIDString turns the store's pgtype.UUID workspace into a string,
// or returns "(unscoped)" when WORKSPACE_ID is unset.
func workspaceIDString(ws pgtype.UUID) string {
	if !ws.Valid {
		return "(unscoped)"
	}
	// pgtype.UUID stores the 16 raw bytes; render as canonical 8-4-4-4-12.
	b := ws.Bytes
	return hex.EncodeToString(b[0:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:16])
}
