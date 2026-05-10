package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/redact"
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
// Set WBT_DISABLE_AUTO_DECISIONS=1 (or "true"/"yes"/"on") to disable.
// Useful if the LLM budget is tight or proposals are noisy for a particular
// workflow.
const disableAutoDecisionsEnvVar = "WBT_DISABLE_AUTO_DECISIONS"

// mcpDecisionProposerSem caps concurrent decision-proposer goroutines. Distinct
// from mcpClassifySem so the two background paths share no budget — a runaway
// classifier shouldn't starve the proposer or vice versa.
var mcpDecisionProposerSem = make(chan struct{}, 20)

// mcpDecisionProposerMaxPerWindow caps Haiku draft calls per rolling window
// to defend against API budget drain from a looping agent or prompt-injected
// client (LLM04 model-DoS mitigation). Mirrors the classifier budget but is
// kept separate so the two paths fail independently.
const (
	mcpDecisionProposerMaxPerWindow = 60
	mcpDecisionProposerWindow       = time.Minute
)

// mcpDecisionProposerBudget is a simple token-bucket rate limiter that
// refills the full quota at the start of each window. Concurrency-safe.
var mcpDecisionProposerBudget = struct {
	mu      sync.Mutex
	tokens  int
	resetAt time.Time
}{tokens: mcpDecisionProposerMaxPerWindow}

// tryAcquireDecisionProposerToken returns true when budget remains in the
// current window; refills the bucket on rollover. Concurrency-safe.
func tryAcquireDecisionProposerToken(now time.Time) bool {
	mcpDecisionProposerBudget.mu.Lock()
	defer mcpDecisionProposerBudget.mu.Unlock()
	if now.After(mcpDecisionProposerBudget.resetAt) {
		mcpDecisionProposerBudget.tokens = mcpDecisionProposerMaxPerWindow
		mcpDecisionProposerBudget.resetAt = now.Add(mcpDecisionProposerWindow)
	}
	if mcpDecisionProposerBudget.tokens <= 0 {
		return false
	}
	mcpDecisionProposerBudget.tokens--
	return true
}

// decisionProposerEnabled returns true unless the operator has explicitly
// opted out via WBT_DISABLE_AUTO_DECISIONS set to a TRUTHY value. Default is
// ENABLED — empty string + any unrecognised value leave the proposer ON.
//
// Disabling values (case-insensitive): "1", "true", "yes", "on".
//
// History: an earlier inverted-default version disabled the proposer for any
// non-empty value not in {0,false,no,off}. That made unrelated env values
// like "auto" or "default" silently turn the feature off, which surprised
// operators. Current semantics are explicit opt-OUT only.
func decisionProposerEnabled() bool {
	raw := strings.TrimSpace(os.Getenv(disableAutoDecisionsEnvVar))
	if raw == "" {
		return true
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return false
	}
	return true
}

// logDecisionProposerStartupOnce is the sync.Once that gates the one-time
// startup log line announcing whether the proposer is enabled or disabled.
// We log on FIRST middleware invocation rather than at New() time because the
// middleware-construction site has no logger context.
var logDecisionProposerStartupOnce sync.Once

// logDecisionProposerStartup emits one slog.Info line documenting the
// observed env value + the resulting enabled state. Useful for operators
// debugging "is the auto-decision feature on right now?" without a code
// reference. Idempotent across the process lifetime.
func logDecisionProposerStartup() {
	logDecisionProposerStartupOnce.Do(func() {
		raw := strings.TrimSpace(os.Getenv(disableAutoDecisionsEnvVar))
		slog.Info("decisionProposer: startup state observed",
			"enabled", decisionProposerEnabled(),
			disableAutoDecisionsEnvVar, raw,
		)
	})
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

// decisionProposerPayload is a package-local alias for the shared payload
// shape. The canonical type lives in internal/proposal/payload.go so the
// confirm_proposal materialise switch can decode it without a circular
// import (mcp -> proposal is allowed; proposal -> mcp would not be). The
// alias is kept so existing callers don't need to learn the new path.
type decisionProposerPayload = proposal.DecisionProposerPayload

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
			logDecisionProposerStartup()
			res, err := next(ctx, req)
			tool := req.Params.Name
			if !s.shouldRunDecisionProposer(res, err, tool) {
				return res, err
			}

			// Rate-limit BEFORE the concurrency semaphore. Defends against
			// API budget drain from a looping agent (LLM04). Silent on the
			// response path; debug-level log so noisy callers leave a trail
			// but operators don't see warnings for ordinary throttling.
			if !tryAcquireDecisionProposerToken(time.Now()) {
				slog.Debug("decisionProposerMiddleware: rate limit reached, skipping",
					"tool", tool)
				return res, err
			}

			// Snapshot tool args + result here on the request goroutine
			// before the request context can be cancelled. Apply the redact
			// pass HERE (sync) so credential strings never reach the
			// background goroutine even if it races the LLM provider.
			args := req.GetArguments()
			argSummary := redact.ForLLM(truncateRunes(fmt.Sprintf("%v", args), mcpArgSummaryMaxRunes))
			resultSummary := redact.ForLLM(extractResultText(res, mcpResultSummaryMaxRunes))
			sessionID := s.sessionID
			workspaceID := s.workspaceID

			// Acquire a goroutine slot. Drop the proposal silently if all
			// slots are in flight — better than unbounded growth. The
			// goroutine MUST outlive the request ctx so the proposal
			// survives request-context cancellation; runDecisionProposer
			// builds its own context.Background-rooted ctx with a
			// dedicated timeout (see decisionProposerTimeout).
			select {
			case mcpDecisionProposerSem <- struct{}{}:
				go func() {
					defer func() { <-mcpDecisionProposerSem }()
					s.runDecisionProposer(tool, argSummary, resultSummary, sessionID, workspaceID)
				}()
			default:
				slog.Debug("decisionProposerMiddleware: goroutine cap reached, skipping",
					"tool", tool)
			}

			return res, err
		}
	}
}
