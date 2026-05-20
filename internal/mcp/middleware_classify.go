package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/redact"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
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
					result.Confidence,
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

// autoCaptureMCPTask routes an LLM classifier's IsTask=true verdict into the
// GTD pipeline. Two paths:
//
//  1. **Auto-accept (1-call quick-capture)**: when `confidence` ≥
//     ai.ClassifierAutoAcceptThreshold AND the synthesised description passes
//     validator.CheckVagueness with zero warnings, the task is materialised
//     directly via s.gtd.CreateTask (skipping the proposal review queue).
//     Requires s.gtd to be wired; otherwise falls through to the proposal path.
//
//  2. **Proposal queue (review-required)**: when confidence is below threshold
//     OR the synthesised description is vague, enqueue a TypeTask proposal so
//     the user reviews and validator.CheckVagueness re-runs at confirm time
//     (closing the bypass identified by SA decision 42e0b783).
//
// Dedup is applied to BOTH paths: scoped to EXISTING pending TypeTask
// proposals (case-insensitive title), NOT to the tasks table — a closed task
// with the same title may have legitimately resurfaced. argSummary/
// resultSummary are sanitised upstream by autoLogMiddleware (sanitize.Notes);
// here we additionally pass argSummary + classifierRationale through
// redact.ForLLM as a defence-in-depth pass before they land in either the
// tasks.description column (auto-accept) or pending_proposals.payload
// (proposal path) — matching the HTTP path in autoCreateTaskFromClassifier
// (internal/handler/autolog_handler.go) so both writers of these long-lived
// columns share the same posture (backend-security-design.md §3.1).
// redact.ForLLM is idempotent: re-applying it to an already-redacted string
// is a safe no-op (the kv-secret catch-all is guarded against re-matching its
// own placeholder).
func (s *Server) autoCaptureMCPTask(
	ctx context.Context,
	title, toolName, argSummary, resultSummary, classifierRationale string,
	confidence float64,
) error {
	if s.proposal == nil {
		return nil
	}

	title = truncateAndTrimTitleMCP(title, mcpTaskMaxTitle)
	if title == "" {
		return nil
	}

	dup, err := pendingTaskTitleExistsMCP(ctx, s.proposal.ListPending, title)
	if err != nil {
		return fmt.Errorf("auto-capture mcp task: %w", err)
	}
	if dup {
		return nil
	}

	redactedArg := redact.ForLLM(argSummary)
	redactedRationale := redact.ForLLM(classifierRationale)
	description := synthesiseClassifierDescription(redactedArg, redactedRationale)

	// Auto-accept gate: high confidence AND zero vagueness warnings AND a
	// gtd store is wired (production) → materialise directly.
	if shouldAutoAcceptMCP(confidence, description) && s.gtd != nil {
		task, terr := s.gtd.CreateTask(ctx, gtd.CreateTaskParams{
			Title:       title,
			Description: description,
			Kind:        validator.KindGeneral,
		})
		if terr != nil {
			return fmt.Errorf("auto-capture mcp task: direct create: %w", terr)
		}
		slog.Info("autoCaptureMCPTask: auto-accepted task (high confidence)",
			"task_id", task.ID.String(),
			"tool", toolName,
			"confidence", confidence,
		)
		return nil
	}

	payload := proposal.TaskPayload{
		Title:               title,
		SourceTool:          toolName,
		ArgSummary:          redactedArg,
		ResultSummary:       resultSummary,
		ClassifierRationale: redactedRationale,
		SuggestedKind:       validator.KindGeneral,
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
		"confidence", confidence,
	)
	return nil
}

// shouldAutoAcceptMCP mirrors handler.shouldAutoAccept — duplicated because
// the mcp package cannot import handler (handler already imports proposal +
// gtd which depend on lower layers). Field name "description" and KindGeneral
// MUST stay in lockstep with the HTTP twin's vagueness check.
func shouldAutoAcceptMCP(confidence float64, description string) bool {
	if confidence < ai.ClassifierAutoAcceptThreshold {
		return false
	}
	return len(validator.CheckVagueness("description", description, validator.KindGeneral)) == 0
}

// truncateAndTrimTitleMCP rune-caps title and trims whitespace. Mirrors the
// handler-side helper — see note on shouldAutoAcceptMCP for why we duplicate
// instead of importing.
func truncateAndTrimTitleMCP(title string, maxRunes int) string {
	runes := []rune(title)
	if len(runes) > maxRunes {
		title = string(runes[:maxRunes])
	}
	return strings.TrimSpace(title)
}

// pendingTaskTitleExistsMCP returns true when listFn yields a TypeTask
// proposal whose title equals `title` case-insensitively. Drained into a
// helper so the main flow stays under the gocyclo budget.
func pendingTaskTitleExistsMCP(
	ctx context.Context,
	listFn func(context.Context) ([]db.PendingProposal, error),
	title string,
) (bool, error) {
	pending, err := listFn(ctx)
	if err != nil {
		return false, fmt.Errorf("list pending proposals: %w", err)
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
			return true, nil
		}
	}
	return false, nil
}

// synthesiseClassifierDescription composes the task description that the
// auto-accept path persists into tasks.description AND that the vagueness
// gate is evaluated against. The same composition is used by the HTTP twin
// (internal/handler/autolog_handler.go) so the two writers always evaluate
// the SAME string against CheckVagueness — preventing a path where MCP
// auto-accepts but the equivalent HTTP request gets routed through the
// proposal queue (or vice versa) for identical inputs.
//
// Rationale text comes second because the classifier's "why" is usually more
// information-dense than the raw argument summary; this gives CheckVagueness
// the best chance to find a file:line ref or a concrete-enough phrase.
func synthesiseClassifierDescription(argSummary, classifierRationale string) string {
	arg := strings.TrimSpace(argSummary)
	rat := strings.TrimSpace(classifierRationale)
	switch {
	case arg == "" && rat == "":
		return ""
	case arg == "":
		return rat
	case rat == "":
		return arg
	default:
		return arg + " | " + rat
	}
}

// truncateRunes returns s capped at maxRunes using []rune conversion (UTF-8 safe).
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}
