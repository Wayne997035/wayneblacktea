package handler

import (
	"errors"
	"net/http"
	"time"

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

	// We fetch by updating status with same value to retrieve — but actually we
	// look through active projects. The store has no GetByID, so we list and filter.
	// Use UpdateProjectStatus to get the row (not ideal but avoids new query).
	// Actually, list active projects and find the one with matching ID.
	projects, err := h.store.ListActiveProjects(c.Request().Context())
	if err != nil {
		c.Logger().Errorf("GetProject: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	for _, p := range projects {
		if p.ID == id {
			return c.JSON(http.StatusOK, p)
		}
	}
	return c.JSON(http.StatusNotFound, errResp("project not found"))
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
func (h *GTDHandler) ListProjectTasks(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid project id"))
	}

	tasks, err := h.store.Tasks(c.Request().Context(), &id)
	if err != nil {
		c.Logger().Errorf("ListProjectTasks: %v", err)
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
