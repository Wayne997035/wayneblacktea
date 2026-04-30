package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

const autologBodyLimit = 64 * 1024 // 64 KB

// AutologHandler handles the /api/activity and /api/auto-handoff endpoints.
type AutologHandler struct {
	gtd      autologGTDStore
	sess     autologSessionStore
	decision autologDecisionStore
}

// NewAutologHandler creates an AutologHandler.
func NewAutologHandler(g autologGTDStore, s autologSessionStore, d autologDecisionStore) *AutologHandler {
	return &AutologHandler{gtd: g, sess: s, decision: d}
}

type logActivityRequest struct {
	Actor     string     `json:"actor"`
	Action    string     `json:"action"`
	Notes     string     `json:"notes"`
	ProjectID *uuid.UUID `json:"project_id"`
}

// LogActivity handles POST /api/activity.
func (h *AutologHandler) LogActivity(c echo.Context) error {
	body := io.LimitReader(c.Request().Body, autologBodyLimit)

	var req logActivityRequest
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}
	if req.Actor == "" || req.Action == "" {
		return c.JSON(http.StatusBadRequest, errResp("actor and action are required"))
	}

	if err := h.gtd.LogActivity(c.Request().Context(), req.Actor, req.Action, req.ProjectID, req.Notes); err != nil {
		c.Logger().Errorf("LogActivity: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// AutoHandoff handles POST /api/auto-handoff.
// It reads in-progress tasks and recent decisions, then creates a session handoff row.
func (h *AutologHandler) AutoHandoff(c echo.Context) error {
	ctx := c.Request().Context()

	// Drain any body safely (none expected, but be defensive).
	_, _ = io.Copy(io.Discard, io.LimitReader(c.Request().Body, autologBodyLimit))

	// 1. Collect in-progress task titles.
	tasks, err := h.gtd.Tasks(ctx, nil)
	if err != nil {
		c.Logger().Errorf("AutoHandoff tasks: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	var inProgress []string
	for _, t := range tasks {
		if t.Status == "in_progress" {
			inProgress = append(inProgress, t.Title)
		}
	}

	// 2. Collect recent decision titles (up to 5).
	decisions, err := h.decision.All(ctx, 5)
	if err != nil {
		c.Logger().Errorf("AutoHandoff decisions: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	var decTitles []string
	for _, d := range decisions {
		decTitles = append(decTitles, d.Title)
	}

	// 3. Build intent and summary strings mechanically.
	intent := fmt.Sprintf("Auto-handoff: in_progress=[%s] recent_decisions=[%s]",
		strings.Join(inProgress, ", "),
		strings.Join(decTitles, ", "),
	)

	// 4. Create session handoff.
	handoff, err := h.sess.SetHandoff(ctx, session.HandoffParams{
		Intent:         intent,
		ContextSummary: intent,
	})
	if err != nil {
		c.Logger().Errorf("AutoHandoff SetHandoff: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, map[string]string{
		"status":     "ok",
		"handoff_id": handoff.ID.String(),
	})
}
