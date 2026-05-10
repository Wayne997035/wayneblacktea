package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// disciplineRecordTimeout caps the background discipline_events write so a
// stalled DB cannot leak a goroutine forever. The middleware itself returns
// to the caller before the goroutine even starts, so this only bounds
// resource use, not user-facing latency.
const disciplineRecordTimeout = 10 * time.Second

// disciplineMiddleware wraps every tool handler and, after a successful tool
// dispatch, records a discipline_events row. The write happens in a
// background goroutine using context.Background() so a request-context
// cancellation cannot drop the audit row mid-flight.
//
// Errors writing the event MUST NOT fail the tool call — they are logged via
// slog.Warn and we move on (per backend-security-design.md §5.1: hook
// binaries / observability sinks must never break the user-facing path).
func (s *Server) disciplineMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
			res, err := next(ctx, req)
			// Only record successful (non-error) results. Failed calls
			// shouldn't count as drift signals — they didn't actually mutate.
			if err != nil || res == nil || res.IsError {
				return res, err
			}

			if s.discipline == nil {
				return res, err
			}

			tool := req.Params.Name
			args := req.GetArguments()
			repoName := stringArg(args, "repo_name")

			params := discipline.InsertParams{
				SessionID:   s.sessionID,
				RepoName:    repoName,
				ToolName:    tool,
				IsMutating:  discipline.IsMutating(tool),
				WorkspaceID: s.workspaceID,
			}

			// Background goroutine so the audit write cannot block / fail the
			// tool response. context.Background() with a fresh timeout — never
			// inherit the request ctx, which is about to be cancelled.
			//nolint:gosec // G118: intentional — goroutine must outlive request ctx so the audit row survives
			go func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Warn("disciplineMiddleware: panic in background goroutine",
							"tool", tool,
							"panic", fmt.Sprintf("%v", r),
						)
					}
				}()
				bgCtx, cancel := context.WithTimeout(context.Background(), disciplineRecordTimeout)
				defer cancel()
				if insertErr := s.discipline.Insert(bgCtx, params); insertErr != nil {
					slog.Warn("disciplineMiddleware: failed to record event",
						"tool", tool,
						"session_id", s.sessionID,
						"is_mutating", params.IsMutating,
						"error", insertErr,
					)
				}
			}()

			return res, err
		}
	}
}
