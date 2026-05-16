package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// embedTimeout caps the Gemini HTTP call + DB write inside the async goroutine.
// Gemini embedding-001 p99 is ~2 s; 25 s leaves ample room while ensuring
// goroutines do not outlive a Railway SIGTERM drain window (~30 s default).
const embedTimeout = 25 * time.Second

// SessionHandler handles the /api/session endpoints.
type SessionHandler struct {
	store    sessionStore
	embedder localai.ContextEmbeddingProvider
}

// NewSessionHandler creates a SessionHandler.
func NewSessionHandler(s sessionStore) *SessionHandler {
	return &SessionHandler{store: s}
}

// WithEmbedder wires an embedding provider so that each new session handoff
// has its intent+summary embedded asynchronously after the HTTP response is
// sent. The embedding enables cosine-similarity recall in wbt-context.
func (h *SessionHandler) WithEmbedder(e localai.ContextEmbeddingProvider) *SessionHandler {
	h.embedder = e
	return h
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

// SetHandoff records a new session handoff and async-embeds it when an
// embedding provider is configured (GEMINI_API_KEY set).
func (h *SessionHandler) SetHandoff(c echo.Context) error {
	var req setHandoffRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if req.Intent == "" {
		return c.JSON(http.StatusBadRequest, errResp("intent is required"))
	}

	// Vagueness check on intent field (warn-only; handoffs are not task descriptions).
	if warnings := validator.CheckVagueness("intent", req.Intent, "general"); len(warnings) > 0 {
		warningsJSON, _ := json.Marshal(warnings)
		c.Response().Header().Set("X-Vagueness-Warnings", string(warningsJSON))
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

	if h.embedder != nil {
		text := req.Intent
		if req.ContextSummary != "" {
			text += "\n" + req.ContextSummary
		}
		handoffID := handoff.ID
		store := h.store
		embedder := h.embedder
		go func(id uuid.UUID) {
			// Bounded context: goroutine must not outlive server shutdown drain.
			// request ctx is cancelled on response — use independent timeout.
			ctx, cancel := context.WithTimeout(context.Background(), embedTimeout)
			defer cancel()
			vec, err := embedder.Embed(ctx, text)
			if err != nil || len(vec) == 0 {
				if err != nil {
					slog.Warn("session embed: embedding failed", "err", err)
				}
				return
			}
			if err := store.UpdateEmbeddingByID(ctx, id, localai.SerializeEmbedding(vec)); err != nil {
				slog.Warn("session embed: UpdateEmbeddingByID failed", "err", err)
			}
		}(handoffID)
	}

	return c.JSON(http.StatusCreated, handoff)
}
