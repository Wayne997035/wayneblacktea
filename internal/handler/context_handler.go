package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

// ContextHandler handles the /api/context endpoints.
type ContextHandler struct {
	gtd           gtdStore
	sess          sessionStore
	snapshotStore snapshot.StoreIface // optional; nil = feature disabled
}

// NewContextHandler creates a ContextHandler.
func NewContextHandler(g gtdStore, s sessionStore) *ContextHandler {
	return &ContextHandler{gtd: g, sess: s}
}

// WithSnapshotStore wires an optional snapshot store for latest-status enrichment.
func (h *ContextHandler) WithSnapshotStore(store snapshot.StoreIface) *ContextHandler {
	h.snapshotStore = store
	return h
}

type weeklyProgressResponse struct {
	Completed int64 `json:"completed"`
	Total     int64 `json:"total"`
}

// latestStatusSnapshotResponse is the summary embedded in get_today_context.
// Full text is available via the generate_project_status MCP tool.
type latestStatusSnapshotResponse struct {
	GeneratedAt    string `json:"generated_at"`
	SprintSummary  string `json:"sprint_summary"`
	SotaCatchupPct int    `json:"sota_catchup_pct"`
}

// pendingHandoffHTTPView is the JSON-friendly view of a session handoff returned
// in GET /api/context/today. next_actions is decoded from raw bytes to a typed
// slice so callers see a JSON array, not a base64-encoded string.
type pendingHandoffHTTPView struct {
	ID             uuid.UUID            `json:"id"`
	ProjectID      pgtype.UUID          `json:"project_id"`
	RepoName       pgtype.Text          `json:"repo_name"`
	Intent         string               `json:"intent"`
	ContextSummary pgtype.Text          `json:"context_summary"`
	ResolvedAt     pgtype.Timestamptz   `json:"resolved_at"`
	CreatedAt      pgtype.Timestamptz   `json:"created_at"`
	WorkspaceID    pgtype.UUID          `json:"workspace_id"`
	NextActions    []session.NextAction `json:"next_actions"`
}

func buildPendingHandoffHTTPView(h *db.SessionHandoff) *pendingHandoffHTTPView {
	if h == nil {
		return nil
	}
	v := &pendingHandoffHTTPView{
		ID:             h.ID,
		ProjectID:      h.ProjectID,
		RepoName:       h.RepoName,
		Intent:         h.Intent,
		ContextSummary: h.ContextSummary,
		ResolvedAt:     h.ResolvedAt,
		CreatedAt:      h.CreatedAt,
		WorkspaceID:    h.WorkspaceID,
		NextActions:    []session.NextAction{},
	}
	if len(h.NextActions) > 0 && string(h.NextActions) != "[]" {
		var actions []session.NextAction
		if err := json.Unmarshal(h.NextActions, &actions); err != nil {
			slog.Warn("buildPendingHandoffHTTPView: corrupt next_actions column", "handoff_id", h.ID, "err", err)
		} else {
			v.NextActions = actions
		}
	}
	return v
}

type todayContextResponse struct {
	Goals                []db.Goal                     `json:"goals"`
	Projects             []db.Project                  `json:"projects"`
	WeeklyProgress       weeklyProgressResponse        `json:"weekly_progress"`
	PendingHandoff       *pendingHandoffHTTPView       `json:"pending_handoff"`
	LatestStatusSnapshot *latestStatusSnapshotResponse `json:"latest_status_snapshot,omitempty"`
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

	// Best-effort: fetch the latest status snapshot (age < 24 h) for the
	// primary project. Failures are logged at warn level and skipped so the
	// response is never blocked by snapshot unavailability.
	var latestSnap *latestStatusSnapshotResponse
	if h.snapshotStore != nil {
		if snap, serr := h.snapshotStore.LatestFresh(ctx, "wayneblacktea", 24*time.Hour); serr == nil {
			latestSnap = &latestStatusSnapshotResponse{
				GeneratedAt:    snap.GeneratedAt.UTC().Format(time.RFC3339),
				SprintSummary:  snap.SprintSummary,
				SotaCatchupPct: snap.SotaCatchupPct,
			}
		} else if !snapshot.IsNotFound(serr) {
			slog.Warn("GetTodayContext: loading latest snapshot", "err", serr)
		}
	}

	return c.JSON(http.StatusOK, todayContextResponse{
		Goals:    goals,
		Projects: projects,
		WeeklyProgress: weeklyProgressResponse{
			Completed: completed,
			Total:     total,
		},
		PendingHandoff:       buildPendingHandoffHTTPView(handoff),
		LatestStatusSnapshot: latestSnap,
	})
}
