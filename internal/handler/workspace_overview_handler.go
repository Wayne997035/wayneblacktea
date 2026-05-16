package handler

import (
	"errors"
	"net/http"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

// WorkspaceOverviewHandler serves GET /api/workspace/repos/:id/overview, the
// drill-down view for a single repo card on /workspace.
//
// Workspace scoping is automatic: every backing Store is constructed with the
// process workspaceID at startup (see internal/storage/factory.go) so all
// reads here are filtered by workspace_id without any request-side input.
type WorkspaceOverviewHandler struct {
	workspaceStore repoOverviewWorkspaceStore
	gtd            repoOverviewGTDStore
	decision       repoOverviewDecisionStore
	session        repoOverviewSessionStore
}

// NewWorkspaceOverviewHandler constructs the handler with the four narrow
// store interfaces it needs. Pass concrete *Store values from cmd/server.
func NewWorkspaceOverviewHandler(
	w repoOverviewWorkspaceStore,
	g repoOverviewGTDStore,
	d repoOverviewDecisionStore,
	s repoOverviewSessionStore,
) *WorkspaceOverviewHandler {
	return &WorkspaceOverviewHandler{
		workspaceStore: w,
		gtd:            g,
		decision:       d,
		session:        s,
	}
}

// repoOverviewListLimit caps every list inside the overview payload. Each
// list is independently capped so a single very-active repo can't blow out
// the response budget.
const (
	repoOverviewListLimit          int32 = 50
	repoOverviewActivityWindowDays int32 = 14
)

// repoOverviewResponse is the JSON shape returned by GetRepoOverview.
//
// All time fields are RFC3339 UTC ("2006-01-02T15:04:05Z07:00"). Lists are
// always serialised as arrays (never null) so the frontend can map without a
// nil check.
type repoOverviewResponse struct {
	Repo            repoSummary          `json:"repo"`
	CompletedTasks  []completedTaskItem  `json:"completed_tasks"`
	PendingTasks    []pendingTaskItem    `json:"pending_tasks"`
	RecentDecisions []recentDecisionItem `json:"recent_decisions"`
	RecentActivity  []recentActivityItem `json:"recent_activity"`
	RecentHandoffs  []recentHandoffItem  `json:"recent_handoffs"`
}

type repoSummary struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	Language        string   `json:"language,omitempty"`
	Status          string   `json:"status"`
	CurrentBranch   string   `json:"current_branch,omitempty"`
	NextPlannedStep string   `json:"next_planned_step,omitempty"`
	LastActivity    string   `json:"last_activity,omitempty"`
	Path            string   `json:"path,omitempty"`
	KnownIssues     []string `json:"known_issues"`
}

type completedTaskItem struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	CompletedAt string `json:"completed_at,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	Artifact    string `json:"artifact,omitempty"`
}

type pendingTaskItem struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Importance int16  `json:"importance,omitempty"`
	Priority   int32  `json:"priority"`
	DueDate    string `json:"due_date,omitempty"`
	ProjectID  string `json:"project_id,omitempty"`
}

type recentDecisionItem struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Decision  string `json:"decision"`
	Rationale string `json:"rationale"`
	CreatedAt string `json:"created_at,omitempty"`
}

type recentActivityItem struct {
	ID        string `json:"id"`
	Action    string `json:"action"`
	Notes     string `json:"notes,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	Kind      string `json:"kind"`
}

type recentHandoffItem struct {
	ID         string `json:"id"`
	Intent     string `json:"intent"`
	Status     string `json:"status"` // "open" or "resolved"
	CreatedAt  string `json:"created_at,omitempty"`
	ResolvedAt string `json:"resolved_at,omitempty"`
}

// GetRepoOverview handles GET /api/workspace/repos/:id/overview.
//
// 400 — id path param is not a valid UUID.
// 404 — repo not found in the configured workspace.
// 200 — overview payload (lists may be empty, never null).
//
// Implementation: looks up the repo by id (workspace-scoped), then resolves
// the matching project by repo.name (project-name == repo-name convention).
// Decisions and handoffs are looked up by repo.name (TEXT). When no project
// matches the repo, task and activity lists are empty (not an error) so the
// UI degrades gracefully for repos that have not yet been mirrored as a
// GTD project.
func (h *WorkspaceOverviewHandler) GetRepoOverview(c echo.Context) error {
	ctx := c.Request().Context()

	repoID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid repo id"))
	}

	repo, err := h.workspaceStore.RepoByID(ctx, repoID)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			return c.JSON(http.StatusNotFound, errResp("repo not found"))
		}
		c.Logger().Errorf("GetRepoOverview RepoByID: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	resp := repoOverviewResponse{
		Repo: toRepoSummary(repo),
		// Pre-allocate empty slices so JSON encodes [] not null.
		CompletedTasks:  []completedTaskItem{},
		PendingTasks:    []pendingTaskItem{},
		RecentDecisions: []recentDecisionItem{},
		RecentActivity:  []recentActivityItem{},
		RecentHandoffs:  []recentHandoffItem{},
	}

	// Look up paired projects in two phases:
	//   1. NEW: projects.repo_name explicitly set (migration 000037).
	//   2. FALLBACK: legacy convention `project.name == repo.name`.
	// We aggregate task / activity lists across all matched projects so a repo
	// hosting multiple projects (the common case once the column is populated)
	// shows everything in one card. Each list is independently capped so a
	// single hot project can't blow out the response budget.
	projects, projErr := h.gtd.ProjectsByRepoName(ctx, repo.Name)
	if projErr != nil {
		c.Logger().Errorf("GetRepoOverview ProjectsByRepoName: %v", projErr)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	if len(projects) == 0 {
		// Fallback: legacy by-name lookup. Repo without a paired project is
		// normal — silently leave lists empty when ErrNotFound surfaces.
		legacy, legacyErr := h.gtd.ProjectByName(ctx, repo.Name)
		switch {
		case legacyErr == nil:
			projects = []db.Project{*legacy}
		case errors.Is(legacyErr, gtd.ErrNotFound):
			// no project paired by either path — leave task/activity lists empty
		default:
			c.Logger().Errorf("GetRepoOverview ProjectByName fallback: %v", legacyErr)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
	}

	since := time.Now().Add(-time.Duration(repoOverviewActivityWindowDays) * 24 * time.Hour)
	for i := range projects {
		project := &projects[i]
		// Pending (incl. in_progress) tasks for the project. Tasks() doesn't
		// accept a limit, so we apply a Go-side cap on the AGGREGATE (not
		// per-project) to keep the response budget stable.
		pending, terr := h.gtd.Tasks(ctx, &project.ID)
		if terr != nil {
			c.Logger().Errorf("GetRepoOverview Tasks: %v", terr)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
		resp.PendingTasks = appendPendingCapped(resp.PendingTasks, pending, int(repoOverviewListLimit))

		completed, cerr := h.gtd.RecentCompletedTasks(ctx, project.ID, repoOverviewListLimit)
		if cerr != nil {
			c.Logger().Errorf("GetRepoOverview RecentCompletedTasks: %v", cerr)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
		resp.CompletedTasks = appendCompletedCapped(resp.CompletedTasks, completed, int(repoOverviewListLimit))

		activity, aerr := h.gtd.RecentActivityByProject(ctx, project.ID, since, repoOverviewListLimit)
		if aerr != nil {
			c.Logger().Errorf("GetRepoOverview RecentActivityByProject: %v", aerr)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
		resp.RecentActivity = appendActivityCapped(resp.RecentActivity, activity, int(repoOverviewListLimit))
	}

	decisions, derr := h.decision.ByRepo(ctx, repo.Name, repoOverviewListLimit)
	if derr != nil {
		c.Logger().Errorf("GetRepoOverview decision.ByRepo: %v", derr)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	resp.RecentDecisions = toRecentDecisionItems(decisions)

	handoffs, herr := h.session.HandoffsByRepo(ctx, repo.Name, int(repoOverviewListLimit))
	if herr != nil {
		c.Logger().Errorf("GetRepoOverview HandoffsByRepo: %v", herr)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	resp.RecentHandoffs = toRecentHandoffItems(handoffs)

	return c.JSON(http.StatusOK, resp)
}

// ---- conversion helpers (db row → JSON DTO) ----

const repoOverviewTimeLayout = "2006-01-02T15:04:05Z07:00"

func formatTS(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(repoOverviewTimeLayout)
}

func toRepoSummary(repo *db.Repo) repoSummary {
	out := repoSummary{
		ID:          repo.ID.String(),
		Name:        repo.Name,
		Status:      repo.Status,
		KnownIssues: repo.KnownIssues,
	}
	if out.KnownIssues == nil {
		out.KnownIssues = []string{}
	}
	if repo.Description.Valid {
		out.Description = repo.Description.String
	}
	if repo.Language.Valid {
		out.Language = repo.Language.String
	}
	if repo.CurrentBranch.Valid {
		out.CurrentBranch = repo.CurrentBranch.String
	}
	if repo.NextPlannedStep.Valid {
		out.NextPlannedStep = repo.NextPlannedStep.String
	}
	if repo.Path.Valid {
		out.Path = repo.Path.String
	}
	out.LastActivity = formatTS(repo.LastActivity)
	return out
}

func toRecentDecisionItems(rows []db.Decision) []recentDecisionItem {
	out := make([]recentDecisionItem, 0, len(rows))
	for _, d := range rows {
		out = append(out, recentDecisionItem{
			ID:        d.ID.String(),
			Title:     d.Title,
			Decision:  d.Decision,
			Rationale: d.Rationale,
			CreatedAt: formatTS(d.CreatedAt),
		})
	}
	return out
}

func toRecentHandoffItems(rows []db.SessionHandoff) []recentHandoffItem {
	out := make([]recentHandoffItem, 0, len(rows))
	for _, h := range rows {
		status := "open"
		if h.ResolvedAt.Valid {
			status = "resolved"
		}
		out = append(out, recentHandoffItem{
			ID:         h.ID.String(),
			Intent:     h.Intent,
			Status:     status,
			CreatedAt:  formatTS(h.CreatedAt),
			ResolvedAt: formatTS(h.ResolvedAt),
		})
	}
	return out
}

// appendPendingCapped converts the raw db.Task slice to pending DTOs and
// appends them to acc, stopping once acc reaches max items. Used so the
// per-repo aggregate across multiple paired projects respects a single hard
// cap rather than a per-project one.
func appendPendingCapped(acc []pendingTaskItem, tasks []db.Task, max int) []pendingTaskItem {
	if len(acc) >= max {
		return acc
	}
	for _, t := range tasks {
		if len(acc) >= max {
			break
		}
		item := pendingTaskItem{
			ID:       t.ID.String(),
			Title:    t.Title,
			Status:   t.Status,
			Priority: t.Priority,
			DueDate:  formatTS(t.DueDate),
		}
		if t.Importance.Valid {
			item.Importance = t.Importance.Int16
		}
		if t.ProjectID.Valid {
			item.ProjectID = uuid.UUID(t.ProjectID.Bytes).String()
		}
		acc = append(acc, item)
	}
	return acc
}

// appendCompletedCapped is the completed-task analogue of appendPendingCapped.
func appendCompletedCapped(acc []completedTaskItem, tasks []db.Task, max int) []completedTaskItem {
	if len(acc) >= max {
		return acc
	}
	for _, t := range tasks {
		if len(acc) >= max {
			break
		}
		item := completedTaskItem{
			ID:          t.ID.String(),
			Title:       t.Title,
			CompletedAt: formatTS(t.UpdatedAt),
		}
		if t.ProjectID.Valid {
			item.ProjectID = uuid.UUID(t.ProjectID.Bytes).String()
		}
		if t.Artifact.Valid {
			item.Artifact = t.Artifact.String
		}
		acc = append(acc, item)
	}
	return acc
}

// appendActivityCapped is the activity-log analogue of appendPendingCapped.
func appendActivityCapped(acc []recentActivityItem, rows []db.ActivityLog, max int) []recentActivityItem {
	if len(acc) >= max {
		return acc
	}
	for _, a := range rows {
		if len(acc) >= max {
			break
		}
		item := recentActivityItem{
			ID:        a.ID.String(),
			Action:    a.Action,
			CreatedAt: formatTS(a.CreatedAt),
			Kind:      classifyActivityKind(a.Action),
		}
		if a.Notes.Valid {
			item.Notes = a.Notes.String
		}
		acc = append(acc, item)
	}
	return acc
}

// classifyActivityKind is defined in activity.go (package-level).
