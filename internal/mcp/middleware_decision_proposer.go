package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// decisionProposerWindow is the look-back window for "did the user already
// log a decision recently?" If a log_decision/confirm_plan event was
// recorded in the same session within this window, the proposer skips —
// avoiding spammy proposals when the user is actively recording decisions.
const decisionProposerWindow = 15 * time.Minute

// decisionProposerTimeout caps the entire background goroutine (LLM draft
// + proposal insert). The middleware itself returns to the caller before
// the goroutine even starts, so this only bounds resource use.
const decisionProposerTimeout = 30 * time.Second

// disableAutoDecisionsEnvVar is the opt-OUT switch for the auto-decision
// proposer. Default behaviour is ON — wayneblacktea's product purpose is
// to auto-track decisions; spam is mitigated by routing through the
// pending_proposals queue (user confirms each one).
//
// Set WBT_DISABLE_AUTO_DECISIONS=1 (or "true") to disable. Useful if the
// LLM budget is tight or proposals are noisy for a particular workflow.
const disableAutoDecisionsEnvVar = "WBT_DISABLE_AUTO_DECISIONS"

// decisionProposerEnabled returns true unless the operator has explicitly
// opted out via WBT_DISABLE_AUTO_DECISIONS=1. Any non-empty value other
// than "0"/"false"/"no" disables.
func decisionProposerEnabled() bool {
	raw := strings.TrimSpace(os.Getenv(disableAutoDecisionsEnvVar))
	if raw == "" {
		return true
	}
	switch strings.ToLower(raw) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

// shouldRunDecisionProposer returns true when the middleware MUST proceed
// past the early-exit guards (successful mutating tool, opt-out unset, deps
// wired). Extracted to keep decisionProposerMiddleware below the gocyclo
// threshold of 15.
func (s *Server) shouldRunDecisionProposer(res *mcpmsg.CallToolResult, err error, tool string) bool {
	if err != nil || res == nil || res.IsError {
		return false
	}
	if !discipline.IsMutating(tool) {
		return false
	}
	// Skip the very tools we're trying to nudge — emitting a decision
	// proposal in response to a log_decision is silly recursion.
	if tool == "log_decision" || tool == "confirm_plan" || tool == "confirm_proposal" {
		return false
	}
	if !decisionProposerEnabled() {
		return false
	}
	return s.discipline != nil && s.proposal != nil && s.drafter != nil
}

// decisionProposerPayload is the JSON shape persisted into pending_proposals
// (type='decision'). confirm_proposal materialises this into the decisions
// table when the user accepts.
type decisionProposerPayload struct {
	Title        string   `json:"title"`
	Decision     string   `json:"decision"`
	Rationale    string   `json:"rationale"`
	Alternatives []string `json:"alternatives,omitempty"`
	SessionID    string   `json:"session_id"`
	TriggerTool  string   `json:"trigger_tool"`
}

// runDecisionProposer is the background-goroutine body extracted from
// decisionProposerMiddleware. It runs under context.Background() with a
// fresh timeout — never inheriting the request ctx — so the proposal
// survives request cancellation.
func (s *Server) runDecisionProposer(tool, argSummary, resultSummary, sessionID string, workspaceID *uuid.UUID) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("decisionProposerMiddleware: panic in background goroutine",
				"tool", tool,
				"panic", fmt.Sprintf("%v", r),
			)
		}
	}()
	bgCtx, cancel := context.WithTimeout(context.Background(), decisionProposerTimeout)
	defer cancel()

	// 1. Skip if user already logged a decision in the window.
	since := time.Now().Add(-decisionProposerWindow)
	times, dErr := s.discipline.RecentDecisionTimes(bgCtx, sessionID, since)
	if dErr != nil {
		slog.Warn("decisionProposerMiddleware: RecentDecisionTimes failed",
			"err", dErr, "session", sessionID, "tool", tool)
		return
	}
	if len(times) > 0 {
		return
	}

	// 2. Draft the proposal.
	draft, draftErr := s.drafter.Draft(bgCtx, ai.DecisionDraftInput{
		TriggerTool:   tool,
		ArgsSummary:   argSummary,
		ResultSummary: resultSummary,
	})
	if draftErr != nil {
		slog.Warn("decisionProposerMiddleware: drafter failed",
			"err", draftErr, "tool", tool)
		return
	}
	if draft == nil || strings.TrimSpace(draft.Title) == "" {
		// Drafter intentionally declined (routine activity).
		return
	}

	// 3. Persist as a pending_proposals row.
	payload := decisionProposerPayload{
		Title:        draft.Title,
		Decision:     draft.Decision,
		Rationale:    draft.Rationale,
		Alternatives: draft.Alternatives,
		SessionID:    sessionID,
		TriggerTool:  tool,
	}
	body, mErr := json.Marshal(payload)
	if mErr != nil {
		slog.Warn("decisionProposerMiddleware: marshal payload failed",
			"err", mErr, "tool", tool)
		return
	}
	if _, cErr := s.proposal.Create(bgCtx, proposal.CreateParams{
		WorkspaceID: workspaceID,
		Type:        proposal.TypeDecision,
		Payload:     body,
		ProposedBy:  "wayneblacktea-auto-decision",
	}); cErr != nil {
		slog.Warn("decisionProposerMiddleware: insert pending_proposal failed",
			"err", cErr, "tool", tool, "session", sessionID)
		return
	}
	slog.Info("decisionProposerMiddleware: proposal inserted",
		"tool", tool,
		"session", sessionID,
		"title_len", len(draft.Title),
	)
}

// decisionProposerMiddleware fires AFTER any mutating MCP tool call when no
// log_decision / confirm_plan happened in the last 15 minutes. It kicks off
// a background goroutine that drafts a decision via the LLM chain (Haiku)
// and writes a pending_proposals row (type='decision'). The user confirms
// or rejects via the existing /api/proposals flow.
//
// SAFETY:
//   - context.Background() with a 30 s timeout so the request ctx ending
//     does not abort the draft / insert.
//   - Errors are slog.Warn only — never fail the user-facing tool call.
//   - When discipline / drafter / proposal store is nil, the middleware is
//     a no-op (preserves SQLite mode + missing-LLM environments).
//   - Default ON; opt-out via WBT_DISABLE_AUTO_DECISIONS=1.
func (s *Server) decisionProposerMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
			res, err := next(ctx, req)
			tool := req.Params.Name
			if !s.shouldRunDecisionProposer(res, err, tool) {
				return res, err
			}

			// Snapshot tool args + result here on the request goroutine
			// before the request context can be cancelled. Pass copies into
			// the background goroutine so we don't race the request lifetime.
			args := req.GetArguments()
			argSummary := truncateRunes(fmt.Sprintf("%v", args), mcpArgSummaryMaxRunes)
			resultSummary := extractResultText(res, mcpResultSummaryMaxRunes)
			sessionID := s.sessionID
			workspaceID := s.workspaceID

			//nolint:gosec // G118: intentional — goroutine MUST outlive request ctx so the proposal survives ctx cancellation
			go s.runDecisionProposer(tool, argSummary, resultSummary, sessionID, workspaceID)

			return res, err
		}
	}
}
