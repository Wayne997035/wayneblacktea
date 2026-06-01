package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	proposalpkg "github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/watchdog"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// watchdogMetaHealth is the WatchdogMeta sub-struct added to healthSnapshot.
type watchdogMetaHealth struct {
	UnresolvedCount  int      `json:"unresolved_count"`
	RecentEventTypes []string `json:"recent_event_types,omitempty"`
}

// registerWatchdogTools registers the three watchdog meta-cognition tools:
//   - analyze_agent_behavior  – runs all 8 detections and inserts findings
//   - detect_unclosed_loops   – lists open (unresolved) discipline events
//   - mark_loop_resolved      – resolves a specific event by ID
func (s *Server) registerWatchdogTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("analyze_agent_behavior",
		mcp.WithDescription(
			"Runs 8 self-monitoring detections against live store data "+
				"(stuck tasks, unlogged decisions, stale handoffs, etc.). "+
				"Inserts findings into the discipline_events_m8 table and "+
				"returns a JSON summary of inserted events.",
		),
		mcp.WithNumber("stuck_threshold_hours",
			mcp.Description("Tasks in_progress longer than this are flagged stuck (default 4)")),
	), s.handleAnalyzeAgentBehavior)

	ms.AddTool(mcp.NewTool("detect_unclosed_loops",
		mcp.WithDescription(
			"Returns all open discipline_events_m8 rows (resolved_at IS NULL) "+
				"scoped to the current workspace. Each entry is a self-monitoring "+
				"signal that has not yet been acknowledged.",
		),
	), s.handleDetectUnclosedLoops)

	ms.AddTool(mcp.NewTool("mark_loop_resolved",
		mcp.WithDescription(
			"Marks a discipline event as resolved by its UUID. "+
				"Call this after you have addressed the underlying issue "+
				"surfaced by analyze_agent_behavior.",
		),
		mcp.WithString("event_id",
			mcp.Description("UUID of the discipline_events_m8 row to resolve"),
			mcp.Required()),
	), s.handleMarkLoopResolved)
}

// ---- analyze_agent_behavior ----

// analyzeAgentBehaviorResult is the JSON shape returned by
// analyze_agent_behavior.
type analyzeAgentBehaviorResult struct {
	RunAt         time.Time              `json:"run_at"`
	TotalInserted int                    `json:"total_inserted"`
	Findings      []agentBehaviorFinding `json:"findings"`
}

type agentBehaviorFinding struct {
	EventType string          `json:"event_type"`
	Severity  string          `json:"severity"`
	Detail    json.RawMessage `json:"detail"`
}

func (s *Server) handleAnalyzeAgentBehavior(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.disciplineEventStore == nil {
		return mcp.NewToolResultError("discipline_event_store not configured"), nil
	}

	args := req.GetArguments()
	stuckHours := int(numberArg(args, "stuck_threshold_hours"))
	if stuckHours <= 0 {
		stuckHours = 4
	}
	if stuckHours > 168 {
		stuckHours = 168
	}

	wsID := s.workspaceID
	var findings []agentBehaviorFinding

	findings = append(findings, s.detectStuckTasks(ctx, wsID, stuckHours)...)
	findings = append(findings, s.detectUnloggedDecisions(ctx, wsID)...)
	findings = append(findings, s.detectPlanNoTask(ctx, wsID)...)
	findings = append(findings, s.detectProposalFail(ctx, wsID)...)
	findings = append(findings, s.detectStaleHandoffs(ctx, wsID)...)
	findings = append(findings, s.detectRepeatedCorrections(ctx, wsID)...)
	findings = append(findings, s.detectTaskNoOutcome(ctx, wsID)...)
	findings = append(findings, s.detectDecisionNoReflection(ctx, wsID)...)

	result := analyzeAgentBehaviorResult{
		RunAt:         time.Now().UTC(),
		TotalInserted: len(findings),
		Findings:      findings,
	}
	if result.Findings == nil {
		result.Findings = []agentBehaviorFinding{}
	}
	return jsonText(result)
}

// insertWatchdogEvent is a helper used by all detection methods.
func (s *Server) insertWatchdogEvent(
	ctx context.Context,
	wsID *uuid.UUID,
	evType watchdog.EventType,
	severity watchdog.Severity,
	detail json.RawMessage,
) bool {
	if err := s.disciplineEventStore.Insert(ctx, watchdog.InsertParams{
		WorkspaceID: wsID,
		EventType:   evType,
		Severity:    severity,
		Detail:      detail,
	}); err != nil {
		slog.Warn("analyze_agent_behavior: Insert failed",
			"event_type", string(evType), "error", err)
		return false
	}
	return true
}

// Detection #1: stuck_task — tasks in_progress older than threshold.
func (s *Server) detectStuckTasks(ctx context.Context, wsID *uuid.UUID, stuckHours int) []agentBehaviorFinding {
	if s.gtd == nil {
		return nil
	}
	tasks, err := s.gtd.Tasks(ctx, nil)
	if err != nil {
		slog.Warn("analyze_agent_behavior: gtd.Tasks failed", "error", err)
		return nil
	}
	stuckCutoff := time.Now().Add(-time.Duration(stuckHours) * time.Hour)
	var out []agentBehaviorFinding
	for _, t := range tasks {
		if t.Status != taskStatusInProgress {
			continue
		}
		if !t.UpdatedAt.Valid || !t.UpdatedAt.Time.Before(stuckCutoff) {
			continue
		}
		detail, _ := json.Marshal(map[string]any{
			"task_id":     t.ID.String(),
			"title":       t.Title,
			"updated_at":  t.UpdatedAt.Time.Format(time.RFC3339),
			"stuck_hours": stuckHours,
		})
		if s.insertWatchdogEvent(ctx, wsID, watchdog.EventTypeStuckTask, watchdog.SeverityWarn, detail) {
			out = append(out, agentBehaviorFinding{
				EventType: string(watchdog.EventTypeStuckTask),
				Severity:  string(watchdog.SeverityWarn),
				Detail:    detail,
			})
		}
	}
	return out
}

// Detection #2: unlogged_decision — mutating calls without preceding decision.
func (s *Server) detectUnloggedDecisions(ctx context.Context, wsID *uuid.UUID) []agentBehaviorFinding {
	if s.discipline == nil {
		return nil
	}
	since := time.Now().Add(-24 * time.Hour)
	mutating, err := s.discipline.RecentMutating(ctx, since, 200)
	if err != nil {
		slog.Warn("analyze_agent_behavior: RecentMutating failed", "error", err)
		return nil
	}
	var out []agentBehaviorFinding
	for _, ev := range mutating {
		decisions, decErr := s.discipline.RecentDecisionTimes(ctx, ev.SessionID, since.Add(-driftWindow))
		if decErr != nil {
			continue
		}
		if hasDecisionInWindow(ev.ObservedAt, decisions) {
			continue
		}
		detail, _ := json.Marshal(map[string]any{
			"tool_name":   ev.ToolName,
			"session_id":  ev.SessionID,
			"observed_at": ev.ObservedAt.Format(time.RFC3339),
		})
		if s.insertWatchdogEvent(ctx, wsID, watchdog.EventTypeUnloggedDecision, watchdog.SeverityWarn, detail) {
			out = append(out, agentBehaviorFinding{
				EventType: string(watchdog.EventTypeUnloggedDecision),
				Severity:  string(watchdog.SeverityWarn),
				Detail:    detail,
			})
		}
	}
	return out
}

// Detection #3: plan_no_task — pending decision proposals older than 48h.
func (s *Server) detectPlanNoTask(ctx context.Context, wsID *uuid.UUID) []agentBehaviorFinding {
	if s.proposal == nil {
		return nil
	}
	proposals, err := s.proposal.ListAll(ctx, "decision", 200)
	if err != nil {
		slog.Warn("analyze_agent_behavior: ListAll proposals failed", "error", err)
		return nil
	}
	cutoff48h := time.Now().Add(-48 * time.Hour)
	var out []agentBehaviorFinding
	for _, p := range proposals {
		if p.Status != "pending" {
			continue
		}
		if !p.CreatedAt.Valid || !p.CreatedAt.Time.Before(cutoff48h) {
			continue
		}
		detail, _ := json.Marshal(map[string]any{
			"proposal_id": p.ID.String(),
			"type":        p.Type,
			"status":      p.Status,
			"created_at":  p.CreatedAt.Time.Format(time.RFC3339),
		})
		if s.insertWatchdogEvent(ctx, wsID, watchdog.EventTypePlanNoTask, watchdog.SeverityInfo, detail) {
			out = append(out, agentBehaviorFinding{
				EventType: string(watchdog.EventTypePlanNoTask),
				Severity:  string(watchdog.SeverityInfo),
				Detail:    detail,
			})
		}
	}
	return out
}

// Detection #4: proposal_fail — rejected proposals in past 7 days by reason.
func (s *Server) detectProposalFail(ctx context.Context, wsID *uuid.UUID) []agentBehaviorFinding {
	if s.proposal == nil {
		return nil
	}
	proposalTypes := []proposalpkg.Type{
		proposalpkg.TypeGoal,
		proposalpkg.TypeProject,
		proposalpkg.TypeTask,
		proposalpkg.TypeConcept,
		proposalpkg.TypeKnowledge,
		proposalpkg.TypePlaybook,
		proposalpkg.TypeDecision,
	}
	var proposals []db.PendingProposal
	for _, typ := range proposalTypes {
		rows, err := s.proposal.ListAll(ctx, string(typ), 200)
		if err != nil {
			slog.Warn("analyze_agent_behavior: ListAll proposals (fail) failed",
				"type", string(typ), "error", err)
			continue
		}
		proposals = append(proposals, rows...)
	}
	cutoff7d := time.Now().Add(-7 * 24 * time.Hour)
	reasonCounts := make(map[string]int)
	for _, p := range proposals {
		if p.Status != "rejected" {
			continue
		}
		if !p.ResolvedAt.Valid || !p.ResolvedAt.Time.After(cutoff7d) {
			continue
		}
		reason := ""
		if p.Reason.Valid {
			reason = p.Reason.String
		}
		if reason == "" {
			reason = "unspecified"
		}
		reasonCounts[reason]++
	}
	if len(reasonCounts) == 0 {
		return nil
	}
	detail, _ := json.Marshal(map[string]any{
		"reason_counts": reasonCounts,
		"window_days":   7,
	})
	if s.insertWatchdogEvent(ctx, wsID, watchdog.EventTypeProposalFail, watchdog.SeverityInfo, detail) {
		return []agentBehaviorFinding{{
			EventType: string(watchdog.EventTypeProposalFail),
			Severity:  string(watchdog.SeverityInfo),
			Detail:    detail,
		}}
	}
	return nil
}

// Detection #5: stale_handoff — session handoffs not resolved within 7 days.
func (s *Server) detectStaleHandoffs(ctx context.Context, wsID *uuid.UUID) []agentBehaviorFinding {
	if s.session == nil {
		return nil
	}
	since7d := time.Now().Add(-7 * 24 * time.Hour)
	handoffs, err := s.session.HandoffsSince(ctx, since7d, 100)
	if err != nil {
		slog.Warn("analyze_agent_behavior: HandoffsSince failed", "error", err)
		return nil
	}
	staleThreshold := time.Now().Add(-7 * 24 * time.Hour)
	var out []agentBehaviorFinding
	for _, h := range handoffs {
		if h.ResolvedAt.Valid {
			continue
		}
		if !h.CreatedAt.Valid || !h.CreatedAt.Time.Before(staleThreshold) {
			continue
		}
		detail, _ := json.Marshal(map[string]any{
			"handoff_id": h.ID.String(),
			"intent":     h.Intent,
			"created_at": h.CreatedAt.Time.Format(time.RFC3339),
		})
		if s.insertWatchdogEvent(ctx, wsID, watchdog.EventTypeStaleHandoff, watchdog.SeverityWarn, detail) {
			out = append(out, agentBehaviorFinding{
				EventType: string(watchdog.EventTypeStaleHandoff),
				Severity:  string(watchdog.SeverityWarn),
				Detail:    detail,
			})
		}
	}
	return out
}

// Detection #6: repeated_correction — same decision title >=3 times in 7 days.
func (s *Server) detectRepeatedCorrections(ctx context.Context, wsID *uuid.UUID) []agentBehaviorFinding {
	if s.decision == nil {
		return nil
	}
	all, err := s.decision.All(ctx, 500)
	if err != nil {
		slog.Warn("analyze_agent_behavior: decision.All failed", "error", err)
		return nil
	}
	cutoff7d := time.Now().Add(-7 * 24 * time.Hour)
	titleCounts := make(map[string]int)
	for _, d := range all {
		if !d.CreatedAt.Valid || d.CreatedAt.Time.Before(cutoff7d) {
			continue
		}
		titleCounts[d.Title]++
	}
	var out []agentBehaviorFinding
	for title, count := range titleCounts {
		if count < 3 {
			continue
		}
		detail, _ := json.Marshal(map[string]any{
			"decision_title": title,
			"count":          count,
			"window_days":    7,
		})
		if s.insertWatchdogEvent(ctx, wsID, watchdog.EventTypeRepeatedCorrection, watchdog.SeverityWarn, detail) {
			out = append(out, agentBehaviorFinding{
				EventType: string(watchdog.EventTypeRepeatedCorrection),
				Severity:  string(watchdog.SeverityWarn),
				Detail:    detail,
			})
		}
	}
	return out
}

// Detection #7: task_no_outcome — completed tasks in past 7 days without an outcome.
func (s *Server) detectTaskNoOutcome(ctx context.Context, wsID *uuid.UUID) []agentBehaviorFinding {
	if s.gtd == nil || s.outcome == nil {
		return nil
	}
	tasks, err := s.gtd.Tasks(ctx, nil)
	if err != nil {
		slog.Warn("analyze_agent_behavior: gtd.Tasks (task_no_outcome) failed", "error", err)
		return nil
	}
	cutoff7d := time.Now().Add(-7 * 24 * time.Hour)
	recentOutcomes, oErr := s.outcome.ListRecentOutcomes(ctx, wsID, "task", 500)
	if oErr != nil {
		slog.Warn("analyze_agent_behavior: ListRecentOutcomes failed", "error", oErr)
		recentOutcomes = nil
	}
	outcomeTaskIDs := make(map[string]bool, len(recentOutcomes))
	for _, o := range recentOutcomes {
		outcomeTaskIDs[o.EntityID.String()] = true
	}
	var out []agentBehaviorFinding
	for _, t := range tasks {
		if t.Status != "done" {
			continue
		}
		completedAt := t.UpdatedAt
		if !completedAt.Valid || completedAt.Time.Before(cutoff7d) {
			continue
		}
		if outcomeTaskIDs[t.ID.String()] {
			continue
		}
		detail, _ := json.Marshal(map[string]any{
			"task_id":      t.ID.String(),
			"title":        t.Title,
			"completed_at": completedAt.Time.Format(time.RFC3339),
		})
		if s.insertWatchdogEvent(ctx, wsID, watchdog.EventTypeTaskNoOutcome, watchdog.SeverityInfo, detail) {
			out = append(out, agentBehaviorFinding{
				EventType: string(watchdog.EventTypeTaskNoOutcome),
				Severity:  string(watchdog.SeverityInfo),
				Detail:    detail,
			})
		}
	}
	return out
}

// Detection #8: decision_no_reflection — decisions in past 30 days without a
// linked reflection.
func (s *Server) detectDecisionNoReflection(ctx context.Context, wsID *uuid.UUID) []agentBehaviorFinding {
	if s.decision == nil || s.reflection == nil {
		return nil
	}
	all, err := s.decision.All(ctx, 500)
	if err != nil {
		slog.Warn("analyze_agent_behavior: decision.All (reflection) failed", "error", err)
		return nil
	}
	cutoff30d := time.Now().Add(-30 * 24 * time.Hour)
	var out []agentBehaviorFinding
	for _, d := range all {
		if !d.CreatedAt.Valid || d.CreatedAt.Time.Before(cutoff30d) {
			continue
		}
		reflections, rErr := s.reflection.ByRelatedEntity(ctx, wsID, "decision", d.ID, 1)
		if rErr != nil {
			slog.Warn("analyze_agent_behavior: ByRelatedEntity failed",
				"error", rErr, "decision_id", d.ID)
			continue
		}
		if len(reflections) > 0 {
			continue
		}
		detail, _ := json.Marshal(map[string]any{
			"decision_id": d.ID.String(),
			"title":       d.Title,
			"created_at":  d.CreatedAt.Time.Format(time.RFC3339),
		})
		if s.insertWatchdogEvent(ctx, wsID, watchdog.EventTypeDecisionNoReflection, watchdog.SeverityInfo, detail) {
			out = append(out, agentBehaviorFinding{
				EventType: string(watchdog.EventTypeDecisionNoReflection),
				Severity:  string(watchdog.SeverityInfo),
				Detail:    detail,
			})
		}
	}
	return out
}

// ---- detect_unclosed_loops ----

func (s *Server) handleDetectUnclosedLoops(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.disciplineEventStore == nil {
		return mcp.NewToolResultError("discipline_event_store not configured"), nil
	}
	events, err := s.disciplineEventStore.ListUnresolved(ctx, s.workspaceID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("listing unresolved events: %v", err)), nil
	}
	if events == nil {
		events = []watchdog.DisciplineEvent{}
	}
	return jsonText(events)
}

// ---- mark_loop_resolved ----

func (s *Server) handleMarkLoopResolved(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.disciplineEventStore == nil {
		return mcp.NewToolResultError("discipline_event_store not configured"), nil
	}
	args := req.GetArguments()
	eventIDStr := stringArg(args, "event_id")
	if eventIDStr == "" {
		return mcp.NewToolResultError("event_id is required"), nil
	}
	eventID, err := uuid.Parse(eventIDStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid event_id UUID: %v", err)), nil
	}
	if err := s.disciplineEventStore.MarkResolved(ctx, eventID); err != nil {
		if errors.Is(err, watchdog.ErrEventNotFound) {
			return mcp.NewToolResultError(fmt.Sprintf("event %s not found", eventIDStr)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("marking event resolved: %v", err)), nil
	}
	out, _ := json.Marshal(map[string]any{
		"event_id":    eventIDStr,
		"resolved":    true,
		"resolved_at": time.Now().UTC().Format(time.RFC3339),
	})
	return mcp.NewToolResultText(string(out)), nil
}

// collectWatchdogMetaHealth queries discipline_events_m8 for open events and
// populates a watchdogMetaHealth struct for inclusion in the healthSnapshot.
// Errors are logged and swallowed — same policy as collectDisciplineHealth.
func (s *Server) collectWatchdogMetaHealth(ctx context.Context) watchdogMetaHealth {
	if s.disciplineEventStore == nil {
		return watchdogMetaHealth{}
	}
	events, err := s.disciplineEventStore.ListUnresolved(ctx, s.workspaceID)
	if err != nil {
		slog.Warn("collectWatchdogMetaHealth: ListUnresolved failed", "error", err)
		return watchdogMetaHealth{}
	}
	h := watchdogMetaHealth{
		UnresolvedCount: len(events),
	}
	seen := make(map[string]bool)
	for _, ev := range events {
		et := string(ev.EventType)
		if !seen[et] {
			h.RecentEventTypes = append(h.RecentEventTypes, et)
			seen[et] = true
		}
	}
	return h
}
