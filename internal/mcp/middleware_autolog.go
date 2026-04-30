package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// autoLogMiddleware wraps tool handlers: after high-signal tools succeed,
// it records an activity_log entry in a background goroutine.
// Goroutine uses context.Background() — never inherits the request context
// to prevent the DB write being cancelled when the request ends.
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
			action, notes, ok := autoLogEntry(tool, args)
			if !ok {
				return res, err
			}

			// Launch in a background goroutine so the log write cannot block
			// or fail the tool response. Use context.Background() with a
			// timeout so the write survives request-context cancellation.
			//nolint:gosec,contextcheck // G118/contextcheck: intentional — goroutine must outlive request ctx to prevent DB write cancellation
			go func() {
				// Recover from any panic so a log failure never crashes the server.
				defer func() {
					if r := recover(); r != nil {
						slog.Warn("autoLogMiddleware: panic in background goroutine",
							"tool", tool,
							"panic", fmt.Sprintf("%v", r),
						)
					}
				}()
				bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()

				if logErr := s.gtd.LogActivity(bgCtx, "wayneblacktea-auto", action, nil, notes); logErr != nil {
					slog.Warn("autoLogMiddleware: failed to log activity",
						"tool", tool,
						"action", action,
						"error", logErr,
					)
				}
			}()

			return res, err
		}
	}
}

// autoLogEntry returns the action string, notes string, and true for the five
// high-signal tools that should produce an activity_log entry. It returns
// ("", "", false) for all other tools.
func autoLogEntry(tool string, args map[string]any) (action, notes string, ok bool) {
	switch tool {
	case "complete_task":
		taskID := stringArg(args, "task_id")
		artifact := stringArg(args, "artifact")
		return "task:completed", fmt.Sprintf("task_id=%s artifact=%s", taskID, artifact), true

	case "add_task":
		title := stringArg(args, "title")
		return "task:added", title, true

	case "log_decision":
		title := stringArg(args, "title")
		return "decision:logged", title, true

	case "confirm_plan":
		phases := stringArg(args, "phases")
		decisions := stringArg(args, "decisions")
		nPhases := jsonArrayLen(phases)
		nDecisions := jsonArrayLen(decisions)
		return "plan:confirmed", fmt.Sprintf("phases=%d decisions=%d", nPhases, nDecisions), true

	case "set_session_handoff":
		intent := stringArg(args, "intent")
		return "session:handoff", intent, true

	default:
		return "", "", false
	}
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
