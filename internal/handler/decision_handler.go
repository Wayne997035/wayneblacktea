package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/Wayne997035/wayneblacktea/internal/db"
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

// decisionsPage is the GET /api/decisions response envelope [F1003-11].
// Field names match the existing list_decisions MCP tool's convention
// (internal/mcp/tools_decision.go) for one naming scheme across both
// surfaces of the domain.
type decisionsPage struct {
	Decisions []db.Decision `json:"decisions"`
	Offset    int32         `json:"offset"`
	Limit     int32         `json:"limit"`
	HasMore   bool          `json:"has_more"`
}

func newDecisionsPage(decisions []db.Decision, offset, limit int32, hasMore bool) decisionsPage {
	if decisions == nil {
		decisions = []db.Decision{} // list endpoints MUST return [] not null (a nil slice marshals to JSON null and breaks frontend .length)
	}
	return decisionsPage{Decisions: decisions, Offset: offset, Limit: limit, HasMore: hasMore}
}

// trimHasMore implements the limit+1 has_more trick already used by
// internal/mcp/tools_decision.go's handleListDecisions: rows was fetched
// with limit+1 rows requested, so having more than limit rows means there's
// a next page.
func trimHasMore(rows []db.Decision, limit int32) ([]db.Decision, bool) {
	hasMore := int32(len(rows)) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return rows, hasMore
}

// listAllWithHasMore fetches the no-filter page via store.List (not All —
// see decisionStore's doc comment). It normally applies the limit+1
// has_more trick, but at limit == maxLimit it cannot: limit+1 would exceed
// decision.ListParams.Validate's own 100-row ceiling (internal/decision/
// decision.go — out of scope for this task, see spec Scope), so it fetches
// exactly limit rows and probes one extra row at offset+limit instead.
func (h *DecisionHandler) listAllWithHasMore(ctx context.Context, limit, offset, maxLimit int32) ([]db.Decision, bool, error) {
	if limit < maxLimit {
		rows, err := h.store.List(ctx, decision.ListParams{Limit: limit + 1, Offset: offset, IncludeAuto: true})
		if err != nil {
			return nil, false, err
		}
		decisions, hasMore := trimHasMore(rows, limit)
		return decisions, hasMore, nil
	}
	decisions, err := h.store.List(ctx, decision.ListParams{Limit: limit, Offset: offset, IncludeAuto: true})
	if err != nil {
		return nil, false, err
	}
	probe, err := h.store.List(ctx, decision.ListParams{Limit: 1, Offset: offset + limit, IncludeAuto: true})
	if err != nil {
		return nil, false, err
	}
	return decisions, len(probe) > 0, nil
}

// ListDecisions returns decisions, optionally filtered by repo_name or
// project_id query params. ?limit= controls page size (default 20, max
// 100). ?offset= pages past the first N rows (default 0; negative or
// unparseable -> 0, same invalid-input fallback convention as limit)
// [F1003-11]. The response is always the decisionsPage object shape, for
// all three filter branches.
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

	var offset int32
	if rawOffset := c.QueryParam("offset"); rawOffset != "" {
		if n, err := strconv.ParseInt(rawOffset, 10, 32); err == nil && n > 0 {
			offset = int32(n)
		}
	}

	ctx := c.Request().Context()

	if projectIDStr := c.QueryParam("project_id"); projectIDStr != "" {
		id, err := uuid.Parse(projectIDStr)
		if err != nil {
			return c.JSON(http.StatusBadRequest, errResp("invalid project_id"))
		}
		rows, err := h.store.ByProject(ctx, id, limit+1, offset)
		if err != nil {
			c.Logger().Errorf("ListDecisions ByProject: %v", err)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
		decisions, hasMore := trimHasMore(rows, limit)
		return c.JSON(http.StatusOK, newDecisionsPage(decisions, offset, limit, hasMore))
	}

	if repoName := c.QueryParam("repo_name"); repoName != "" {
		rows, err := h.store.ByRepo(ctx, repoName, limit+1, offset)
		if err != nil {
			c.Logger().Errorf("ListDecisions ByRepo: %v", err)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
		decisions, hasMore := trimHasMore(rows, limit)
		return c.JSON(http.StatusOK, newDecisionsPage(decisions, offset, limit, hasMore))
	}

	// [F1003-11] No-filter branch routes through List (not All) so it gets
	// offset/has_more paging; IncludeAuto: true preserves All's historical
	// no-source-filter behaviour — omitting it would silently drop
	// auto-sourced decisions from the dashboard's no-filter view (see
	// decision.Store.List's IncludeAuto doc comment).
	decisions, hasMore, err := h.listAllWithHasMore(ctx, limit, offset, maxLimit)
	if err != nil {
		c.Logger().Errorf("ListDecisions List: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, newDecisionsPage(decisions, offset, limit, hasMore))
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

	// Noise/injection check — same validator.CheckDecisionNoise the MCP
	// log_decision tool runs (internal/mcp/tools_decision.go:64), so a
	// <script> tag or markdown fence in any of the four text fields is
	// rejected regardless of which entry point the caller used.
	if reason := validator.CheckDecisionNoise(req.Title, req.Context, req.Decision, req.Rationale); reason != "" {
		return c.JSON(http.StatusBadRequest, errResp("invalid params: "+reason))
	}
	// [F0925-29] Workspace repo name rule; empty stays allowed.
	if !validator.IsValidRepoName(req.RepoName) {
		return c.JSON(http.StatusBadRequest, errResp(validator.RepoNameMessage))
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
		Source:       decision.SourceManual,
	})
	if err != nil {
		if errors.Is(err, validator.ErrInvalidRepoName) {
			return c.JSON(http.StatusBadRequest, errResp(validator.RepoNameMessage))
		}
		c.Logger().Errorf("LogDecision: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusCreated, d)
}
