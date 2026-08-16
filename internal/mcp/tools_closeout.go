package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// closeoutReport is the JSON payload returned by closeout_session_check.
type closeoutReport struct {
	GeneratedAt      time.Time       `json:"generated_at"`
	OpenTaskCount    int             `json:"open_task_count"`
	StuckTasks       []stuckTaskInfo `json:"stuck_tasks,omitempty"`
	PendingProposals int             `json:"pending_proposals"`
	HandoffSet       bool            `json:"handoff_set"`
	HandoffSummary   string          `json:"handoff_summary,omitempty"`
	// HandoffSummaryNotice is set alongside HandoffSummary (never on its
	// own) — round-4 PR #158 security review s-3-1: mark_next_action_done,
	// the handoff resource, and recall's episodic branch all carry
	// storedDataNotice in the same payload as their fenced handoff text,
	// but closeout_session_check and analyze_agent_behavior's stale_handoff
	// detection (tools_watchdog.go) used to carry only the fence with no
	// notice — the same stored data got two different treatments depending
	// on which of the five exit points read it. This field closes that gap
	// for this exit point.
	HandoffSummaryNotice string   `json:"handoff_summary_notice,omitempty"`
	CompletionCandidates int      `json:"completion_candidates"`
	Actions              []string `json:"next_actions"`
	Clean                bool     `json:"clean"`
}

// stuckTaskInfo is a brief summary of an in_progress task older than 7 days.
type stuckTaskInfo struct {
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
}

// stuckTaskThreshold is how long a task must be in_progress before it is
// considered stuck by the closeout check.
const stuckTaskThreshold = 7 * 24 * time.Hour

// registerCloseoutTools registers the closeout_session_check MCP tool.
func (s *Server) registerCloseoutTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"closeout_session_check",
		mcp.WithDescription(
			"Aggregates session-end checks into one actionable closeout report: open in_progress tasks, "+
				"stuck tasks (in_progress > 7 days), pending proposals awaiting user resolution, latest "+
				"session handoff status, and completion candidates. Also writes a summary line to activity_log. "+
				"Call at session end to verify nothing was left open.",
		),
	), s.handleCloseoutSessionCheck)
}

// handleCloseoutSessionCheck implements the closeout_session_check tool.
func (s *Server) handleCloseoutSessionCheck(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	report := closeoutReport{
		GeneratedAt: time.Now().UTC(),
	}

	s.fillCloseoutTasks(ctx, &report)
	s.fillCloseoutProposals(ctx, &report)
	s.fillCloseoutHandoff(ctx, &report)
	s.fillCloseoutCandidates(ctx, &report)

	report.Actions = buildCloseoutActions(report)
	report.Clean = len(report.Actions) == 0

	// Write summary to activity_log (best-effort; ignore error).
	summary := fmt.Sprintf(
		"closeout_check: open=%d stuck=%d proposals=%d handoff=%v candidates=%d clean=%v",
		report.OpenTaskCount, len(report.StuckTasks), report.PendingProposals,
		report.HandoffSet, report.CompletionCandidates, report.Clean,
	)
	// This LogActivity call uses the same Store method that makes the
	// standalone log_activity tool mutating, but closeout_session_check
	// itself is intentionally NOT in discipline.MutatingTools — it's listed
	// under discipline.DeliberatelyExcludedTools' "System-generated
	// cache/candidate writes" category (internal/discipline/discipline.go):
	// the actor is hardcoded to "system", the summary is entirely
	// server-computed from prior read-only aggregation above, and there is
	// no caller-controlled field, so flagging this write as drift would be
	// noise rather than signal.
	_ = s.gtd.LogActivity(ctx, "system", "closeout_session_check", nil, summary)

	return jsonText(report)
}

// fillCloseoutTasks loads tasks and classifies them as open or stuck.
func (s *Server) fillCloseoutTasks(ctx context.Context, report *closeoutReport) {
	tasks, err := s.gtd.Tasks(ctx, nil)
	if err != nil {
		return
	}
	for _, t := range tasks {
		classifyTaskForCloseout(t, report)
	}
}

// classifyTaskForCloseout updates report counters for a single task.
func classifyTaskForCloseout(t db.Task, report *closeoutReport) {
	if t.Status != taskStatusPending && t.Status != taskStatusInProgress {
		return
	}
	report.OpenTaskCount++
	if t.Status != taskStatusInProgress || !t.CreatedAt.Valid {
		return
	}
	if time.Since(t.CreatedAt.Time) > stuckTaskThreshold {
		report.StuckTasks = append(report.StuckTasks, stuckTaskInfo{
			Title:     t.Title,
			CreatedAt: t.CreatedAt.Time.UTC(),
		})
	}
}

// fillCloseoutProposals counts pending proposals into the report.
func (s *Server) fillCloseoutProposals(ctx context.Context, report *closeoutReport) {
	proposals, err := s.proposal.ListPending(ctx)
	if err != nil {
		return
	}
	report.PendingProposals = len(proposals)
}

// fillCloseoutHandoff checks whether a session handoff has been set.
//
// HandoffSummary goes through safeSessionHandoff.hardenedIntent(), not
// sanitize.Notes(handoff.Intent) as before: sanitize.Notes strips control
// characters and caps length but does not neutralise forged boundary
// markers, so a poisoned Intent could still fake an escape from whatever
// fence a reader expects around stored data (PR #158 chokepoint — same
// threat class as recallEpisodic's episodic branch, tools_procedural.go).
// hardenedIntent shares the exact clip+fence treatment
// buildHardenedHandoffView gives Intent, so this tool and mark_next_action_
// done/the handoff resource never disagree on what "hardened" means for the
// same field.
func (s *Server) fillCloseoutHandoff(ctx context.Context, report *closeoutReport) {
	handoff, err := s.session.LatestHandoff(ctx)
	if err != nil || handoff == nil {
		return // ErrNotFound or real error: HandoffSet stays false (advisory).
	}
	report.HandoffSet = true
	report.HandoffSummary = newSafeSessionHandoff(handoff).hardenedIntent()
	report.HandoffSummaryNotice = storedDataNotice
}

// fillCloseoutCandidates counts pending completion candidates into the report.
func (s *Server) fillCloseoutCandidates(ctx context.Context, report *closeoutReport) {
	if s.completionCandidates == nil {
		return
	}
	candidates, err := s.completionCandidates.ListPendingCandidates(ctx, nil)
	if err != nil {
		return
	}
	report.CompletionCandidates = len(candidates)
}

// buildCloseoutActions assembles the next-actions list from a filled report.
// Returns an empty non-nil slice so JSON encodes as [] not null.
func buildCloseoutActions(report closeoutReport) []string {
	actions := make([]string, 0)
	if len(report.StuckTasks) > 0 {
		titles := make([]string, 0, len(report.StuckTasks))
		for _, st := range report.StuckTasks {
			titles = append(titles, st.Title)
		}
		actions = append(
			actions,
			fmt.Sprintf("Complete or cancel %d stuck task(s): %s",
				len(report.StuckTasks), strings.Join(titles, ", ")),
		)
	}
	if report.PendingProposals > 0 {
		actions = append(
			actions,
			fmt.Sprintf("Resolve %d pending proposal(s) via confirm_proposals", report.PendingProposals),
		)
	}
	if !report.HandoffSet {
		actions = append(actions, "Call set_session_handoff before ending session")
	}
	if report.CompletionCandidates > 0 {
		actions = append(
			actions,
			fmt.Sprintf("Review %d completion candidate(s) via detect_completion_candidates", report.CompletionCandidates),
		)
	}
	return actions
}
