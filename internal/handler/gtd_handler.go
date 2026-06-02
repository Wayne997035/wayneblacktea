package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// githubPRURLRe is the canonical GitHub PR URL pattern from the shared validator
// package. Kept as a package-level alias so call-sites within this file remain
// unchanged while the single source of truth lives in internal/validator.
var githubPRURLRe = validator.GitHubPRURLRe

// errMsgInvalidPRURL is the user-facing error string returned when a supplied
// pr_url does not match the GitHub PR URL regex. Hoisted to a constant so the
// CreateTask / UpdateTask / BeginTask handlers all surface the same phrasing
// (and the goconst linter stays happy).
const errMsgInvalidPRURL = "pr_url must be a valid GitHub PR URL (https://github.com/owner/repo/pull/N)"

func validateBranchName(s string) string {
	// Count characters by rune, not byte: a 255-character CJK branch name
	// is well within git's branch-name limits but trips a byte-length check
	// because each char is 2-3 bytes in UTF-8.
	if len([]rune(s)) > 255 {
		return "branch_name must not exceed 255 characters"
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7F || unicode.Is(unicode.C, r) {
			return "branch_name must not contain control characters"
		}
	}
	return ""
}

func validateTaskStatus(s string) string {
	switch gtd.TaskStatus(s) {
	case gtd.TaskStatusPending, gtd.TaskStatusInProgress, gtd.TaskStatusCancelled:
		return ""
	default:
		return "status must be one of: pending, in_progress, cancelled"
	}
}

func validatePriority(n int32) string {
	if n < 1 || n > 5 {
		return "priority must be between 1 and 5"
	}
	return ""
}

func validateImportance(n int16) string {
	if n < 1 || n > 3 {
		return "importance must be between 1 and 3"
	}
	return ""
}

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
	RepoName    string     `json:"repo_name"`
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
		RepoName:    req.RepoName,
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
	if !gtd.ProjectStatus(req.Status).IsValid() {
		return c.JSON(http.StatusBadRequest, errResp("status must be one of: active, completed, archived, on_hold"))
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
	if tasks == nil {
		tasks = []db.Task{} // list endpoints MUST return [] not null (a nil slice marshals to JSON null and breaks frontend .length)
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
	Kind        string     `json:"kind"`
	Context     string     `json:"context"`
	Importance  int16      `json:"importance"`
	BranchName  string     `json:"branch_name"`
	PRUrl       string     `json:"pr_url"`
}

// validateCreateTaskRequest validates the optional extra fields on createTaskRequest
// and returns a user-facing error message, or "" if valid. This extracts branches
// from CreateTask to keep its cyclomatic complexity within the configured limit.
func validateCreateTaskRequest(req *createTaskRequest) string {
	if req.Importance != 0 {
		if msg := validateImportance(req.Importance); msg != "" {
			return msg
		}
	}
	if req.BranchName != "" {
		if msg := validateBranchName(req.BranchName); msg != "" {
			return msg
		}
	}
	if req.PRUrl != "" && !githubPRURLRe.MatchString(req.PRUrl) {
		return errMsgInvalidPRURL
	}
	return ""
}

// buildCreateTaskParams constructs gtd.CreateTaskParams from a validated request.
func buildCreateTaskParams(req *createTaskRequest, kind string) gtd.CreateTaskParams {
	p := gtd.CreateTaskParams{
		Title:       req.Title,
		ProjectID:   req.ProjectID,
		Description: req.Description,
		Assignee:    req.Assignee,
		Priority:    req.Priority,
		DueDate:     req.DueDate,
		Kind:        kind,
		Context:     req.Context,
	}
	if req.Importance != 0 {
		imp := req.Importance
		p.Importance = &imp
	}
	if req.BranchName != "" {
		bn := req.BranchName
		p.BranchName = &bn
	}
	if req.PRUrl != "" {
		pru := req.PRUrl
		p.PRUrl = &pru
	}
	return p
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

	kind := req.Kind
	if kind == "" {
		kind = "general"
	}
	if !validator.IsValidKind(kind) {
		return c.JSON(http.StatusBadRequest, errResp("kind must be one of: general, fix-pr, feature, refactor, research, chore"))
	}
	if msg := validateCreateTaskRequest(&req); msg != "" {
		return c.JSON(http.StatusBadRequest, errResp(msg))
	}

	// Vagueness and kind-field checks — warn-by-default, strict on opt-in.
	var allWarnings []string
	allWarnings = append(allWarnings, validator.CheckVagueness("description", req.Description, kind)...)
	allWarnings = append(allWarnings, validator.CheckKindFields(kind, req.Description)...)
	if len(allWarnings) > 0 && strictVagueness() {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":    "vagueness check failed",
			"warnings": allWarnings,
		})
	}

	task, err := h.store.CreateTask(c.Request().Context(), buildCreateTaskParams(&req, kind))
	if err != nil {
		c.Logger().Errorf("CreateTask: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	if len(allWarnings) > 0 {
		warningsJSON, _ := json.Marshal(allWarnings)
		c.Response().Header().Set("X-Vagueness-Warnings", string(warningsJSON))
	}
	return c.JSON(http.StatusCreated, task)
}

type updateTaskStatusRequest struct {
	Status string `json:"status"`
}

// UpdateTaskStatus sets the status of a task.
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
	switch gtd.TaskStatus(req.Status) {
	case gtd.TaskStatusPending, gtd.TaskStatusInProgress, gtd.TaskStatusCancelled:
	default:
		return c.JSON(http.StatusBadRequest, errResp("status must be one of: pending, in_progress, cancelled"))
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
	RepoName    *string    `json:"repo_name"` // nil → preserve; empty string → clear to NULL
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
		RepoName:    req.RepoName,
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
	BranchName  *string    `json:"branch_name"` // nil → preserve; empty string → clear to NULL
	PRUrl       *string    `json:"pr_url"`      // nil → preserve; empty string → clear to NULL
}

// updateTaskRequestIsEmpty returns true when all patch fields are nil (nothing to update).
func updateTaskRequestIsEmpty(req *updateTaskRequest) bool {
	return req.Title == nil && req.Description == nil && req.Priority == nil &&
		req.Importance == nil && req.Assignee == nil && req.DueDate == nil &&
		req.Context == nil && req.Status == nil &&
		req.BranchName == nil && req.PRUrl == nil
}

// validateUpdateTaskFields validates individual field values in the request,
// assuming the at-least-one-field check has already passed.
func validateUpdateTaskFields(req *updateTaskRequest) string {
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		return "title must not be empty"
	}
	if req.Priority != nil {
		if msg := validatePriority(*req.Priority); msg != "" {
			return msg
		}
	}
	if req.Importance != nil {
		if msg := validateImportance(*req.Importance); msg != "" {
			return msg
		}
	}
	if req.Status != nil {
		if msg := validateTaskStatus(*req.Status); msg != "" {
			return msg
		}
	}
	if req.BranchName != nil {
		if msg := validateBranchName(*req.BranchName); msg != "" {
			return msg
		}
	}
	if req.PRUrl != nil && *req.PRUrl != "" && !githubPRURLRe.MatchString(*req.PRUrl) {
		return errMsgInvalidPRURL
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
		BranchName:  req.BranchName,
		PRUrl:       req.PRUrl,
	}
}

// ListTasks returns all pending/in-progress tasks, optionally filtered by
// branch_name or pr_url query parameters. Filter is applied Go-side after
// fetching all tasks (personal-scale, low row count).
func (h *GTDHandler) ListTasks(c echo.Context) error {
	tasks, err := h.store.Tasks(c.Request().Context(), nil)
	if err != nil {
		c.Logger().Errorf("ListTasks: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	if tasks == nil {
		tasks = []db.Task{} // list endpoints MUST return [] not null (a nil slice marshals to JSON null and breaks frontend .length)
	}

	branchFilter := c.QueryParam("branch")
	prURLFilter := c.QueryParam("pr_url")

	if branchFilter == "" && prURLFilter == "" {
		return c.JSON(http.StatusOK, tasks)
	}

	// Apply Go-side filtering for new columns.
	filtered := tasks[:0]
	for _, t := range tasks {
		if branchFilter != "" && (!t.BranchName.Valid || t.BranchName.String != branchFilter) {
			continue
		}
		if prURLFilter != "" && (!t.PRUrl.Valid || t.PRUrl.String != prURLFilter) {
			continue
		}
		filtered = append(filtered, t)
	}
	return c.JSON(http.StatusOK, filtered)
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
// If the artifact is a GitHub PR URL or a 40-hex commit SHA, the corresponding
// task fields (pr_url / commit_shas) are updated as a side-effect so the HTTP
// path stays in parity with the MCP complete_task tool.
// SECURITY: only the string is stored — no HTTP fetch is made.
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

	if req.Artifact != nil && *req.Artifact != "" {
		if updated := gtd.ApplyArtifactSideEffects(c.Request().Context(), h.store, id, task, *req.Artifact); updated != nil {
			task = updated
		}
	}

	return c.JSON(http.StatusOK, task)
}

// checklistBodyLimit is the maximum request body size for checklist endpoints (32 KB).
const checklistBodyLimit = 32 * 1024

// workspaceUUIDFromStore converts a store's pgtype.UUID into a plain uuid.UUID.
// An invalid (zero) pgtype.UUID → zero uuid.UUID (unscoped mode).
func workspaceUUIDFromStore(s gtdStore) uuid.UUID {
	pg := s.WorkspaceID()
	if !pg.Valid {
		return uuid.UUID{}
	}
	return uuid.UUID(pg.Bytes)
}

type addChecklistItemRequest struct {
	Title   string `json:"title"`
	FileRef string `json:"file_ref"`
	Notes   string `json:"notes"`
}

// AddChecklistItem handles POST /api/tasks/:id/checklist/items.
// Body: {title (required), file_ref?, notes?}
// Response 200: full updated []ChecklistItem.
func (h *GTDHandler) AddChecklistItem(c echo.Context) error {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid task id"))
	}

	body, err := io.ReadAll(io.LimitReader(c.Request().Body, checklistBodyLimit))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("failed to read request body"))
	}

	var req addChecklistItemRequest
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}

	title := gtd.SanitiseChecklistText(req.Title)
	if title == "" {
		return c.JSON(http.StatusBadRequest, errResp("title is required"))
	}
	if len([]rune(title)) > gtd.ChecklistMaxTitle {
		return c.JSON(http.StatusBadRequest, errResp("title exceeds 500 characters"))
	}

	fileRef := gtd.SanitiseChecklistText(req.FileRef)
	notes := gtd.SanitiseChecklistText(req.Notes)
	if len([]rune(fileRef)) > gtd.ChecklistMaxText {
		return c.JSON(http.StatusBadRequest, errResp("file_ref exceeds 2000 characters"))
	}
	if len([]rune(notes)) > gtd.ChecklistMaxText {
		return c.JSON(http.StatusBadRequest, errResp("notes exceeds 2000 characters"))
	}

	item := gtd.ChecklistItem{
		ID:        uuid.New(),
		Title:     title,
		FileRef:   fileRef,
		Notes:     notes,
		CreatedAt: time.Now().UTC(),
	}

	wsID := workspaceUUIDFromStore(h.store)
	items, err := h.store.AddChecklistItem(c.Request().Context(), taskID, wsID, item)
	if err != nil {
		if errors.Is(err, gtd.ErrNotFound) {
			return c.JSON(http.StatusNotFound, errResp("task not found"))
		}
		c.Logger().Errorf("AddChecklistItem: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, items)
}

type updateChecklistItemRequest struct {
	Done        *bool   `json:"done"`
	Title       *string `json:"title"`
	Notes       *string `json:"notes"`
	EvidenceURL *string `json:"evidence_url"`
}

// UpdateChecklistItem handles PATCH /api/tasks/:id/checklist/items/:item_id.
// Body: {done?, title?, notes?, evidence_url?}
// Response 200: full updated []ChecklistItem.
func (h *GTDHandler) UpdateChecklistItem(c echo.Context) error {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid task id"))
	}
	itemID, err := uuid.Parse(c.Param("item_id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid item_id"))
	}

	body, err := io.ReadAll(io.LimitReader(c.Request().Body, checklistBodyLimit))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("failed to read request body"))
	}

	var req updateChecklistItemRequest
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}

	// Validate and sanitise optional text fields.
	update := gtd.UpdateChecklistItemParams{Done: req.Done}
	if req.Title != nil {
		t := gtd.SanitiseChecklistText(*req.Title)
		if t == "" {
			return c.JSON(http.StatusBadRequest, errResp("title must not be empty"))
		}
		if len([]rune(t)) > gtd.ChecklistMaxTitle {
			return c.JSON(http.StatusBadRequest, errResp("title exceeds 500 characters"))
		}
		update.Title = &t
	}
	if req.Notes != nil {
		n := gtd.SanitiseChecklistText(*req.Notes)
		if len([]rune(n)) > gtd.ChecklistMaxText {
			return c.JSON(http.StatusBadRequest, errResp("notes exceeds 2000 characters"))
		}
		update.Notes = &n
	}
	if req.EvidenceURL != nil {
		eu := gtd.SanitiseChecklistText(*req.EvidenceURL)
		if len([]rune(eu)) > gtd.ChecklistMaxText {
			return c.JSON(http.StatusBadRequest, errResp("evidence_url exceeds 2000 characters"))
		}
		update.EvidenceURL = &eu
	}

	wsID := workspaceUUIDFromStore(h.store)
	items, err := h.store.UpdateChecklistItem(c.Request().Context(), taskID, wsID, itemID, update)
	if err != nil {
		if errors.Is(err, gtd.ErrNotFound) {
			return c.JSON(http.StatusNotFound, errResp("task or item not found"))
		}
		c.Logger().Errorf("UpdateChecklistItem: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, items)
}

// DeleteChecklistItem handles DELETE /api/tasks/:id/checklist/items/:item_id.
// Response 204: no content on success.
func (h *GTDHandler) DeleteChecklistItem(c echo.Context) error {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid task id"))
	}
	itemID, err := uuid.Parse(c.Param("item_id"))
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid item_id"))
	}

	wsID := workspaceUUIDFromStore(h.store)
	if err := h.store.DeleteChecklistItem(c.Request().Context(), taskID, wsID, itemID); err != nil {
		if errors.Is(err, gtd.ErrNotFound) {
			return c.JSON(http.StatusNotFound, errResp("task or item not found"))
		}
		c.Logger().Errorf("DeleteChecklistItem: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.NoContent(http.StatusNoContent)
}

// beginTaskRequest is the optional JSON body for POST /api/tasks/:id/begin.
// Both fields are optional; when non-empty they are validated and persisted on
// the task row before the response is returned. This closes the GTD linkage
// gap (sprint feature/gtd-enforce-server-side GTD-fix 8/12) where Claude
// returned branch_name_suggestion but never persisted the actual linkage.
type beginTaskRequest struct {
	BranchName string `json:"branch_name"`
	PRUrl      string `json:"pr_url"`
}

// beginTaskBodyLimit caps the begin-task request body. Both fields are short
// strings (branch_name ≤ 255 chars, pr_url ≤ ~200 chars), so 8 KiB is generous
// while still bounding DoS / accidental large-paste risk.
const beginTaskBodyLimit = 8 * 1024

// BeginTask handles POST /api/tasks/:id/begin.
// It atomically marks the task in_progress and records a work_session_started
// activity log entry. Idempotent: calling on an already in_progress task
// returns the task without duplicate logging.
//
// Optional JSON body: {"branch_name"?: string, "pr_url"?: string}. When either
// field is supplied and validates, the task row is updated with the linkage
// BEFORE the response returns (sprint feature/gtd-enforce-server-side
// GTD-fix 8/12 — closes the opt-in linkage gap surfaced by today's 5 stale
// tasks).
func (h *GTDHandler) BeginTask(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid task id")
	}

	req, parseErr := parseBeginTaskBody(c)
	if parseErr != nil {
		return parseErr
	}
	if vmsg := validateBeginTaskLinkage(req); vmsg != "" {
		return echo.NewHTTPError(http.StatusBadRequest, vmsg)
	}

	wsID := workspaceUUIDFromStore(h.store)
	task, err := h.store.BeginTask(c.Request().Context(), id, wsID)
	if errors.Is(err, gtd.ErrNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "task not found")
	}
	if err != nil {
		c.Logger().Errorf("BeginTask: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "internal server error")
	}

	// Persist branch_name / pr_url linkage after the status flip.
	// We deliberately call UpdateTask (not BeginTask) because BeginTask is
	// designed to be idempotent and only flips status — extending it would
	// risk masking pre-flip linkage updates as no-ops on the second call.
	if req.BranchName != "" || req.PRUrl != "" {
		updated, uerr := h.store.UpdateTask(c.Request().Context(), id, buildBeginTaskUpdateParams(req))
		if uerr != nil {
			// Status was already flipped to in_progress; surface the persist
			// failure but log so operators can detect drift.
			c.Logger().Errorf("BeginTask linkage persist: %v", uerr)
			return echo.NewHTTPError(http.StatusInternalServerError, "internal server error")
		}
		task = updated
	}

	return c.JSON(http.StatusOK, map[string]any{
		"task":                   task,
		"branch_name_suggestion": gtd.TitleToBranchSlug(task.Title),
		"work_session_id":        uuid.New().String(),
	})
}

// parseBeginTaskBody reads the optional JSON body. Empty body → zero-value req
// (legacy contract preserved). Returns a 400 echo.HTTPError on read/parse fail.
func parseBeginTaskBody(c echo.Context) (beginTaskRequest, error) {
	var req beginTaskRequest
	if c.Request().ContentLength == 0 {
		return req, nil
	}
	body, readErr := io.ReadAll(io.LimitReader(c.Request().Body, beginTaskBodyLimit))
	if readErr != nil {
		return req, echo.NewHTTPError(http.StatusBadRequest, "failed to read request body")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return req, nil
	}
	if jsonErr := json.NewDecoder(bytes.NewReader(body)).Decode(&req); jsonErr != nil {
		return req, echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	return req, nil
}

// validateBeginTaskLinkage returns a non-empty user-facing error message when
// either branch_name or pr_url is supplied but invalid. "" on valid (or empty).
func validateBeginTaskLinkage(req beginTaskRequest) string {
	if req.BranchName != "" {
		if msg := validateBranchName(req.BranchName); msg != "" {
			return msg
		}
	}
	if req.PRUrl != "" && !githubPRURLRe.MatchString(req.PRUrl) {
		return errMsgInvalidPRURL
	}
	return ""
}

// buildBeginTaskUpdateParams turns the validated request into UpdateTaskParams.
// Only non-empty fields are set, matching the partial-update semantics.
func buildBeginTaskUpdateParams(req beginTaskRequest) gtd.UpdateTaskParams {
	up := gtd.UpdateTaskParams{}
	if req.BranchName != "" {
		bn := req.BranchName
		up.BranchName = &bn
	}
	if req.PRUrl != "" {
		pu := req.PRUrl
		up.PRUrl = &pu
	}
	return up
}
