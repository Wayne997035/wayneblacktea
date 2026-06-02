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
	Intent         string          `json:"intent"`
	RepoName       string          `json:"repo_name"`
	ProjectID      *uuid.UUID      `json:"project_id"`
	ContextSummary string          `json:"context_summary"`
	NextActions    json.RawMessage `json:"next_actions"` // optional []session.NextAction; nil/[] → empty slice
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

	var nextActions []session.NextAction
	if len(req.NextActions) > 0 && string(req.NextActions) != "[]" && string(req.NextActions) != "null" {
		if err := json.Unmarshal(req.NextActions, &nextActions); err != nil {
			return c.JSON(http.StatusBadRequest, errResp("next_actions must be a valid JSON array of next-action objects"))
		}
	}

	handoff, err := h.store.SetHandoff(c.Request().Context(), session.HandoffParams{
		Intent:         req.Intent,
		RepoName:       req.RepoName,
		ProjectID:      req.ProjectID,
		ContextSummary: req.ContextSummary,
		NextActions:    nextActions,
	})
	if err != nil {
		c.Logger().Errorf("SetHandoff: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	h.embedHandoffAsync(handoff.ID, req.Intent, req.ContextSummary)

	return c.JSON(http.StatusCreated, handoff)
}

// embedHandoffAsync embeds the handoff text in a bounded background goroutine and
// writes the embedding + provider metadata. No-op when no embedder is configured
// (GEMINI_API_KEY unset). Extracted from SetHandoff to keep that handler's
// cyclomatic complexity within budget.
func (h *SessionHandler) embedHandoffAsync(handoffID uuid.UUID, intent, contextSummary string) {
	if h.embedder == nil {
		return
	}
	text := intent
	if contextSummary != "" {
		text += "\n" + contextSummary
	}
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
		// Dimension guard before serialising: reject any non-provider dimension
		// (Gemini 768 / hashed 32) so a corrupted embedding can't poison recall.
		if len(vec) != localai.GeminiEmbeddingDims && len(vec) != localai.HashedEmbeddingDims {
			slog.Warn("session embed: embedding has unknown dimension; skipping write", "dim", len(vec))
			return
		}
		providerTag := localai.ProviderTagFromDim(len(vec))
		if err := store.UpdateEmbeddingByID(ctx, id, localai.SerializeEmbedding(vec), providerTag, len(vec)); err != nil {
			slog.Warn("session embed: UpdateEmbeddingByID failed", "err", err)
		}
	}(handoffID)
}
