package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// GTDHandler handles all GTD-domain endpoints.
type GTDHandler struct {
	store gtdStore
}

// NewGTDHandler creates a GTDHandler.
func NewGTDHandler(s gtdStore) *GTDHandler {
	return &GTDHandler{store: s}
}

// ListGoals returns all active goals.
func (h *GTDHandler) ListGoals(c echo.Context) error {
	goals, err := h.store.ActiveGoals(c.Request().Context())
	if err != nil {
		c.Logger().Errorf("ListGoals: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, goals)
}

type createGoalRequest struct {
	Title       string     `json:"title"`
	Area        string     `json:"area"`
	Description string     `json:"description"`
	DueDate     *time.Time `json:"due_date"`
}

// CreateGoal inserts a new goal.
func (h *GTDHandler) CreateGoal(c echo.Context) error {
	var req createGoalRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if req.Title == "" {
		return c.JSON(http.StatusBadRequest, errResp("title is required"))
	}

	goal, err := h.store.CreateGoal(c.Request().Context(), gtd.CreateGoalParams{
		Title:       req.Title,
		Area:        req.Area,
		Description: req.Description,
		DueDate:     req.DueDate,
	})
	if err != nil {
		c.Logger().Errorf("CreateGoal: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusCreated, goal)
}

// ListProjects returns all active projects.
func (h *GTDHandler) ListProjects(c echo.Context) error {
	projects, err := h.store.ListActiveProjects(c.Request().Context())
	if err != nil {
		c.Logger().Errorf("ListProjects: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, projects)
}

type createProjectRequest struct {
	Name        string     `json:"name"`
	Title       string     `json:"title"`
	Area        string     `json:"area"`
	Description string     `json:"description"`
	GoalID      *uuid.UUID `json:"goal_id"`
	Priority    int32      `json:"priority"`
}

// CreateProject inserts a new project.
func (h *GTDHandler) CreateProject(c echo.Context) error {
	var req createProjectRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if req.Name == "" || req.Title == "" {
		return c.JSON(http.StatusBadRequest, errResp("name and title are required"))
	}

	project, err := h.store.CreateProject(c.Request().Context(), gtd.CreateProjectParams{
		Name:        req.Name,
		Title:       req.Title,
		Area:        req.Area,
		Description: req.Description,
		GoalID:      req.GoalID,
		Priority:    req.Priority,
	})
	if err != nil {
		if errors.Is(err, gtd.ErrConflict) {
			return c.JSON(http.StatusConflict, errResp("project name already exists"))
		}
		c.Logger().Errorf("CreateProject: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusCreated, project)
}

// GetProject returns a single project by ID (UUID path param).
func (h *GTDHandler) GetProject(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid project id"))
	}
	project, err := h.store.GetProjectByID(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, gtd.ErrNotFound) {
			return c.JSON(http.StatusNotFound, errResp("project not found"))
		}
		c.Logger().Errorf("GetProject: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, project)
}

type updateProjectStatusRequest struct {
	Status string `json:"status"`
}

// UpdateProjectStatus updates a project's status.
//
//nolint:dupl // intentionally parallel handlers for project and task — same pattern, different entity
func (h *GTDHandler) UpdateProjectStatus(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid project id"))
	}

	var req updateProjectStatusRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if req.Status == "" {
		return c.JSON(http.StatusBadRequest, errResp("status is required"))
	}

	project, err := h.store.UpdateProjectStatus(c.Request().Context(), id, gtd.ProjectStatus(req.Status))
	if err != nil {
		if errors.Is(err, gtd.ErrNotFound) {
			return c.JSON(http.StatusNotFound, errResp("project not found"))
		}
		c.Logger().Errorf("UpdateProjectStatus: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, project)
}

// ListProjectTasks returns tasks for a specific project.
//
// Query params:
//
//   - status=all → return every task regardless of status, ordered by
//     COALESCE(updated_at, created_at) DESC. Used by the project-detail UI to
//     render the "completed" section.
//   - any other value (or unset) → default behaviour: only pending /
//     in_progress tasks. Preserves the prior contract so existing GTD list
//     pages do not regress.
//
// Unknown status values are treated as the default rather than 400 so future
// clients passing experimental filters degrade gracefully; the only opt-in
// is the explicit `all` token.
func (h *GTDHandler) ListProjectTasks(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid project id"))
	}

	var (
		tasks    []db.Task
		queryErr error
	)
	if c.QueryParam("status") == statusAll {
		tasks, queryErr = h.store.TasksByProjectAllStatuses(c.Request().Context(), id)
	} else {
		tasks, queryErr = h.store.Tasks(c.Request().Context(), &id)
	}
	if queryErr != nil {
		c.Logger().Errorf("ListProjectTasks: %v", queryErr)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, tasks)
}

type createTaskRequest struct {
	Title       string     `json:"title"`
	ProjectID   *uuid.UUID `json:"project_id"`
	Description string     `json:"description"`
	Assignee    string     `json:"assignee"`
	Priority    int32      `json:"priority"`
	DueDate     *time.Time `json:"due_date"`
}

// CreateTask inserts a new task.
func (h *GTDHandler) CreateTask(c echo.Context) error {
	var req createTaskRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if req.Title == "" {
		return c.JSON(http.StatusBadRequest, errResp("title is required"))
	}

	task, err := h.store.CreateTask(c.Request().Context(), gtd.CreateTaskParams{
		Title:       req.Title,
		ProjectID:   req.ProjectID,
		Description: req.Description,
		Assignee:    req.Assignee,
		Priority:    req.Priority,
		DueDate:     req.DueDate,
	})
	if err != nil {
		c.Logger().Errorf("CreateTask: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusCreated, task)
}

type updateTaskStatusRequest struct {
	Status string `json:"status"`
}

// UpdateTaskStatus sets the status of a task.
//
//nolint:dupl // intentionally parallel handlers for project and task — same pattern, different entity
func (h *GTDHandler) UpdateTaskStatus(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid task id"))
	}

	var req updateTaskStatusRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if req.Status == "" {
		return c.JSON(http.StatusBadRequest, errResp("status is required"))
	}

	task, err := h.store.UpdateTaskStatus(c.Request().Context(), id, gtd.TaskStatus(req.Status))
	if err != nil {
		if errors.Is(err, gtd.ErrNotFound) {
			return c.JSON(http.StatusNotFound, errResp("task not found"))
		}
		c.Logger().Errorf("UpdateTaskStatus: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, task)
}

type updateGoalRequest struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Area        string  `json:"area"`
	Status      string  `json:"status"`
	DueDate     *string `json:"due_date"`
}

// UpdateGoal handles PATCH /api/goals/:id — full update of a goal's mutable fields.
func (h *GTDHandler) UpdateGoal(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid goal id"))
	}

	var req updateGoalRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if strings.TrimSpace(req.Title) == "" {
		return c.JSON(http.StatusBadRequest, errResp("title is required"))
	}

	var dueDate *time.Time
	if req.DueDate != nil {
		t, parseErr := time.Parse(time.RFC3339, *req.DueDate)
		if parseErr != nil {
			return c.JSON(http.StatusBadRequest, errResp("due_date must be RFC3339"))
		}
		dueDate = &t
	}

	status := gtd.GoalStatus(req.Status)
	if status == "" {
		status = gtd.GoalStatusActive
	}
	if !status.IsValid() {
		return c.JSON(http.StatusBadRequest, errResp("invalid status value"))
	}

	goal, err := h.store.UpdateGoal(c.Request().Context(), id, gtd.UpdateGoalParams{
		Title:       strings.TrimSpace(req.Title),
		Description: req.Description,
		Area:        req.Area,
		Status:      status,
		DueDate:     dueDate,
	})
	if err != nil {
		if errors.Is(err, gtd.ErrNotFound) {
			return c.JSON(http.StatusNotFound, errResp("goal not found"))
		}
		c.Logger().Errorf("UpdateGoal: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, goal)
}

type updateProjectRequest struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Area        string     `json:"area"`
	Priority    int32      `json:"priority"`
	Status      string     `json:"status"`
	GoalID      *uuid.UUID `json:"goal_id"`
}

// UpdateProject handles PATCH /api/projects/:id — full update of a project's mutable fields.
func (h *GTDHandler) UpdateProject(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid project id"))
	}

	var req updateProjectRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if strings.TrimSpace(req.Title) == "" {
		return c.JSON(http.StatusBadRequest, errResp("title is required"))
	}

	status := gtd.ProjectStatus(req.Status)
	if status == "" {
		status = gtd.ProjectStatusActive
	}
	if !status.IsValid() {
		return c.JSON(http.StatusBadRequest, errResp("invalid status value"))
	}
	if req.Priority != 0 && (req.Priority < 1 || req.Priority > 5) {
		return c.JSON(http.StatusBadRequest, errResp("priority must be between 1 and 5"))
	}

	project, err := h.store.UpdateProject(c.Request().Context(), id, gtd.UpdateProjectParams{
		Title:       strings.TrimSpace(req.Title),
		Description: req.Description,
		Area:        req.Area,
		Priority:    req.Priority,
		Status:      status,
		GoalID:      req.GoalID,
	})
	if err != nil {
		if errors.Is(err, gtd.ErrNotFound) {
			return c.JSON(http.StatusNotFound, errResp("project not found"))
		}
		c.Logger().Errorf("UpdateProject: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, project)
}

type updateTaskRequest struct {
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	Priority    *int32     `json:"priority"`
	Importance  *int16     `json:"importance"`
	Assignee    *string    `json:"assignee"`
	DueDate     *time.Time `json:"due_date"`
	Context     *string    `json:"context"`
	Status      *string    `json:"status"`
}

// updateTaskRequestIsEmpty returns true when all patch fields are nil (nothing to update).
func updateTaskRequestIsEmpty(req *updateTaskRequest) bool {
	return req.Title == nil && req.Description == nil && req.Priority == nil &&
		req.Importance == nil && req.Assignee == nil && req.DueDate == nil &&
		req.Context == nil && req.Status == nil
}

// validateUpdateTaskFields validates individual field values in the request,
// assuming the at-least-one-field check has already passed.
func validateUpdateTaskFields(req *updateTaskRequest) string {
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		return "title must not be empty"
	}
	if req.Priority != nil && (*req.Priority < 1 || *req.Priority > 5) {
		return "priority must be between 1 and 5"
	}
	if req.Importance != nil && (*req.Importance < 1 || *req.Importance > 3) {
		return "importance must be between 1 and 3"
	}
	if req.Status != nil {
		switch gtd.TaskStatus(*req.Status) {
		case gtd.TaskStatusPending, gtd.TaskStatusInProgress, gtd.TaskStatusCancelled:
			// valid
		default:
			return "status must be one of: pending, in_progress, cancelled"
		}
	}
	return ""
}

// validateUpdateTaskRequest validates the update request and returns a user-facing
// error message, or "" if valid.
func validateUpdateTaskRequest(req *updateTaskRequest) string {
	if updateTaskRequestIsEmpty(req) {
		return "at least one field is required"
	}
	return validateUpdateTaskFields(req)
}

// updateTaskParamsFromRequest converts a validated updateTaskRequest to gtd.UpdateTaskParams,
// normalizing fields such as trimming title whitespace.
func updateTaskParamsFromRequest(req *updateTaskRequest) gtd.UpdateTaskParams {
	var title *string
	if req.Title != nil {
		trimmed := strings.TrimSpace(*req.Title)
		title = &trimmed
	}
	return gtd.UpdateTaskParams{
		Title:       title,
		Description: req.Description,
		Priority:    req.Priority,
		Importance:  req.Importance,
		Assignee:    req.Assignee,
		DueDate:     req.DueDate,
		Context:     req.Context,
		Status:      req.Status,
	}
}

// UpdateTask handles PATCH /api/tasks/:id — partial update of a task's mutable fields.
// At least one field must be provided. status="completed" is rejected (use CompleteTask).
func (h *GTDHandler) UpdateTask(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid task id"))
	}

	var req updateTaskRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}

	if msg := validateUpdateTaskRequest(&req); msg != "" {
		return c.JSON(http.StatusBadRequest, errResp(msg))
	}

	task, err := h.store.UpdateTask(c.Request().Context(), id, updateTaskParamsFromRequest(&req))
	if err != nil {
		if errors.Is(err, gtd.ErrNotFound) {
			return c.JSON(http.StatusNotFound, errResp("task not found"))
		}
		c.Logger().Errorf("UpdateTask: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, task)
}

type completeTaskRequest struct {
	Artifact *string `json:"artifact"`
}

// CompleteTask marks a task as completed.
func (h *GTDHandler) CompleteTask(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid task id"))
	}

	var req completeTaskRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}

	task, err := h.store.CompleteTask(c.Request().Context(), id, req.Artifact)
	if err != nil {
		if errors.Is(err, gtd.ErrNotFound) {
			return c.JSON(http.StatusNotFound, errResp("task not found"))
		}
		c.Logger().Errorf("CompleteTask: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, task)
}
