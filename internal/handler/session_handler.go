package handler

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/waynechen/wayneblacktea/internal/session"
)

// SessionHandler handles the /api/session endpoints.
type SessionHandler struct {
	store sessionStore
}

// NewSessionHandler creates a SessionHandler.
func NewSessionHandler(s sessionStore) *SessionHandler {
	return &SessionHandler{store: s}
}

// GetHandoff returns the latest unresolved session handoff, or null if none.
func (h *SessionHandler) GetHandoff(c echo.Context) error {
	handoff, err := h.store.LatestHandoff(c.Request().Context())
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return c.JSON(http.StatusOK, nil)
		}
		c.Logger().Errorf("GetHandoff: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, handoff)
}

type setHandoffRequest struct {
	Intent         string     `json:"intent"`
	RepoName       string     `json:"repo_name"`
	ProjectID      *uuid.UUID `json:"project_id"`
	ContextSummary string     `json:"context_summary"`
}

// SetHandoff records a new session handoff.
func (h *SessionHandler) SetHandoff(c echo.Context) error {
	var req setHandoffRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if req.Intent == "" {
		return c.JSON(http.StatusBadRequest, errResp("intent is required"))
	}

	handoff, err := h.store.SetHandoff(c.Request().Context(), session.HandoffParams{
		Intent:         req.Intent,
		RepoName:       req.RepoName,
		ProjectID:      req.ProjectID,
		ContextSummary: req.ContextSummary,
	})
	if err != nil {
		c.Logger().Errorf("SetHandoff: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusCreated, handoff)
}
