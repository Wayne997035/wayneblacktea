package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// DecisionHandler handles the /api/decisions endpoints.
type DecisionHandler struct {
	store decisionStore
}

// NewDecisionHandler creates a DecisionHandler.
func NewDecisionHandler(s decisionStore) *DecisionHandler {
	return &DecisionHandler{store: s}
}

// ListDecisions returns decisions, optionally filtered by repo_name or project_id query params.
// The ?limit= query parameter controls how many results to return (default 20, max 100).
func (h *DecisionHandler) ListDecisions(c echo.Context) error {
	const (
		defaultLimit int32 = 20
		maxLimit     int32 = 100
	)

	limit := defaultLimit
	if rawLimit := c.QueryParam("limit"); rawLimit != "" {
		n, err := strconv.ParseInt(rawLimit, 10, 32)
		switch {
		case err != nil || n < 1:
			limit = defaultLimit
		case int32(n) > maxLimit:
			limit = maxLimit
		default:
			limit = int32(n)
		}
	}

	ctx := c.Request().Context()

	if projectIDStr := c.QueryParam("project_id"); projectIDStr != "" {
		id, err := uuid.Parse(projectIDStr)
		if err != nil {
			return c.JSON(http.StatusBadRequest, errResp("invalid project_id"))
		}
		decisions, err := h.store.ByProject(ctx, id, limit)
		if err != nil {
			c.Logger().Errorf("ListDecisions ByProject: %v", err)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
		return c.JSON(http.StatusOK, decisions)
	}

	if repoName := c.QueryParam("repo_name"); repoName != "" {
		decisions, err := h.store.ByRepo(ctx, repoName, limit)
		if err != nil {
			c.Logger().Errorf("ListDecisions ByRepo: %v", err)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
		return c.JSON(http.StatusOK, decisions)
	}

	decisions, err := h.store.All(ctx, limit)
	if err != nil {
		c.Logger().Errorf("ListDecisions All: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, decisions)
}

type logDecisionRequest struct {
	Title        string     `json:"title"`
	Context      string     `json:"context"`
	Decision     string     `json:"decision"`
	Rationale    string     `json:"rationale"`
	RepoName     string     `json:"repo_name"`
	ProjectID    *uuid.UUID `json:"project_id"`
	Alternatives string     `json:"alternatives"`
}

// LogDecision records a new architectural decision.
func (h *DecisionHandler) LogDecision(c echo.Context) error {
	var req logDecisionRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if req.Title == "" || req.Context == "" || req.Decision == "" || req.Rationale == "" {
		return c.JSON(http.StatusBadRequest, errResp("title, context, decision and rationale are required"))
	}

	// Vagueness check on rationale (warn-only; decisions are not task descriptions).
	if warnings := validator.CheckVagueness("rationale", req.Rationale, "general"); len(warnings) > 0 {
		warningsJSON, _ := json.Marshal(warnings)
		c.Response().Header().Set("X-Vagueness-Warnings", string(warningsJSON))
	}

	d, err := h.store.Log(c.Request().Context(), decision.LogParams{
		Title:        req.Title,
		Context:      req.Context,
		Decision:     req.Decision,
		Rationale:    req.Rationale,
		RepoName:     req.RepoName,
		ProjectID:    req.ProjectID,
		Alternatives: req.Alternatives,
	})
	if err != nil {
		c.Logger().Errorf("LogDecision: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusCreated, d)
}
