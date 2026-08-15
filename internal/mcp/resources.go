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
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerResources registers the 5 read-only MCP resources.
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

	ms.AddResource(
		mcp.NewResource(
			"wayneblacktea://session/handoff/latest",
			"Latest Session Handoff",
			mcp.WithResourceDescription(
				"Full detail of the latest session handoff: intent, context_summary, and next_actions, "+
					"fenced as stored data (not instructions). get_today_context's pending_handoff field only "+
					"reports presence and next_actions_total — read this resource for the actual text. "+
					"handoff_present is false with no other fields when no handoff is pending.",
			),
		),
		s.handleResourceHandoffLatest,
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
	b, err := json.Marshal(v)
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

// ─── wayneblacktea://session/handoff/latest ───────────────────────────────

// handoffResourceIntentMaxRunes / handoffResourceSummaryMaxRunes are the
// read-time caps for this resource's free-text fields.
//
// Correction to an earlier premise (dispatch spec, W3 planning): set_session_
// handoff DOES apply a write-time bound — checkHandoffNoise (tools_session.go
// handleSetSessionHandoff) delegates to validator.CheckField, which rejects
// any field over validator.MaxFieldLen (5000 BYTES). It is a byte cap, not a
// rune cap, so it under-bounds CJK content in rune terms (5000 bytes ≈ 1666
// runes of 3-byte CJK) while over-bounding it in the ASCII case (5000 bytes =
// 5000 runes, well over this resource's 2000/4000 rune caps). Contrast
// upsert_project_arch, whose maxSummaryLen=8000 write-time bound lets
// fenceArchSummary skip re-clipping entirely at read time (boundary_markers.go)
// — that shortcut is NOT safe here, because 5000 write-time bytes can still
// mean up to 5000 runes for ASCII-heavy text, comfortably over both caps
// below. This resource's own read-time cap therefore remains the real bound
// for the common (ASCII/mixed) case and must never be dropped as "surely
// fine, the write side already caps it."
//
// Sized generously relative to get_today_context's now-removed
// pending_handoff caps (300/350) because this resource is read on demand,
// not injected into every session's opening prompt.
const (
	handoffResourceIntentMaxRunes  = 2000
	handoffResourceSummaryMaxRunes = 4000
)

// handoffResourceRepoNameMaxRunes bounds repo_name, which this resource
// renders in the SAME JSON payload as the fenced intent/context_summary above
// without a fence of its own (it and next_actions rely on StoredDataNotice
// instead — see the handoffResource type doc comment). set_session_handoff
// applies NO write-time validation to repo_name at all — checkHandoffNoise
// (tools_session.go handleSetSessionHandoff) covers only intent and
// context_summary — so this read-time cap is the only bound that exists (PR
// #157 security review C-1 / M-4: an unbounded, un-neutralised repo_name both
// forged an escape for its sibling fences and let a single field blow the
// payload to 200,000+ runes). Shared with buildPendingHandoffSummary
// (tools_context.go) so both readers of RepoName enforce the same bound.
const handoffResourceRepoNameMaxRunes = 200

// handoffResourceMaxNextActions bounds how many next_actions rows this
// resource renders. Each row already carries three 500-rune-capped fields
// (tools_session.go maxNextActionFieldLen) and set_session_handoff allows up
// to maxNextActionItems=50 of them — rendering all 50 at once measured at
// 80,687 bytes for a single resource read (PR #157 security review m-3), an
// unbounded cost for a mechanism whose whole purpose is saving tokens.
// NextActionsTotal is computed from the FULL decoded list before this cap is
// applied (see handleResourceHandoffLatest), so a truncated response stays
// visible (total > len(next_actions)) rather than looking identical to "there
// were only N to begin with".
const handoffResourceMaxNextActions = 20

// handoffResource is the full, fenced view of the latest session handoff.
//
// Deliberately NOT a reuse of pendingHandoffView (tools_context.go): that
// type is returned verbatim by set_session_handoff / mark_next_action_done
// to echo the CALLER'S OWN just-written text back unfenced
// (buildPendingHandoffView's doc comment), which is safe only because the
// reader is the same turn that wrote it. This resource can be read by any
// session, including one that has not written anything and has earned no
// trust, so its free-text fields go through the same
// clip+fence+neutralise treatment get_today_context's pending_handoff used
// to apply before W3.
type handoffResource struct {
	HandoffPresent bool `json:"handoff_present"`
	// StoredDataNotice leads the payload for the same reason it leads
	// get_today_context's (tools_context.go): repo_name and next_actions.*
	// are neutralised against forged boundary markers but NOT individually
	// fenced like Intent/ContextSummary below, so this notice is the only
	// in-payload signal that they are stored data, not instructions (PR #157
	// security review M-1). omitempty keeps the handoff_present=false path a
	// single-field response.
	StoredDataNotice string `json:"stored_data_notice,omitempty"`
	// ID is a pointer (not uuid.UUID) so omitempty actually drops it on the
	// handoff_present=false path: [16]byte arrays never satisfy
	// encoding/json's isEmptyValue (Len() on a fixed-size array is always its
	// declared length, never 0), so a plain uuid.UUID field would serialize
	// the zero UUID string even when omitempty is set — this pointer is the
	// fix, not a stylistic choice.
	ID               *uuid.UUID          `json:"id,omitempty"`
	RepoName         string              `json:"repo_name,omitempty"`
	Intent           string              `json:"intent,omitempty"`
	ContextSummary   string              `json:"context_summary,omitempty"`
	CreatedAt        string              `json:"created_at,omitempty"`
	NextActions      []nextActionSummary `json:"next_actions,omitempty"`
	NextActionsTotal *int                `json:"next_actions_total,omitempty"`
}

// neutralizePtr is neutralizeBoundaryMarkers over an optional string pointer;
// nil becomes "". Deliberately NOT clipSafe/clipPtr (tools_context.go): the
// next_actions JSON column is already bounded at write time
// (maxNextActionItems=50 rows, maxNextActionFieldLen=500 runes per field,
// tools_session.go parseAndValidateNextActions), so this resource only needs
// to strip forged boundary markers, not re-clip.
func neutralizePtr(p *string) string {
	if p == nil {
		return ""
	}
	return neutralizeBoundaryMarkers(*p)
}

func (s *Server) handleResourceHandoffLatest(
	ctx context.Context,
	_ mcp.ReadResourceRequest,
) ([]mcp.ResourceContents, error) {
	const uri = "wayneblacktea://session/handoff/latest"

	h, err := s.session.LatestHandoff(ctx)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return marshalResource(uri, handoffResource{HandoffPresent: false})
		}
		return nil, fmt.Errorf("handoff resource: loading handoff: %w", err)
	}

	view := buildPendingHandoffView(h)
	id := h.ID
	// Computed from the FULL decoded list, BEFORE the handoffResourceMaxNextActions
	// truncation below — handoffResourceMaxNextActions doc comment.
	nextActionsTotal := len(view.NextActions)
	out := handoffResource{
		HandoffPresent:   true,
		ID:               &id,
		StoredDataNotice: storedDataNotice,
		// clipSafe, not textValue: repo_name is rendered in the SAME payload
		// as the fenced intent/context_summary, so an un-neutralised marker
		// here forges an escape for those fences (boundary_markers.go:20-25).
		// clipSafe also bounds the field to handoffResourceRepoNameMaxRunes,
		// which that const's doc comment covers.
		RepoName: clipSafe(textValue(h.RepoName), handoffResourceRepoNameMaxRunes),
		// Fenced, not merely clipped: this resource is the ONLY reader of the
		// full text now that get_today_context's pending_handoff carries none
		// of it (W3) — see the type doc comment above for the threat model.
		Intent:           clipAndFenceStoredContext(h.Intent, handoffResourceIntentMaxRunes),
		ContextSummary:   clipAndFenceStoredContext(textValue(h.ContextSummary), handoffResourceSummaryMaxRunes),
		NextActionsTotal: &nextActionsTotal,
	}
	if h.CreatedAt.Valid {
		out.CreatedAt = h.CreatedAt.Time.UTC().Format(time.RFC3339)
	}
	actions := view.NextActions
	if len(actions) > handoffResourceMaxNextActions {
		actions = actions[:handoffResourceMaxNextActions]
	}
	if len(actions) > 0 {
		out.NextActions = make([]nextActionSummary, 0, len(actions))
		for i := range actions {
			a := &actions[i]
			out.NextActions = append(out.NextActions, nextActionSummary{
				Step:      a.Step,
				Title:     neutralizeBoundaryMarkers(a.Title),
				Status:    string(a.Status),
				Command:   neutralizeBoundaryMarkers(a.Command),
				Expected:  neutralizeBoundaryMarkers(a.Expected),
				RefTaskID: neutralizePtr(a.RefTaskID),
			})
		}
	}

	return marshalResource(uri, out)
}
