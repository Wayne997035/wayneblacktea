package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerResources registers the 4 read-only MCP resources.
// All handlers are workspace-scoped via s.workspaceID and NEVER accept
// workspace identity from the URI or any request parameter.
func (s *Server) registerResources(ms *server.MCPServer) {
	ms.AddResource(
		mcp.NewResource(
			"wayneblacktea://dashboard/overview",
			"Dashboard Overview",
			mcp.WithResourceDescription(
				"Active goals, projects, weekly progress, and pending-handoff status. "+
					"arch_snapshot_present is a bool — raw arch text is intentionally excluded "+
					"to prevent prompt-injection via stored snapshot content.",
			),
		),
		s.handleResourceDashboardOverview,
	)

	ms.AddResource(
		mcp.NewResource(
			"wayneblacktea://dashboard/upcoming",
			"Upcoming Tasks",
			mcp.WithResourceDescription(
				"Tasks grouped into five buckets: today, tomorrow, day_after, upcoming (next 7 days), "+
					"and unscheduled_important. Use to plan the next work session.",
			),
		),
		s.handleResourceDashboardUpcoming,
	)

	ms.AddResource(
		mcp.NewResource(
			"wayneblacktea://system/health",
			"System Health",
			mcp.WithResourceDescription(
				"Lightweight health snapshot: in_progress task count, stuck task IDs, "+
					"pending proposal count, due review count, discipline drift, and forgotten signals. "+
					"recent_calls and tool_call_counts are intentionally excluded (expensive / high-churn).",
			),
		),
		s.handleResourceSystemHealth,
	)

	ms.AddResource(
		mcp.NewResource(
			"wayneblacktea://gtd/current",
			"GTD Current State",
			mcp.WithResourceDescription(
				"Top pending task, proposal backlog count, unresolved handoff flag, and active task count. "+
					"Use at session start to orient the next action without a tool call.",
			),
		),
		s.handleResourceGTDCurrent,
	)
}

// workspaceIDForResource returns a canonical UUID string for the configured
// workspace, or "(unscoped)" when WORKSPACE_ID is not set.
func (s *Server) workspaceIDForResource() string {
	if s.workspaceID == nil {
		return "(unscoped)"
	}
	return s.workspaceID.String()
}

// marshalResource serialises v to JSON and wraps it in a TextResourceContents
// slice as required by ResourceHandlerFunc. Any marshalling error is returned
// as a Go error because the MCP transport handles it at the protocol layer.
func marshalResource(uri string, v any) ([]mcp.ResourceContents, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal resource %s: %w", uri, err)
	}
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      uri,
			MIMEType: "application/json",
			Text:     string(b),
		},
	}, nil
}

// ─── wayneblacktea://dashboard/overview ───────────────────────────────────

// dashboardOverviewResource is the JSON shape for the overview resource.
// Raw arch snapshot text is intentionally excluded (prompt-injection risk,
// see backend-security-design.md §2). Only a boolean presence flag is surfaced.
type dashboardOverviewResource struct {
	GeneratedAt         string         `json:"generated_at"`
	WorkspaceID         string         `json:"workspace_id"`
	Goals               any            `json:"goals"`
	Projects            any            `json:"projects"`
	WeeklyProgress      weeklyProgress `json:"weekly_progress"`
	PendingHandoff      bool           `json:"pending_handoff"`
	PendingHandoffAt    *string        `json:"pending_handoff_created_at,omitempty"`
	ArchSnapshotPresent bool           `json:"arch_snapshot_present"`
}

func (s *Server) handleResourceDashboardOverview(
	ctx context.Context,
	_ mcp.ReadResourceRequest,
) ([]mcp.ResourceContents, error) {
	const uri = "wayneblacktea://dashboard/overview"

	goals, err := s.gtd.ActiveGoals(ctx)
	if err != nil {
		return nil, fmt.Errorf("overview resource: loading goals: %w", err)
	}

	projects, err := s.gtd.ListActiveProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("overview resource: loading projects: %w", err)
	}

	completed, total, err := s.gtd.WeeklyProgress(ctx)
	if err != nil {
		return nil, fmt.Errorf("overview resource: loading progress: %w", err)
	}

	var (
		pendingHandoff   bool
		pendingHandoffAt *string
	)
	handoff, hErr := s.session.LatestHandoff(ctx)
	if hErr != nil && !errors.Is(hErr, session.ErrNotFound) {
		// Non-fatal: log and continue so other data is still returned.
		slog.Warn("overview resource: loading handoff", "err", hErr)
	}
	if handoff != nil {
		pendingHandoff = true
		if handoff.CreatedAt.Valid {
			ts := handoff.CreatedAt.Time.UTC().Format(time.RFC3339)
			pendingHandoffAt = &ts
		}
	}

	// Best-effort: surface arch snapshot presence without raw content.
	archPresent := false
	if _, archErr := s.arch.GetSnapshot(ctx, primaryProjectSlug); archErr == nil {
		archPresent = true
	}

	out := dashboardOverviewResource{
		GeneratedAt:         s.now().UTC().Format(time.RFC3339),
		WorkspaceID:         s.workspaceIDForResource(),
		Goals:               goals,
		Projects:            projects,
		WeeklyProgress:      weeklyProgress{Completed: completed, Total: total},
		PendingHandoff:      pendingHandoff,
		PendingHandoffAt:    pendingHandoffAt,
		ArchSnapshotPresent: archPresent,
	}
	return marshalResource(uri, out)
}

// ─── wayneblacktea://dashboard/upcoming ───────────────────────────────────

// dashboardUpcomingResource is the JSON shape for the upcoming resource.
type dashboardUpcomingResource struct {
	GeneratedAt string                 `json:"generated_at"`
	WorkspaceID string                 `json:"workspace_id"`
	Groups      resourceUpcomingGroups `json:"groups"`
}

// resourceUpcomingGroups holds the five upcoming-task buckets.
type resourceUpcomingGroups struct {
	Today                []resourceUpcomingItem `json:"today"`
	Tomorrow             []resourceUpcomingItem `json:"tomorrow"`
	DayAfter             []resourceUpcomingItem `json:"day_after"`
	Upcoming             []resourceUpcomingItem `json:"upcoming"`
	UnscheduledImportant []resourceUpcomingItem `json:"unscheduled_important"`
}

// resourceUpcomingItem is a lean task representation for the upcoming resource.
type resourceUpcomingItem struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Status     string  `json:"status"`
	Priority   int32   `json:"priority"`
	Importance *int16  `json:"importance,omitempty"`
	DueDate    *string `json:"due_date,omitempty"`
}

func (s *Server) handleResourceDashboardUpcoming(
	ctx context.Context,
	_ mcp.ReadResourceRequest,
) ([]mcp.ResourceContents, error) {
	const uri = "wayneblacktea://dashboard/upcoming"

	now := s.now()
	tasks, err := s.gtd.UpcomingTasks(ctx, now, 7, 20)
	if err != nil {
		return nil, fmt.Errorf("upcoming resource: loading tasks: %w", err)
	}

	groups := gtd.GroupUpcomingTasks(tasks, now, time.UTC, 7, 20)

	out := dashboardUpcomingResource{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		WorkspaceID: s.workspaceIDForResource(),
		Groups: resourceUpcomingGroups{
			Today:                toResourceUpcomingItems(groups.Today),
			Tomorrow:             toResourceUpcomingItems(groups.Tomorrow),
			DayAfter:             toResourceUpcomingItems(groups.DayAfter),
			Upcoming:             toResourceUpcomingItems(groups.Upcoming),
			UnscheduledImportant: toResourceUpcomingItems(groups.UnscheduledImportant),
		},
	}
	return marshalResource(uri, out)
}

// toResourceUpcomingItems converts a db.Task slice to resource-friendly items.
func toResourceUpcomingItems(tasks []db.Task) []resourceUpcomingItem {
	out := make([]resourceUpcomingItem, 0, len(tasks))
	for _, t := range tasks {
		item := resourceUpcomingItem{
			ID:       t.ID.String(),
			Title:    t.Title,
			Status:   t.Status,
			Priority: t.Priority,
		}
		if t.Importance.Valid {
			imp := t.Importance.Int16
			item.Importance = &imp
		}
		if t.DueDate.Valid {
			ds := t.DueDate.Time.UTC().Format(time.RFC3339)
			item.DueDate = &ds
		}
		out = append(out, item)
	}
	return out
}

// ─── wayneblacktea://system/health ─────────────────────────────────────────

// lightHealthResource is the JSON shape for the lightweight system/health resource.
// Deliberately excludes recent_calls, tool_call_counts, and
// completion_drift_candidates (expensive / high-churn). Consumers that need
// those fields should call the system_health MCP tool directly.
type lightHealthResource struct {
	GeneratedAt      string           `json:"generated_at"`
	WorkspaceID      string           `json:"workspace_id"`
	Tasks            taskHealth       `json:"tasks"`
	PendingProposals proposalHealth   `json:"pending_proposals"`
	DueReviews       reviewHealth     `json:"due_reviews"`
	Discipline       disciplineHealth `json:"discipline"`
	ForgottenSignals []string         `json:"forgotten_signals,omitempty"`
}

func (s *Server) handleResourceSystemHealth(
	ctx context.Context,
	_ mcp.ReadResourceRequest,
) ([]mcp.ResourceContents, error) {
	const uri = "wayneblacktea://system/health"

	const defaultStuckHours = 4
	stuckCutoff := s.now().Add(-defaultStuckHours * time.Hour)

	snap := lightHealthResource{
		GeneratedAt: s.now().UTC().Format(time.RFC3339),
		WorkspaceID: s.workspaceIDForResource(),
	}

	if tasks, err := s.gtd.Tasks(ctx, nil); err == nil {
		for _, t := range tasks {
			if t.Status != taskStatusInProgress {
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

	snap.Discipline = s.collectDisciplineHealth(ctx)

	// Build forgotten signals from the lightweight data available here.
	// (Watchdog-based signals are skipped — recent_calls are excluded by design.)
	var signals []string
	if snap.Tasks.Stuck > 0 {
		signals = append(signals,
			"There are stuck in-progress tasks. Claude likely forgot to call complete_task after finishing.")
	}
	if snap.PendingProposals.Pending >= 5 {
		signals = append(signals,
			"5+ pending proposals queued. Either ask Claude to confirm/reject them, or it stopped triaging.")
	}
	if snap.Discipline.DriftCount24h > 0 {
		signals = append(signals, fmt.Sprintf(
			"%d mutating MCP calls in last 24h with no preceding log_decision — likely undocumented changes.",
			snap.Discipline.DriftCount24h,
		))
	}
	snap.ForgottenSignals = signals

	return marshalResource(uri, snap)
}

// ─── wayneblacktea://gtd/current ──────────────────────────────────────────

// gtdCurrentResource is the JSON shape for the gtd/current resource.
type gtdCurrentResource struct {
	GeneratedAt       string   `json:"generated_at"`
	WorkspaceID       string   `json:"workspace_id"`
	TopTask           *topTask `json:"top_task"`
	ProposalBacklog   int      `json:"proposal_backlog"`
	UnresolvedHandoff bool     `json:"unresolved_handoff"`
	ActiveTaskCount   int      `json:"active_task_count"`
}

// topTask is the lean shape for the top pending task surfaced by gtd/current.
type topTask struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Priority int32   `json:"priority"`
	DueDate  *string `json:"due_date,omitempty"`
}

func (s *Server) handleResourceGTDCurrent(
	ctx context.Context,
	_ mcp.ReadResourceRequest,
) ([]mcp.ResourceContents, error) {
	const uri = "wayneblacktea://gtd/current"

	out := gtdCurrentResource{
		GeneratedAt: s.now().UTC().Format(time.RFC3339),
		WorkspaceID: s.workspaceIDForResource(),
	}

	// Top pending task.
	if t, err := s.gtd.TopPendingTask(ctx); err == nil && t != nil {
		tt := &topTask{
			ID:       t.ID.String(),
			Title:    t.Title,
			Priority: t.Priority,
		}
		if t.DueDate.Valid {
			ds := t.DueDate.Time.UTC().Format(time.RFC3339)
			tt.DueDate = &ds
		}
		out.TopTask = tt
	}

	// Proposal backlog count.
	if proposals, err := s.proposal.ListPending(ctx); err == nil {
		out.ProposalBacklog = len(proposals)
	}

	// Unresolved handoff: unresolved and < 48 hours old.
	handoff, hErr := s.session.LatestHandoff(ctx)
	if hErr != nil && !errors.Is(hErr, session.ErrNotFound) {
		slog.Warn("gtd/current resource: loading handoff", "err", hErr)
	}
	if handoff != nil && !handoff.ResolvedAt.Valid {
		if handoff.CreatedAt.Valid && time.Since(handoff.CreatedAt.Time) < 48*time.Hour {
			out.UnresolvedHandoff = true
		}
	}

	// Active task count (pending + in_progress).
	if tasks, err := s.gtd.Tasks(ctx, nil); err == nil {
		for _, t := range tasks {
			if t.Status == taskStatusPending || t.Status == taskStatusInProgress {
				out.ActiveTaskCount++
			}
		}
	}

	return marshalResource(uri, out)
}
