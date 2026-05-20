package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// dashboardActivityStore is a narrow interface for activity_log methods used by
// DashboardHandler. Kept separate from dashboardGTDStore and gtd.StoreIface to
// avoid coupling unrelated functionality.
type dashboardActivityStore interface {
	LatestActivityAt(ctx context.Context) (*time.Time, error)
	ListRecentAutomation(ctx context.Context, limit int32) ([]db.ActivityLog, error)
	LatestActionAt(ctx context.Context, action string) (*time.Time, error)
}

// DashboardActivityStoreIface is the exported alias of dashboardActivityStore for
// use by cmd/server/main.go when wiring the concrete store implementation.
type DashboardActivityStoreIface = dashboardActivityStore

// dashboardGTDStore covers the GTD methods used by DashboardHandler.
type dashboardGTDStore interface {
	WeeklyProgress(ctx context.Context) (completed, total int64, err error)
	ListActiveProjects(ctx context.Context) ([]db.Project, error)
	TopPendingTask(ctx context.Context) (*db.Task, error)
	UpcomingTasks(ctx context.Context, refDate time.Time, days, limit int) ([]db.Task, error)
	// TasksByDueDateRange returns pending/in_progress tasks whose due_date
	// falls inside [from, to] inclusive, ordered by due_date ASC.
	// Workspace isolation is enforced by the store implementation.
	TasksByDueDateRange(ctx context.Context, from, to time.Time) ([]db.Task, error)
	// Tasks returns all tasks for the configured workspace (optionally scoped
	// to projectID). Used by GetVagueTasks to surface descriptions that the
	// validator flags as too-vague / placeholder — a defence-in-depth signal
	// that the auto-capture regression (or any future bug) has leaked a
	// placeholder description into the GTD store.
	Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error)
}

// dashboardDecisionStore covers the decision methods used by DashboardHandler.
type dashboardDecisionStore interface {
	All(ctx context.Context, limit int32) ([]db.Decision, error)
}

// dashboardProposalStore covers the proposal methods used by DashboardHandler.
type dashboardProposalStore interface {
	ListPending(ctx context.Context) ([]db.PendingProposal, error)
}

// DashboardHandler handles the /api/dashboard/* endpoints.
type DashboardHandler struct {
	gtd      dashboardGTDStore
	decision dashboardDecisionStore
	proposal dashboardProposalStore
	// candidate is optional; nil = automation-health endpoint skips detection.
	candidate candidateStore
	// handoff is optional; nil = missing-handoff check skipped.
	handoff automationHandoffStore
	// activity is optional; nil = last_updated_at / automation-feed skipped.
	activity dashboardActivityStore
}

// NewDashboardHandler creates a DashboardHandler.
func NewDashboardHandler(g dashboardGTDStore, d dashboardDecisionStore, p dashboardProposalStore) *DashboardHandler {
	return &DashboardHandler{gtd: g, decision: d, proposal: p}
}

// SetActivityStore wires the activity store into the dashboard handler.
func (h *DashboardHandler) SetActivityStore(s dashboardActivityStore) {
	h.activity = s
}

// statsResponse is the JSON shape for GET /api/dashboard/stats.
//
// Field semantics for TaskCompleted / TaskTotal mirror gtd.Store.WeeklyProgress
// (see internal/gtd/store.go) and were tightened in PR #125 — they are
// week-scoped, not all-time.
type statsResponse struct {
	Period string `json:"period"`

	// TaskCompleted is the count of tasks with status='completed' whose
	// updated_at falls in the current ISO week (Monday 00:00 UTC to next
	// Monday 00:00 UTC). Source: gtd.Store.WeeklyProgress -> CountCompletedTasksThisWeek.
	TaskCompleted int64 `json:"task_completed"`

	// TaskTotal is the count of tasks RELEVANT to the current ISO week:
	// (tasks completed this week) UNION (pending/in_progress tasks with
	// due_date this week) UNION (pending/in_progress tasks created this week).
	// Invariant: TaskCompleted <= TaskTotal.
	// Source: gtd.Store.WeeklyProgress -> CountWeeklyRelevantTasks.
	TaskTotal int64 `json:"task_total"`

	DecisionCount    int     `json:"decision_count"`
	PendingProposals int     `json:"pending_proposals"`
	LastUpdatedAt    *string `json:"last_updated_at,omitempty"`
}

// GetStats handles GET /api/dashboard/stats?period=7 (or period=30).
// It returns completed task count (this week), total active task count,
// decision count (up to periodLimit), and pending proposal count.
func (h *DashboardHandler) GetStats(c echo.Context) error {
	period := c.QueryParam("period")
	if period == "" {
		period = "7"
	}
	if period != "7" && period != "30" {
		return c.JSON(http.StatusBadRequest, errResp("period must be 7 or 30"))
	}

	// Limit for decisions differs per period.
	var decLimit int32 = 50
	if period == "30" {
		decLimit = 200
	}

	ctx := c.Request().Context()

	completed, total, err := h.gtd.WeeklyProgress(ctx)
	if err != nil {
		c.Logger().Errorf("GetStats WeeklyProgress: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	decisions, err := h.decision.All(ctx, decLimit)
	if err != nil {
		c.Logger().Errorf("GetStats decisions: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	pending, err := h.proposal.ListPending(ctx)
	if err != nil {
		c.Logger().Errorf("GetStats proposals: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	resp := statsResponse{
		Period:           period + "d",
		TaskCompleted:    completed,
		TaskTotal:        total,
		DecisionCount:    len(decisions),
		PendingProposals: len(pending),
	}

	// Populate last_updated_at when activity store is wired.
	if h.activity != nil {
		if latestAt, err := h.activity.LatestActivityAt(ctx); err != nil {
			c.Logger().Warnf("GetStats LatestActivityAt: %v", err)
			// Non-fatal: omit the field rather than failing the response.
		} else if latestAt != nil {
			s := latestAt.UTC().Format(time.RFC3339)
			resp.LastUpdatedAt = &s
		}
	}

	return c.JSON(http.StatusOK, resp)
}

const (
	defaultDecisionLimit = 10
	maxDecisionLimit     = 100
)

// recentDecisionResponse is the JSON shape for recent decisions.
type recentDecisionResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	RepoName  string `json:"repo_name,omitempty"`
	Decision  string `json:"decision"`
	Rationale string `json:"rationale"`
	CreatedAt string `json:"created_at,omitempty"`
}

// GetRecentDecisions handles GET /api/dashboard/recent-decisions?limit=10.
// limit is capped at 100 to prevent DoS.
func (h *DashboardHandler) GetRecentDecisions(c echo.Context) error {
	limit := defaultDecisionLimit
	if raw := c.QueryParam("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return c.JSON(http.StatusBadRequest, errResp("limit must be a positive integer"))
		}
		if n > maxDecisionLimit {
			n = maxDecisionLimit
		}
		limit = n
	}

	ctx := c.Request().Context()
	decisions, err := h.decision.All(ctx, int32(limit))
	if err != nil {
		c.Logger().Errorf("GetRecentDecisions: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	out := make([]recentDecisionResponse, 0, len(decisions))
	for _, d := range decisions {
		r := recentDecisionResponse{
			ID:        d.ID.String(),
			Title:     d.Title,
			Decision:  d.Decision,
			Rationale: d.Rationale,
		}
		if d.RepoName.Valid {
			r.RepoName = d.RepoName.String
		}
		if d.CreatedAt.Valid {
			r.CreatedAt = d.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		out = append(out, r)
	}
	return c.JSON(http.StatusOK, out)
}

// activeProjectResponse is the JSON shape for active projects.
type activeProjectResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Area        string `json:"area,omitempty"`
	Description string `json:"description,omitempty"`
	Priority    int32  `json:"priority"`
}

// GetActiveProjects handles GET /api/dashboard/active-projects.
func (h *DashboardHandler) GetActiveProjects(c echo.Context) error {
	ctx := c.Request().Context()
	projects, err := h.gtd.ListActiveProjects(ctx)
	if err != nil {
		c.Logger().Errorf("GetActiveProjects: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	out := make([]activeProjectResponse, 0, len(projects))
	for _, p := range projects {
		r := activeProjectResponse{
			ID:       p.ID.String(),
			Name:     p.Name,
			Title:    p.Title,
			Status:   p.Status,
			Area:     p.Area,
			Priority: p.Priority,
		}
		if p.Description.Valid {
			r.Description = p.Description.String
		}
		out = append(out, r)
	}
	return c.JSON(http.StatusOK, out)
}

// dashboardWeeklyProgressResponse is the JSON shape for GET /api/dashboard/weekly-progress.
type dashboardWeeklyProgressResponse struct {
	Completed int64 `json:"completed"`
	Total     int64 `json:"total"`
}

// GetWeeklyProgress handles GET /api/dashboard/weekly-progress.
func (h *DashboardHandler) GetWeeklyProgress(c echo.Context) error {
	ctx := c.Request().Context()
	completed, total, err := h.gtd.WeeklyProgress(ctx)
	if err != nil {
		c.Logger().Errorf("GetWeeklyProgress: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, dashboardWeeklyProgressResponse{Completed: completed, Total: total})
}

// pendingKnowledgeProposalResponse is the JSON shape for pending knowledge proposals.
type pendingKnowledgeProposalResponse struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at,omitempty"`
}

// GetPendingKnowledgeProposals handles GET /api/dashboard/pending-knowledge-proposals.
func (h *DashboardHandler) GetPendingKnowledgeProposals(c echo.Context) error {
	ctx := c.Request().Context()
	pending, err := h.proposal.ListPending(ctx)
	if err != nil {
		c.Logger().Errorf("GetPendingKnowledgeProposals: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	out := make([]pendingKnowledgeProposalResponse, 0)
	for _, p := range pending {
		if p.Type != string(proposal.TypeKnowledge) {
			continue
		}
		r := pendingKnowledgeProposalResponse{
			ID:     p.ID.String(),
			Type:   p.Type,
			Status: p.Status,
		}
		if p.CreatedAt.Valid {
			r.CreatedAt = p.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		out = append(out, r)
	}
	return c.JSON(http.StatusOK, out)
}

// nextTaskResponse is the JSON shape for GET /api/dashboard/next-task.
type nextTaskResponse struct {
	Task *db.Task `json:"task"`
}

// GetNextTask handles GET /api/dashboard/next-task.
// Returns 200 with {"task": <db.Task>} when a pending task exists, or
// {"task": null} when none exist. Store errors yield 500.
func (h *DashboardHandler) GetNextTask(c echo.Context) error {
	ctx := c.Request().Context()
	task, err := h.gtd.TopPendingTask(ctx)
	if err != nil {
		c.Logger().Errorf("GetNextTask: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, nextTaskResponse{Task: task})
}

const (
	defaultUpcomingDays  = 7
	maxUpcomingDays      = 14
	minUpcomingDays      = 1
	defaultUpcomingLimit = 10
	maxUpcomingLimit     = 50
)

// upcomingTaskItem is the JSON shape for a single task in an upcoming-tasks group.
type upcomingTaskItem struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Status     string  `json:"status"`
	Priority   int32   `json:"priority"`
	Importance *int16  `json:"importance,omitempty"`
	DueDate    *string `json:"due_date,omitempty"`
	ProjectID  *string `json:"project_id,omitempty"`
}

// upcomingTasksResponse is the JSON shape for GET /api/dashboard/upcoming-tasks.
type upcomingTasksResponse struct {
	Groups upcomingTaskGroups `json:"groups"`
}

// upcomingTaskGroups holds the five buckets returned by the upcoming-tasks endpoint.
type upcomingTaskGroups struct {
	Today                []upcomingTaskItem `json:"today"`
	Tomorrow             []upcomingTaskItem `json:"tomorrow"`
	DayAfter             []upcomingTaskItem `json:"day_after"`
	Upcoming             []upcomingTaskItem `json:"upcoming"`
	UnscheduledImportant []upcomingTaskItem `json:"unscheduled_important"`
}

// GetUpcomingTasks handles GET /api/dashboard/upcoming-tasks.
//
// Query params:
//   - days:  1-14 (default 7) — how far ahead the "upcoming" window extends
//   - limit: 1-50 (default 10) — total tasks across all groups
//   - tz:    IANA timezone name (default "UTC") — used for day-boundary calculation
func (h *DashboardHandler) GetUpcomingTasks(c echo.Context) error {
	days := defaultUpcomingDays
	if raw := c.QueryParam("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < minUpcomingDays {
			return c.JSON(http.StatusBadRequest, errResp("days must be an integer >= 1"))
		}
		if n > maxUpcomingDays {
			n = maxUpcomingDays
		}
		days = n
	}

	limit := defaultUpcomingLimit
	if raw := c.QueryParam("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return c.JSON(http.StatusBadRequest, errResp("limit must be a positive integer"))
		}
		if n > maxUpcomingLimit {
			n = maxUpcomingLimit
		}
		limit = n
	}

	tz := time.UTC
	if tzName := c.QueryParam("tz"); tzName != "" {
		if len(tzName) > 64 {
			return c.JSON(http.StatusBadRequest, errResp("invalid tz: timezone name too long"))
		}
		loc, err := time.LoadLocation(tzName)
		if err != nil {
			return c.JSON(http.StatusBadRequest, errResp("invalid tz: must be a valid IANA timezone name"))
		}
		tz = loc
	}

	ctx := c.Request().Context()
	now := time.Now().In(tz)

	tasks, err := h.gtd.UpcomingTasks(ctx, now, days, limit)
	if err != nil {
		c.Logger().Errorf("GetUpcomingTasks: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	groups := gtd.GroupUpcomingTasks(tasks, now, tz, days, limit)

	resp := upcomingTasksResponse{
		Groups: upcomingTaskGroups{
			Today:                toUpcomingItems(groups.Today),
			Tomorrow:             toUpcomingItems(groups.Tomorrow),
			DayAfter:             toUpcomingItems(groups.DayAfter),
			Upcoming:             toUpcomingItems(groups.Upcoming),
			UnscheduledImportant: toUpcomingItems(groups.UnscheduledImportant),
		},
	}
	return c.JSON(http.StatusOK, resp)
}

// toUpcomingItems converts a slice of db.Task into API-friendly upcomingTaskItem values.
func toUpcomingItems(tasks []db.Task) []upcomingTaskItem {
	if len(tasks) == 0 {
		return []upcomingTaskItem{}
	}
	out := make([]upcomingTaskItem, 0, len(tasks))
	for _, t := range tasks {
		item := upcomingTaskItem{
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
			s := t.DueDate.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
			item.DueDate = &s
		}
		if t.ProjectID.Valid {
			s := uuid.UUID(t.ProjectID.Bytes).String()
			item.ProjectID = &s
		}
		out = append(out, item)
	}
	return out
}

// GetUpcoming handles GET /api/dashboard/upcoming.
// It returns a flat JSON array of pending/in_progress tasks with due_date
// in the next 7 days (server-side hardcoded window), ordered by due_date ASC.
// Workspace isolation is enforced by the store — no workspace input from request.
func (h *DashboardHandler) GetUpcoming(c echo.Context) error {
	ctx := c.Request().Context()
	now := time.Now().UTC()
	tasks, err := h.gtd.TasksByDueDateRange(ctx, now, now.AddDate(0, 0, 7))
	if err != nil {
		c.Logger().Errorf("GetUpcoming: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "internal server error")
	}
	if tasks == nil {
		tasks = []db.Task{}
	}
	return c.JSON(http.StatusOK, tasks)
}

const (
	defaultFeedLimit = 20
	minFeedLimit     = 1
	maxFeedLimit     = 50
)

// automationFeedItem is a single entry in the automation feed response.
type automationFeedItem struct {
	ID         string  `json:"id"`
	Action     string  `json:"action"`
	Kind       string  `json:"kind"`
	Notes      *string `json:"notes,omitempty"`
	OccurredAt string  `json:"occurred_at"`
}

// automationFeedResponse is the JSON shape for GET /api/dashboard/automation-feed.
type automationFeedResponse struct {
	Feed      []automationFeedItem `json:"feed"`
	FetchedAt string               `json:"fetched_at"`
}

// GetAutomationFeed handles GET /api/dashboard/automation-feed?limit=20.
// limit must be 1-50 (default 20); values outside range → 400.
// Returns recent MCP automation actions from activity_log, most-recent first.
func (h *DashboardHandler) GetAutomationFeed(c echo.Context) error {
	limit := defaultFeedLimit
	if raw := c.QueryParam("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < minFeedLimit || n > maxFeedLimit {
			return c.JSON(http.StatusBadRequest, errResp("limit must be an integer between 1 and 50"))
		}
		limit = n
	}

	if h.activity == nil {
		return c.JSON(http.StatusOK, automationFeedResponse{
			Feed:      []automationFeedItem{},
			FetchedAt: time.Now().UTC().Format(time.RFC3339),
		})
	}

	ctx := c.Request().Context()
	rows, err := h.activity.ListRecentAutomation(ctx, int32(limit))
	if err != nil {
		c.Logger().Errorf("GetAutomationFeed ListRecentAutomation: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	feed := make([]automationFeedItem, 0, len(rows))
	for _, r := range rows {
		item := automationFeedItem{
			ID:         r.ID.String(),
			Action:     r.Action,
			Kind:       classifyActivityKind(r.Action),
			OccurredAt: r.CreatedAt.Time.UTC().Format(time.RFC3339),
		}
		if r.Notes.Valid && r.Notes.String != "" {
			n := r.Notes.String
			item.Notes = &n
		}
		feed = append(feed, item)
	}

	return c.JSON(http.StatusOK, automationFeedResponse{
		Feed:      feed,
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

// vagueTasksResponse is the JSON shape for GET /api/dashboard/vague-tasks.
// Mirrors the system_health VaguePendingTaskCount / VagueSampleIDs fields so
// frontend / monitoring clients can subscribe to either signal without
// translating shapes.
type vagueTasksResponse struct {
	Count     int      `json:"count"`
	SampleIDs []string `json:"sample_ids"`
}

// maxDashboardVagueSampleIDs caps the number of vague-task IDs returned by
// the HTTP endpoint. Kept consistent with the system_health cap so the two
// signals agree.
const maxDashboardVagueSampleIDs = 5

// countVagueTasks scans tasks for pending/in_progress entries whose
// Description is flagged by validator.CheckVagueness for the task's Kind.
// Returns the total count plus the first `sampleCap` IDs in scan order.
//
// Mirrors the MCP system_health helper of the same purpose — kept in the
// handler package to avoid importing internal/mcp (cycle), and to keep the
// HTTP surface self-contained. Behavioural drift between the two helpers is
// guarded by validator.CheckVagueness being the single source of truth.
func countVagueTasks(tasks []db.Task, sampleCap int) (int, []string) {
	count := 0
	var sampleIDs []string
	const (
		statusPending    = "pending"
		statusInProgress = "in_progress"
	)
	for _, t := range tasks {
		if t.Status != statusPending && t.Status != statusInProgress {
			continue
		}
		if !t.Description.Valid || t.Description.String == "" {
			continue
		}
		warnings := validator.CheckVagueness("description", t.Description.String, t.Kind)
		if len(warnings) == 0 {
			continue
		}
		count++
		if len(sampleIDs) < sampleCap {
			sampleIDs = append(sampleIDs, t.ID.String())
		}
	}
	return count, sampleIDs
}

// GetVagueTasks handles GET /api/dashboard/vague-tasks.
//
// It scans pending/in_progress tasks in the configured workspace and counts
// those whose Description triggers validator.CheckVagueness for the task's
// Kind (chore tasks are exempt per CheckVagueness). The first
// maxDashboardVagueSampleIDs IDs are returned alongside the total count.
//
// This is a defence-in-depth surface: the structural fix for the
// auto-capture regression (engineer A's TASK 4) is the must-have; this
// endpoint exists so any future leak — whether from auto-capture or another
// path — is visible without spelunking system_health.
func (h *DashboardHandler) GetVagueTasks(c echo.Context) error {
	ctx := c.Request().Context()
	tasks, err := h.gtd.Tasks(ctx, nil)
	if err != nil {
		c.Logger().Errorf("GetVagueTasks Tasks: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	count, sampleIDs := countVagueTasks(tasks, maxDashboardVagueSampleIDs)
	if sampleIDs == nil {
		sampleIDs = []string{}
	}
	return c.JSON(http.StatusOK, vagueTasksResponse{Count: count, SampleIDs: sampleIDs})
}
