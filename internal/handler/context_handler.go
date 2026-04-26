package handler

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/waynechen/wayneblacktea/internal/db"
	"github.com/waynechen/wayneblacktea/internal/session"
)

// ContextHandler handles the /api/context endpoints.
type ContextHandler struct {
	gtd  gtdStore
	sess sessionStore
}

// NewContextHandler creates a ContextHandler.
func NewContextHandler(g gtdStore, s sessionStore) *ContextHandler {
	return &ContextHandler{gtd: g, sess: s}
}

type weeklyProgressResponse struct {
	Completed int64 `json:"completed"`
	Total     int64 `json:"total"`
}

type todayContextResponse struct {
	Goals          []db.Goal              `json:"goals"`
	Projects       []db.Project           `json:"projects"`
	WeeklyProgress weeklyProgressResponse `json:"weekly_progress"`
	PendingHandoff *db.SessionHandoff     `json:"pending_handoff"`
}

// GetTodayContext returns active goals, projects, weekly progress and pending handoff.
func (h *ContextHandler) GetTodayContext(c echo.Context) error {
	ctx := c.Request().Context()

	goals, err := h.gtd.ActiveGoals(ctx)
	if err != nil {
		c.Logger().Errorf("GetTodayContext loading goals: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	projects, err := h.gtd.ListActiveProjects(ctx)
	if err != nil {
		c.Logger().Errorf("GetTodayContext loading projects: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	completed, total, err := h.gtd.WeeklyProgress(ctx)
	if err != nil {
		c.Logger().Errorf("GetTodayContext loading weekly progress: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	handoff, err := h.sess.LatestHandoff(ctx)
	if err != nil && !errors.Is(err, session.ErrNotFound) {
		c.Logger().Errorf("GetTodayContext loading handoff: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	return c.JSON(http.StatusOK, todayContextResponse{
		Goals:    goals,
		Projects: projects,
		WeeklyProgress: weeklyProgressResponse{
			Completed: completed,
			Total:     total,
		},
		PendingHandoff: handoff,
	})
}
