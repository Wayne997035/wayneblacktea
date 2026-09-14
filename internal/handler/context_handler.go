package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/safetext"
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
	// [GTD 49f2ed81] Every string below is agent-authored free text that came
	// back out of session_handoffs, and this response is read by agents as
	// well as by the dashboard (the CLI prints it; people paste it into a
	// conversation). Without neutralisation a stored payload can forge a
	// closing boundary marker and place instructions "outside" the fence that
	// a reader downstream puts around this span.
	//
	// The field set mirrors the MCP twin deliberately — internal/mcp/tools_
	// context.go's disposition comment names repo_name and next_actions.title/
	// command/expected as the unfenced-but-neutralised set, and the handoff
	// resource fences intent/context_summary. Leaving the HTTP view raw is how
	// the two sides drift apart, which is the shape of this finding.
	v := &pendingHandoffHTTPView{
		ID:             h.ID,
		ProjectID:      h.ProjectID,
		RepoName:       neutralizeText(h.RepoName),
		Intent:         safetext.NeutralizeBoundaryMarkers(h.Intent),
		ContextSummary: neutralizeText(h.ContextSummary),
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
			for i := range actions {
				actions[i].Title = safetext.NeutralizeBoundaryMarkers(actions[i].Title)
				actions[i].Command = safetext.NeutralizeBoundaryMarkers(actions[i].Command)
				actions[i].Expected = safetext.NeutralizeBoundaryMarkers(actions[i].Expected)
				if actions[i].RefTaskID != nil {
					ref := safetext.NeutralizeBoundaryMarkers(*actions[i].RefTaskID)
					actions[i].RefTaskID = &ref
				}
			}
			v.NextActions = actions
		}
	}
	return v
}

// neutralizeText applies safetext.NeutralizeBoundaryMarkers to a pgtype.Text
// without disturbing its NULL-ness: a NULL column must stay NULL in the JSON
// response, so the Valid flag decides whether there is anything to scan at
// all. Returning a zero-value pgtype.Text for NULL would silently turn null
// into "" for every consumer of this view.
func neutralizeText(t pgtype.Text) pgtype.Text {
	if !t.Valid {
		return t
	}
	t.String = safetext.NeutralizeBoundaryMarkers(t.String)
	return t
}

// dashboardHandoffFreshness bounds how old an unresolved session handoff may be
// before the dashboard "下次工作" card stops surfacing it. A handoff is a
// "resume where you left off" note; once it is days old and was never resolved
// it is stale context, not next work, and showing it misleads. The DB row is
// left untouched (MCP get_today_context / SessionStart still see it so it can be
// resolved) — only the human-facing dashboard hides it. 72h spans a typical
// long-weekend gap so a handoff you genuinely paused on survives, while one
// abandoned for weeks does not.
const dashboardHandoffFreshness = 72 * time.Hour

// freshDashboardHandoff returns h only when it is recent enough to show on the
// dashboard. A nil handoff, a handoff with no valid created_at, or one older
// than dashboardHandoffFreshness all yield nil so the card renders its empty
// state instead of stale context.
func freshDashboardHandoff(h *db.SessionHandoff, now time.Time) *db.SessionHandoff {
	if h == nil || !h.CreatedAt.Valid {
		return nil
	}
	if now.Sub(h.CreatedAt.Time) > dashboardHandoffFreshness {
		return nil
	}
	return h
}

type todayContextResponse struct {
	Goals                []db.Goal                     `json:"goals"`
	Projects             []db.Project                  `json:"projects"`
	WeeklyProgress       weeklyProgressResponse        `json:"weekly_progress"`
	PendingHandoff       *pendingHandoffHTTPView       `json:"pending_handoff"`
	LatestStatusSnapshot *latestStatusSnapshotResponse `json:"latest_status_snapshot,omitempty"`
	// PulledForward holds up to gtd.PullForwardCap important (importance=1)
	// tasks not yet due, auto-surfaced into today's context (2026-07-19 user
	// decision: automatic, data-only). Always a non-nil slice — empty array,
	// never null, so web/src/hooks/useContextToday.ts doesn't need a presence check.
	PulledForward []db.Task `json:"pulled_forward"`
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

	pulledForward, err := h.gtd.PullForwardTasks(ctx, time.Now())
	if err != nil {
		c.Logger().Errorf("GetTodayContext loading pull-forward tasks: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	if pulledForward == nil {
		pulledForward = []db.Task{}
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
		PendingHandoff:       buildPendingHandoffHTTPView(freshDashboardHandoff(handoff, time.Now())),
		LatestStatusSnapshot: latestSnap,
		PulledForward:        pulledForward,
	})
}
