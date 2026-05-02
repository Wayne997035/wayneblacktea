package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// workSessionStore covers the subset of worksession.StoreIface used by
// WorkSessionHandler.
type workSessionStore interface {
	// GetActive is called with the workspace UUID from the server's own env config.
	// The store already enforces workspace scoping internally; the UUID argument
	// is a belt-and-suspenders guard so the store can reject cross-workspace calls.
	GetActive(ctx context.Context, workspaceID uuid.UUID, repoName string) (*worksession.ActiveSessionResult, error)
}

// WorkSessionHandler handles the /api/work-sessions endpoints.
type WorkSessionHandler struct {
	store       workSessionStore
	workspaceID *uuid.UUID // server-level workspace UUID from env (never from request)
}

// NewWorkSessionHandler creates a WorkSessionHandler.
// workspaceID is the server-configured workspace UUID (from WORKSPACE_ID env).
// It is passed explicitly so tests can inject a known value without relying on
// environment variables.
func NewWorkSessionHandler(s workSessionStore, workspaceID *uuid.UUID) *WorkSessionHandler {
	return &WorkSessionHandler{store: s, workspaceID: workspaceID}
}

// GetActiveWorkSession handles GET /api/work-sessions/active?repo_name=X.
//
// workspace_id is taken from the server-level env configuration, never from
// the HTTP request (prevents IDOR cross-workspace leakage).
//
// Response:
//
//	200 {active: false}                         — no in_progress session
//	200 {active: true, session: ..., ...}        — active session found
//	400 {error: "repo_name query param required"} — missing param
//	500 {error: "internal server error"}          — unexpected store error
func (h *WorkSessionHandler) GetActiveWorkSession(c echo.Context) error {
	repoName := c.QueryParam("repo_name")
	if repoName == "" {
		return c.JSON(http.StatusBadRequest, errResp("repo_name query param is required"))
	}

	// workspace_id from env config, never from request input.
	if h.workspaceID == nil {
		// No workspace configured: return inactive to avoid false-positives.
		return c.JSON(http.StatusOK, &worksession.ActiveSessionResult{
			Active:                false,
			ImplementationAllowed: false,
		})
	}

	result, err := h.store.GetActive(c.Request().Context(), *h.workspaceID, repoName)
	if err != nil {
		if errors.Is(err, worksession.ErrNotFound) {
			return c.JSON(http.StatusOK, &worksession.ActiveSessionResult{
				Active:                false,
				ImplementationAllowed: false,
			})
		}
		c.Logger().Errorf("GetActiveWorkSession: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, result)
}
