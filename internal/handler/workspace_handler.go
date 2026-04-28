package handler

import (
	"net/http"

	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/labstack/echo/v4"
)

// WorkspaceHandler handles the /api/workspace endpoints.
type WorkspaceHandler struct {
	store workspaceStore
}

// NewWorkspaceHandler creates a WorkspaceHandler.
func NewWorkspaceHandler(s workspaceStore) *WorkspaceHandler {
	return &WorkspaceHandler{store: s}
}

// ListRepos returns all active repos.
func (h *WorkspaceHandler) ListRepos(c echo.Context) error {
	repos, err := h.store.ActiveRepos(c.Request().Context())
	if err != nil {
		c.Logger().Errorf("ListRepos: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, repos)
}

type upsertRepoRequest struct {
	Name            string   `json:"name"`
	Path            string   `json:"path"`
	Description     string   `json:"description"`
	Language        string   `json:"language"`
	CurrentBranch   string   `json:"current_branch"`
	KnownIssues     []string `json:"known_issues"`
	NextPlannedStep string   `json:"next_planned_step"`
}

// UpsertRepo creates or updates a repo.
func (h *WorkspaceHandler) UpsertRepo(c echo.Context) error {
	var req upsertRepoRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if req.Name == "" {
		return c.JSON(http.StatusBadRequest, errResp("name is required"))
	}

	repo, err := h.store.UpsertRepo(c.Request().Context(), workspace.UpsertRepoParams{
		Name:            req.Name,
		Path:            req.Path,
		Description:     req.Description,
		Language:        req.Language,
		CurrentBranch:   req.CurrentBranch,
		KnownIssues:     req.KnownIssues,
		NextPlannedStep: req.NextPlannedStep,
	})
	if err != nil {
		c.Logger().Errorf("UpsertRepo: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, repo)
}
