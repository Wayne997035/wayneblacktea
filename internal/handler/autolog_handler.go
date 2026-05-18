package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/redact"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

const autoHandoffPrefix = "Auto-handoff:"

const autologBodyLimit = 64 * 1024 // 64 KB

const (
	maxActorLen                 = 200
	maxActionLen                = 200
	maxNotesLen                 = 2000
	maxTaskTitle                = 500
	maxAutoHandoffMessageBytes  = 8 * 1024
	untrustedTranscriptBeginFmt = "[BEGIN UNTRUSTED message %d]\n"
	untrustedTranscriptEnd      = "\n[END UNTRUSTED]"
)

// classifySem caps concurrent classifier goroutines to prevent goroutine accumulation
// when /api/activity receives bursts of requests.
var classifySem = make(chan struct{}, 20)

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
	classifier activityClassifier
	// proposalStore is the optional gate that converts IsTask=true classifier
	// verdicts into TypeTask proposals (user-reviewed) instead of direct task
	// rows. nil = legacy direct-create path (kept for the few call sites that
	// do not yet wire the proposal store, e.g. unit tests that don't exercise
	// auto-capture). When non-nil the autoCreateTask routes via Create.
	proposalStore autologProposalStore
}

// NewAutologHandler creates an AutologHandler.
// sum may be nil — when nil, AI enrichment is disabled and the handler falls back
// to the mechanical "Auto-handoff: in_progress=[...]" summary.
// classifier is wired when CLAUDE_API_KEY is set; nil disables auto-decision capture.
func NewAutologHandler(
	g autologGTDStore,
	s autologSessionStore,
	d autologDecisionStore,
	sum *ai.Summarizer,
) *AutologHandler {
	var ts transcriptSummarizer
	if sum != nil {
		ts = sum
	}
	return &AutologHandler{gtd: g, sess: s, decision: d, summarizer: ts}
}

// NewAutologHandlerWithClassifier creates an AutologHandler with both summarizer and classifier.
// Used by main.go when CLAUDE_API_KEY is configured; clf may be nil to disable auto-capture.
func NewAutologHandlerWithClassifier(
	g autologGTDStore,
	s autologSessionStore,
	d autologDecisionStore,
	sum *ai.Summarizer,
	clf *ai.ActivityClassifier,
) *AutologHandler {
	h := NewAutologHandler(g, s, d, sum)
	if clf != nil {
		h.classifier = clf
	}
	return h
}

// NewAutologHandlerWithClassifierAndProposal extends NewAutologHandlerWithClassifier
// by also wiring the proposal store. When proposalStore is non-nil, IsTask=true
// classifier verdicts go through the TypeTask proposal queue for user review
// (TASK 2 of feature/gtd-enforce-server-side); when nil the legacy direct-task
// path is preserved so existing callers / tests are unaffected.
func NewAutologHandlerWithClassifierAndProposal(
	g autologGTDStore,
	s autologSessionStore,
	d autologDecisionStore,
	sum *ai.Summarizer,
	clf *ai.ActivityClassifier,
	proposalStore autologProposalStore,
) *AutologHandler {
	h := NewAutologHandlerWithClassifier(g, s, d, sum, clf)
	if proposalStore != nil {
		h.proposalStore = proposalStore
	}
	return h
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
	if runes := []rune(req.Actor); len(runes) > maxActorLen {
		req.Actor = string(runes[:maxActorLen])
	}
	if runes := []rune(req.Action); len(runes) > maxActionLen {
		req.Action = string(runes[:maxActionLen])
	}
	if runes := []rune(req.Notes); len(runes) > maxNotesLen {
		req.Notes = string(runes[:maxNotesLen])
	}
	req.Actor = sanitize.Notes(req.Actor)
	req.Action = sanitize.Notes(req.Action)
	req.Notes = sanitize.Notes(req.Notes)
	if req.Actor == "" || req.Action == "" {
		return c.JSON(http.StatusBadRequest, errResp("actor and action are required"))
	}

	if err := h.gtd.LogActivity(c.Request().Context(), req.Actor, req.Action, req.ProjectID, req.Notes); err != nil {
		c.Logger().Errorf("LogActivity: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	if h.classifier != nil {
		h.maybeClassifyAsync(req.Actor, req.Action, req.Notes)
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// maybeClassifyAsync tries to acquire classifySem and, if successful, spawns a goroutine
// that classifies the activity and auto-captures any implied decision or task.
// When the semaphore is full it logs a warning and returns immediately without blocking.
func (h *AutologHandler) maybeClassifyAsync(actor, action, notes string) {
	select {
	case classifySem <- struct{}{}:
		go func() {
			defer func() { <-classifySem }()
			result := h.classifier.Classify(context.Background(), actor, action, notes)
			if result.IsDecision && result.Title != "" {
				bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if err := h.logImplicitDecision(bgCtx, result.Title); err != nil {
					slog.Warn("auto-decision classify: log failed", "err", err)
				}
			}
			if result.IsTask && result.TaskTitle != "" {
				bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if err := h.autoCreateTaskFromClassifier(
					bgCtx, result.TaskTitle, actor, action, notes, result.Rationale,
				); err != nil {
					slog.Warn("auto-task classify: enqueue/create failed", "err", err)
				}
			}
		}()
	default:
		slog.Warn("auto-classify: goroutine cap reached, skipping", "actor", actor)
	}
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
	if err := prepareAutoHandoffTranscript(req.Transcript); err != nil {
		return c.JSON(http.StatusBadRequest, errResp(err.Error()))
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

func prepareAutoHandoffTranscript(transcript []ai.Message) error {
	for i := range transcript {
		msg := &transcript[i]
		switch msg.Role {
		case "user", "assistant":
		default:
			return fmt.Errorf("invalid transcript role")
		}
		if len([]byte(msg.Content)) > maxAutoHandoffMessageBytes {
			return fmt.Errorf("transcript message too large")
		}
		msg.Content = fmt.Sprintf(untrustedTranscriptBeginFmt, i+1) + msg.Content + untrustedTranscriptEnd
	}
	return nil
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
	for _, t := range result.Tasks {
		if createErr := h.autoCreateTask(ctx, t); createErr != nil {
			c.Logger().Warnf("AutoHandoff: failed to auto-create task %q: %v", t, createErr)
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
	const maxDecisionTitle = 500
	if len(title) > maxDecisionTitle {
		runes := []rune(title)
		if len(runes) > maxDecisionTitle {
			title = string(runes[:maxDecisionTitle])
		}
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}

	// Dedup: skip if same title was logged recently (check last 10 decisions).
	if recent, err := h.decision.All(ctx, int32(10)); err == nil {
		for _, d := range recent {
			if strings.EqualFold(d.Title, title) {
				return nil
			}
		}
	}

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

// autoCreateTask creates a GTD task for the given title if no active task with
// the same title exists. Dedup is scoped to pending/in_progress tasks only;
// completed tasks with the same title may be re-created.
//
// This path is the legacy direct-create flow still used by the transcript
// summarizer in AutoHandoff (where the summary is itself user-attested).
// The IsTask=true classifier verdict path (maybeClassifyAsync) routes through
// autoCreateTaskFromClassifier instead so it goes via the proposal queue when
// proposalStore is wired.
func (h *AutologHandler) autoCreateTask(ctx context.Context, title string) error {
	if len(title) > maxTaskTitle {
		runes := []rune(title)
		if len(runes) > maxTaskTitle {
			title = string(runes[:maxTaskTitle])
		}
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}

	// Dedup: skip if an active (pending/in_progress) task with the same title exists.
	tasks, err := h.gtd.Tasks(ctx, nil)
	if err != nil {
		return fmt.Errorf("auto-create task: list tasks: %w", err)
	}
	for _, t := range tasks {
		if (t.Status == "pending" || t.Status == "in_progress") && strings.EqualFold(t.Title, title) {
			return nil
		}
	}

	_, err = h.gtd.CreateTask(ctx, gtd.CreateTaskParams{
		Title:       title,
		Description: "auto-captured from activity log",
	})
	if err != nil {
		return fmt.Errorf("auto-create task: %w", err)
	}
	return nil
}

// autoCreateTaskFromClassifier is the IsTask=true classifier branch of the
// /api/activity auto-capture. When proposalStore is wired (production wiring),
// the verdict goes onto the TypeTask proposal queue so the user reviews and
// validator.CheckVagueness runs at confirm time. When proposalStore is nil
// (legacy callers and unit tests that don't exercise auto-capture), it falls
// back to direct task creation — preserves prior behaviour.
//
// argSummary/resultSummary equivalents here are the actor/action/notes triple
// already control-char sanitised by LogActivity via sanitize.Notes. Because
// the proposal payload is persisted long-lived (pending_proposals.payload row
// surfaced in the proposal UI), we additionally run redact.ForLLM on the
// summary + rationale to scrub credential-shape patterns (token=ghp_…,
// password=…, AKIA…, postgres://user:pass@…) before write — defence-in-depth
// per security review M-1 (PR #120).
func (h *AutologHandler) autoCreateTaskFromClassifier(
	ctx context.Context,
	title, actor, action, notes, classifierRationale string,
) error {
	if h.proposalStore == nil {
		// Legacy direct path — unit tests rely on this when they don't
		// inject a proposalStore. Production wiring always sets it.
		return h.autoCreateTask(ctx, title)
	}

	if len(title) > maxTaskTitle {
		runes := []rune(title)
		if len(runes) > maxTaskTitle {
			title = string(runes[:maxTaskTitle])
		}
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}

	// Dedup against EXISTING pending TypeTask proposals (not the tasks table).
	pending, err := h.proposalStore.ListPending(ctx)
	if err != nil {
		return fmt.Errorf("auto-create task: list pending proposals: %w", err)
	}
	for _, p := range pending {
		if p.Type != string(proposal.TypeTask) {
			continue
		}
		var existing proposal.TaskPayload
		if uerr := json.Unmarshal(p.Payload, &existing); uerr != nil {
			continue
		}
		if strings.EqualFold(existing.Title, title) {
			return nil
		}
	}

	payload := proposal.TaskPayload{
		Title:               title,
		SourceTool:          "activity:" + actor + ":" + action,
		ArgSummary:          redact.ForLLM(notes),
		ClassifierRationale: redact.ForLLM(classifierRationale),
		SuggestedKind:       "general",
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("auto-create task: marshal payload: %w", err)
	}
	if len(encoded) > proposal.MaxTaskPayloadBytes {
		slog.Warn("autoCreateTaskFromClassifier: payload exceeds cap, skipping",
			"actor", actor, "bytes", len(encoded), "cap", proposal.MaxTaskPayloadBytes)
		return nil
	}

	row, err := h.proposalStore.Create(ctx, proposal.CreateParams{
		Type:       proposal.TypeTask,
		Payload:    encoded,
		ProposedBy: "claude-code",
	})
	if err != nil {
		return fmt.Errorf("auto-create task: create proposal: %w", err)
	}
	slog.Info("autoCreateTaskFromClassifier: enqueued task proposal",
		"proposal_id", row.ID.String(),
		"actor", actor,
	)
	return nil
}
