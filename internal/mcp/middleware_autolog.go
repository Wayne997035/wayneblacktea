package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/redact"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// autoLogMiddleware wraps tool handlers: after high-signal tools succeed,
// it records an activity_log entry in a background goroutine.
// Goroutine uses context.Background() — never inherits the request context
// to prevent the DB write being cancelled when the request ends.
//
// For tools in significantTools, it also fires maybeClassifyToolCall so
// that implicit decisions and follow-up tasks are captured automatically.
func (s *Server) autoLogMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
			res, err := next(ctx, req)
			// Only fire for successful (non-error) results.
			if err != nil || res == nil || res.IsError {
				return res, err
			}

			tool := req.Params.Name
			args := req.GetArguments()

			// Auto-classify significant tools regardless of whether they
			// produce an activity log entry. maybeClassifyToolCall guards
			// with its own significantTools check, so this is always safe.
			//
			// SECURITY: scrub credential patterns BEFORE handing the strings
			// to maybeClassifyToolCall — the classifier ships them to an
			// upstream LLM provider (LLM02 prompt-data leakage). Redaction is
			// regex-based defence-in-depth; primary mitigation is structured
			// payload design upstream of this middleware.
			argSummary := redact.ForLLM(truncateRunes(marshalArgsDeterministic(args), mcpArgSummaryMaxRunes))
			resultSummary := redact.ForLLM(extractResultText(res, mcpResultSummaryMaxRunes))
			// Captured from the request ctx HERE, before maybeClassifyToolCall's
			// goroutine switches to context.Background() — a background
			// context carries no server.ClientSessionFromContext value, so
			// the actor identity must be resolved on the request goroutine
			// or it is lost (U15).
			actorSessionID := s.auditSessionID(ctx)
			s.maybeClassifyToolCall(tool, argSummary, resultSummary, actorSessionID)

			action, notes, ok := autoLogEntry(tool, args)
			if !ok {
				return res, err
			}

			// Launch in a background goroutine so the log write cannot block
			// or fail the tool response. Use context.Background() with a
			// timeout so the write survives request-context cancellation.
			//
			// A buffered semaphore (cap 50) caps concurrent goroutines to
			// prevent accumulation under sustained MCP burst traffic.
			// If autologSem is nil (e.g. in unit tests that construct Server
			// directly), fall back to the unbounded path so existing tests
			// are not broken by the addition of this cap.
			// launchAutolog starts fn in a background goroutine, guarded by
			// autologSem (cap 50). If the semaphore is nil (direct-struct
			// construction in unit tests), it falls back to an uncapped goroutine
			// so existing tests are not affected by the cap.
			launchAutolog := func(fn func()) {
				if s.autologSem == nil {
					go fn()
					return
				}
				select {
				case s.autologSem <- struct{}{}:
					go func() {
						defer func() { <-s.autologSem }()
						fn()
					}()
				default:
					slog.Warn("autoLogMiddleware: semaphore full, skipping autolog", "tool", tool)
				}
			}

			launchAutolog(func() {
				// Recover from any panic so a log failure never crashes the server.
				defer func() {
					if r := recover(); r != nil {
						slog.Warn(
							"autoLogMiddleware: panic in background goroutine",
							"tool", tool,
							"panic", fmt.Sprintf("%v", r),
						)
					}
				}()
				bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				var projectID *uuid.UUID
				if taskIDStr := stringArg(args, "task_id"); taskIDStr != "" {
					if taskID, parseErr := uuid.Parse(taskIDStr); parseErr == nil {
						if task, lookupErr := s.gtd.GetTaskByID(bgCtx, taskID); lookupErr == nil {
							if task.ProjectID.Valid {
								pid := uuid.UUID(task.ProjectID.Bytes)
								projectID = &pid
							}
						} else if lookupErr != gtd.ErrNotFound {
							slog.Warn(
								"autoLogMiddleware: task lookup failed",
								"tool", tool,
								"task_id", taskIDStr,
								"error", lookupErr,
							)
						}
					}
				}

				if logErr := s.gtd.LogActivity(bgCtx, "wayneblacktea-auto", action, projectID, notes); logErr != nil {
					slog.Warn(
						"autoLogMiddleware: failed to log activity",
						"tool", tool,
						"action", action,
						"error", logErr,
					)
				}
			})

			return res, err
		}
	}
}

// extractResultText returns the text content of the first text content block
// in the result, capped at maxRunes runes. Returns "" for nil or empty results.
func extractResultText(res *mcpmsg.CallToolResult, maxRunes int) string {
	if res == nil {
		return ""
	}
	for _, c := range res.Content {
		if tc, ok := c.(mcpmsg.TextContent); ok {
			return truncateRunes(tc.Text, maxRunes)
		}
	}
	return ""
}

const (
	maxNotesBytes   = 2000
	maxJSONArgBytes = 512 * 1024 // 512 KB cap before json.Unmarshal to prevent double-parse OOM
)

// autoLogEntry returns the action string, notes string, and true for the five
// high-signal tools that should produce an activity_log entry. It returns
// ("", "", false) for all other tools.
func autoLogEntry(tool string, args map[string]any) (action, notes string, ok bool) {
	switch tool {
	case "begin_task":
		taskID := stringArg(args, "task_id")
		return "task:begin", truncate(fmt.Sprintf("task_id=%s", taskID)), true

	case "complete_task":
		taskID := stringArg(args, "task_id")
		artifact := stringArg(args, "artifact")
		return "task:completed", truncate(fmt.Sprintf("task_id=%s artifact=%s", taskID, artifact)), true

	case "update_task":
		taskID := stringArg(args, "task_id")
		return "task:updated", truncate(fmt.Sprintf("task_id=%s", taskID)), true

	case "add_task":
		return "task:added", truncate(stringArg(args, "title")), true

	case "log_decision":
		return "decision:logged", truncate(stringArg(args, "title")), true

	case "confirm_plan":
		phases := stringArg(args, "phases")
		decisions := stringArg(args, "decisions")
		if len(phases) > maxJSONArgBytes {
			phases = ""
		}
		if len(decisions) > maxJSONArgBytes {
			decisions = ""
		}
		return "plan:confirmed", fmt.Sprintf("phases=%d decisions=%d", jsonArrayLen(phases), jsonArrayLen(decisions)), true

	case "set_session_handoff":
		intent := stringArg(args, "intent")
		return "session:handoff", truncate(intent), true

	case "start_work":
		repoName := stringArg(args, "repo_name")
		return "worksession:started", truncate(repoName), true

	case "finish_work":
		sessID := stringArg(args, "session_id")
		return "worksession:finished", truncate(sessID), true

	case "checkpoint_work":
		sessID := stringArg(args, "session_id")
		return "worksession:checkpointed", truncate(sessID), true

	case "get_active_work":
		return "", "", false // read-only: no audit log needed

	case "reconcile_dashboard":
		return "dashboard:reconciled", "", true

	default:
		return "", "", false
	}
}

// truncate caps notes at maxNotesBytes to prevent unbounded DB writes.
func truncate(s string) string {
	if len(s) <= maxNotesBytes {
		return s
	}
	return s[:maxNotesBytes]
}

// jsonArrayLen parses a JSON array string and returns its length.
// Returns 0 for empty strings or invalid JSON.
func jsonArrayLen(raw string) int {
	if raw == "" {
		return 0
	}
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return 0
	}
	return len(arr)
}
