package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

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
}

// NewDashboardHandler creates a DashboardHandler.
func NewDashboardHandler(g dashboardGTDStore, d dashboardDecisionStore, p dashboardProposalStore) *DashboardHandler {
	return &DashboardHandler{gtd: g, decision: d, proposal: p}
}

// statsResponse is the JSON shape for GET /api/dashboard/stats.
type statsResponse struct {
	Period           string `json:"period"`
	TaskCompleted    int64  `json:"task_completed"`
	TaskTotal        int64  `json:"task_total"`
	DecisionCount    int    `json:"decision_count"`
	PendingProposals int    `json:"pending_proposals"`
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

	return c.JSON(http.StatusOK, statsResponse{
		Period:           period + "d",
		TaskCompleted:    completed,
		TaskTotal:        total,
		DecisionCount:    len(decisions),
		PendingProposals: len(pending),
	})
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
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if tasks == nil {
		tasks = []db.Task{}
	}
	return c.JSON(http.StatusOK, tasks)
}
