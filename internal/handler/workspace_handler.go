package handler

import (
	"net/http"

	"github.com/Wayne997035/wayneblacktea/internal/validator"
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

// upsertRepoRequest's optional fields are *string (not string) so
// encoding/json's standard pointer-unmarshal behaviour distinguishes "key
// absent from the JSON body" (nil, preserve stored value) from "key present
// with an empty string" (non-nil *string pointing at "", explicit clear) —
// Ω6, 2026-08-20-mcp-surface-spec.md. A plain string field folds both into
// "", which is the omission-clobber bug this type change closes on the HTTP
// path (workspace.UpsertRepoParams already required this type on the Go
// side once its fields became presence-aware).
type upsertRepoRequest struct {
	Name            string   `json:"name"`
	Path            *string  `json:"path"`
	Description     *string  `json:"description"`
	Language        *string  `json:"language"`
	CurrentBranch   *string  `json:"current_branch"`
	KnownIssues     []string `json:"known_issues"`
	NextPlannedStep *string  `json:"next_planned_step"`
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
	if !validator.RepoSlugRe.MatchString(req.Name) {
		return c.JSON(http.StatusBadRequest, errResp("name must match owner/repo slug pattern"))
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

// workspaceSettings is the GET/PATCH /api/workspace/settings response/request body.
type workspaceSettings struct {
	ModelPreference string `json:"model_preference"`
}

// GetSettings returns the workspace's AI model preference.
func (h *WorkspaceHandler) GetSettings(c echo.Context) error {
	model, err := h.store.GetModelPreference(c.Request().Context())
	if err != nil {
		c.Logger().Errorf("GetSettings: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, workspaceSettings{ModelPreference: model})
}

// PatchSettings updates the workspace's AI model preference. The model MUST be
// in workspace.AllowedModels (explicit whitelist — arbitrary strings rejected).
func (h *WorkspaceHandler) PatchSettings(c echo.Context) error {
	var req workspaceSettings
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if req.ModelPreference == "" {
		return c.JSON(http.StatusBadRequest, errResp("model_preference is required"))
	}
	if !workspace.IsAllowedModel(req.ModelPreference) {
		return c.JSON(http.StatusBadRequest, errResp("model not in allowed list"))
	}
	if err := h.store.UpsertModelPreference(c.Request().Context(), req.ModelPreference); err != nil {
		c.Logger().Errorf("PatchSettings: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, workspaceSettings{ModelPreference: req.ModelPreference})
}
