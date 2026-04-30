package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

const autoHandoffPrefix = "Auto-handoff:"

const autologBodyLimit = 64 * 1024 // 64 KB

// maxTranscriptMessages caps the number of messages accepted from callers.
const maxTranscriptMessages = 100

// transcriptSummarizer is the narrow interface AutologHandler needs from the AI package.
// *ai.Summarizer satisfies this interface.
type transcriptSummarizer interface {
	Summarize(ctx context.Context, transcript []ai.Message) ai.SummaryResult
}

// AutologHandler handles the /api/activity and /api/auto-handoff endpoints.
type AutologHandler struct {
	gtd        autologGTDStore
	sess       autologSessionStore
	decision   autologDecisionStore
	summarizer transcriptSummarizer
}

// NewAutologHandler creates an AutologHandler.
// sum may be nil — when nil, AI enrichment is disabled and the handler falls back
// to the mechanical "Auto-handoff: in_progress=[...]" summary.
func NewAutologHandler(g autologGTDStore, s autologSessionStore, d autologDecisionStore, sum *ai.Summarizer) *AutologHandler {
	var ts transcriptSummarizer
	if sum != nil {
		ts = sum
	}
	return &AutologHandler{gtd: g, sess: s, decision: d, summarizer: ts}
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

// autoHandoffRequest is the optional request body for POST /api/auto-handoff.
type autoHandoffRequest struct {
	Transcript []ai.Message `json:"transcript,omitempty"`
}

// AutoHandoff handles POST /api/auto-handoff.
// It reads in-progress tasks and recent decisions, then creates a session handoff row.
// When a transcript is provided and a summarizer is configured, the handoff includes
// an AI-generated summary and any implicitly decided architectural decisions.
func (h *AutologHandler) AutoHandoff(c echo.Context) error {
	ctx := c.Request().Context()

	// Decode optional body. Failures are silently ignored — empty body = mechanical fallback.
	var req autoHandoffRequest
	_ = json.NewDecoder(io.LimitReader(c.Request().Body, autologBodyLimit)).Decode(&req)
	if len(req.Transcript) > maxTranscriptMessages {
		req.Transcript = req.Transcript[len(req.Transcript)-maxTranscriptMessages:]
	}

	mechanicalIntent, err := h.buildMechanicalIntent(ctx)
	if err != nil {
		c.Logger().Errorf("AutoHandoff: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	contextSummary := h.enrichSummary(ctx, c, mechanicalIntent, req.Transcript)

	// Resolve any existing auto-generated unresolved handoff so there is at
	// most one at a time (Stop hook can fire multiple times per session).
	if existing, latestErr := h.sess.LatestHandoff(ctx); latestErr == nil &&
		existing != nil && strings.HasPrefix(existing.Intent, autoHandoffPrefix) {
		_ = h.sess.Resolve(ctx, existing.ID)
	}

	handoff, err := h.sess.SetHandoff(ctx, session.HandoffParams{
		Intent:         mechanicalIntent,
		ContextSummary: contextSummary,
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

// buildMechanicalIntent collects in-progress tasks and recent decisions, then
// assembles the "Auto-handoff: in_progress=[...] recent_decisions=[...]" string.
func (h *AutologHandler) buildMechanicalIntent(ctx context.Context) (string, error) {
	tasks, err := h.gtd.Tasks(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("tasks: %w", err)
	}
	var inProgress []string
	for _, t := range tasks {
		if t.Status == "in_progress" {
			inProgress = append(inProgress, t.Title)
		}
	}

	decisions, err := h.decision.All(ctx, 5)
	if err != nil {
		return "", fmt.Errorf("decisions: %w", err)
	}
	var decTitles []string
	for _, d := range decisions {
		decTitles = append(decTitles, d.Title)
	}

	return fmt.Sprintf("%s in_progress=[%s] recent_decisions=[%s]",
		autoHandoffPrefix,
		strings.Join(inProgress, ", "),
		strings.Join(decTitles, ", "),
	), nil
}

// enrichSummary attempts AI enrichment when a summarizer and transcript are available.
// Falls back to mechanical intent on any error or empty result.
func (h *AutologHandler) enrichSummary(ctx context.Context, c echo.Context, mechanical string, transcript []ai.Message) string {
	if h.summarizer == nil || len(transcript) == 0 {
		return mechanical
	}
	result := h.summarizer.Summarize(ctx, transcript)
	for _, d := range result.Decisions {
		if logErr := h.logImplicitDecision(ctx, d); logErr != nil {
			c.Logger().Warnf("AutoHandoff: failed to log implicit decision %q: %v", d, logErr)
		}
	}
	if result.Summary != "" {
		return result.Summary
	}
	return mechanical
}

// logImplicitDecision persists a single implicit decision extracted from the transcript.
// Errors are returned wrapped so the caller can log them, but the handoff is never aborted.
func (h *AutologHandler) logImplicitDecision(ctx context.Context, title string) error {
	_, err := h.decision.Log(ctx, decision.LogParams{
		Title:     title,
		Context:   "auto-extracted from session transcript",
		Decision:  title,
		Rationale: "implicitly decided during session",
	})
	if err != nil {
		return fmt.Errorf("log implicit decision: %w", err)
	}
	return nil
}
