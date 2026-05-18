package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
)

// mcpClassifySem caps concurrent classifier goroutines spawned by the MCP
// middleware to prevent goroutine accumulation during tool call bursts.
var mcpClassifySem = make(chan struct{}, 20)

// significantTools is the set of MCP tool names whose invocations are worth
// classifying for implicit decisions and follow-up tasks.
var significantTools = map[string]bool{
	"complete_task":         true,
	"confirm_proposal":      true,
	"upsert_project_arch":   true,
	"update_project_status": true,
	"resolve_handoff":       true,
	"sync_repo":             true,
}

const (
	mcpArgSummaryMaxRunes    = 500
	mcpResultSummaryMaxRunes = 300
	mcpClassifyTimeout       = 15 * time.Second
	mcpDecisionMaxTitle      = 500
	mcpTaskMaxTitle          = 500

	// mcpClassifyMaxPerWindow caps Haiku classify calls per rolling window to
	// prevent API budget drain from a looping agent or prompt-injected client
	// (LLM04 model-DoS mitigation).
	mcpClassifyMaxPerWindow = 60
	mcpClassifyWindow       = time.Minute
)

// mcpClassifyBudget is a simple token-bucket rate limiter that refills the
// full quota at the start of each window. Sufficient to defend against
// runaway tool loops without the complexity of golang.org/x/time/rate.
var mcpClassifyBudget = struct {
	mu      sync.Mutex
	tokens  int
	resetAt time.Time
}{tokens: mcpClassifyMaxPerWindow}

// tryAcquireClassifyToken returns true if budget remains in the current
// window. It refills the bucket when the window has elapsed. Concurrency-safe.
func tryAcquireClassifyToken(now time.Time) bool {
	mcpClassifyBudget.mu.Lock()
	defer mcpClassifyBudget.mu.Unlock()
	if now.After(mcpClassifyBudget.resetAt) {
		mcpClassifyBudget.tokens = mcpClassifyMaxPerWindow
		mcpClassifyBudget.resetAt = now.Add(mcpClassifyWindow)
	}
	if mcpClassifyBudget.tokens <= 0 {
		return false
	}
	mcpClassifyBudget.tokens--
	return true
}

// maybeClassifyToolCall tries to acquire mcpClassifySem and, if successful,
// spawns a goroutine that classifies the tool call and auto-captures any
// implied decision or task. When the semaphore is full it logs a warning and
// returns immediately without blocking the tool response.
//
// All DB writes use context.Background() so they survive request-context
// cancellation — the tool response is already delivered by the time the
// goroutine commits to the DB.
func (s *Server) maybeClassifyToolCall(toolName, argSummary, resultSummary string) {
	if s.classifier == nil || !significantTools[toolName] {
		return
	}

	// Rate-limit BEFORE the concurrency semaphore. Defends against API budget
	// drain from a looping agent (LLM04). The drop is silent on the response
	// path; we only log a warning so noisy callers leave a trail.
	if !tryAcquireClassifyToken(time.Now()) {
		slog.Warn("maybeClassifyToolCall: rate limit reached, skipping", "tool", toolName)
		return
	}

	select {
	case mcpClassifySem <- struct{}{}:
		go func() {
			defer func() { <-mcpClassifySem }()
			defer func() {
				if r := recover(); r != nil {
					slog.Warn("maybeClassifyToolCall: panic in background goroutine",
						"tool", toolName,
						"panic", fmt.Sprintf("%v", r),
					)
				}
			}()

			actor := "mcp:" + toolName
			result := s.classifier.Classify(context.Background(), actor, argSummary, resultSummary)

			if result.IsDecision && result.Title != "" {
				bgCtx, cancel := context.WithTimeout(context.Background(), mcpClassifyTimeout)
				defer cancel()
				if err := s.logMCPDecision(bgCtx, result.Title, toolName); err != nil {
					slog.Warn("maybeClassifyToolCall: log decision failed",
						"tool", toolName,
						"err", err,
					)
				}
			}

			if result.IsTask && result.TaskTitle != "" {
				bgCtx, cancel := context.WithTimeout(context.Background(), mcpClassifyTimeout)
				defer cancel()
				if err := s.autoCaptureMCPTask(
					bgCtx,
					result.TaskTitle,
					toolName,
					argSummary,
					resultSummary,
					result.Rationale,
				); err != nil {
					slog.Warn("maybeClassifyToolCall: enqueue task proposal failed",
						"tool", toolName,
						"err", err,
					)
				}
			}
		}()
	default:
		slog.Warn("maybeClassifyToolCall: goroutine cap reached, skipping", "tool", toolName)
	}
}

// logMCPDecision persists an implicit decision extracted from a MCP tool call.
// Dedup: if a decision with the same title (case-insensitive) exists in the
// last 10, it is skipped to prevent flooding.
func (s *Server) logMCPDecision(ctx context.Context, title, toolName string) error {
	runes := []rune(title)
	if len(runes) > mcpDecisionMaxTitle {
		title = string(runes[:mcpDecisionMaxTitle])
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}

	if recent, err := s.decision.All(ctx, 10); err == nil {
		for _, d := range recent {
			if strings.EqualFold(d.Title, title) {
				return nil
			}
		}
	}

	_, err := s.decision.Log(ctx, decision.LogParams{
		Title:     title,
		Context:   "auto-extracted from MCP tool call: " + toolName,
		Decision:  title,
		Rationale: "implicitly decided via tool invocation",
	})
	if err != nil {
		return fmt.Errorf("log mcp decision: %w", err)
	}
	return nil
}

// autoCaptureMCPTask enqueues a TypeTask proposal (NOT a real task row) so the
// user can review the LLM classifier's IsTask=true verdict via the proposal UI
// before it materialises into a GTD task. Routing through proposal.Store.Create
// ensures the validator that handleAddTask runs is invoked at confirm time,
// closing the bypass identified by SA decision 42e0b783.
//
// Dedup: scoped to EXISTING pending TypeTask proposals (case-insensitive
// title), NOT to the tasks table — a closed task with the same title may have
// legitimately resurfaced. argSummary/resultSummary are already redacted by
// autoLogMiddleware (sanitize.Notes) before reaching this function; we do not
// re-redact here, only enforce length + payload-size caps.
func (s *Server) autoCaptureMCPTask(
	ctx context.Context,
	title, toolName, argSummary, resultSummary, classifierRationale string,
) error {
	if s.proposal == nil {
		return nil
	}

	runes := []rune(title)
	if len(runes) > mcpTaskMaxTitle {
		title = string(runes[:mcpTaskMaxTitle])
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}

	// Dedup against EXISTING pending TypeTask proposals — not against the
	// `tasks` table — so a closed task can legitimately recur as a proposal.
	pending, err := s.proposal.ListPending(ctx)
	if err != nil {
		return fmt.Errorf("auto-capture mcp task: list pending proposals: %w", err)
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
		SourceTool:          toolName,
		ArgSummary:          argSummary,
		ResultSummary:       resultSummary,
		ClassifierRationale: classifierRationale,
		SuggestedKind:       "general",
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("auto-capture mcp task: marshal payload: %w", err)
	}
	if len(encoded) > proposal.MaxTaskPayloadBytes {
		slog.Warn("autoCaptureMCPTask: payload exceeds cap, skipping",
			"tool", toolName, "bytes", len(encoded), "cap", proposal.MaxTaskPayloadBytes)
		return nil
	}

	row, err := s.proposal.Create(ctx, proposal.CreateParams{
		Type:       proposal.TypeTask,
		Payload:    encoded,
		ProposedBy: "claude-code",
	})
	if err != nil {
		return fmt.Errorf("auto-capture mcp task: create proposal: %w", err)
	}
	slog.Info("autoCaptureMCPTask: enqueued task proposal",
		"proposal_id", row.ID.String(),
		"tool", toolName,
	)
	return nil
}

// truncateRunes returns s capped at maxRunes using []rune conversion (UTF-8 safe).
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}
