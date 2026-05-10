package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// disciplineRecordTimeout caps the background discipline_events write so a
// stalled DB cannot leak a goroutine forever. The middleware itself returns
// to the caller before the goroutine even starts, so this only bounds
// resource use, not user-facing latency.
const disciplineRecordTimeout = 10 * time.Second

// Audit-text caps for fields persisted into discipline_events. These bound
// LLM-influenced strings (tool name + repo_name argument) before the row hits
// the DB. See backend-security-design.md §5.4 (control-char strip + length
// cap) and CWE-117 (log/audit injection).
const (
	maxToolNameRunes = 128
	maxRepoNameRunes = 256
)

// sanitizeAuditText strips ASCII control bytes (< 0x20 except \t) and caps
// the result at maxRunes runes. Any value with multi-byte runes counts by
// rune (so a 256-rune cap is at most ~1 KB on UTF-8). Empty input returns
// empty output. Matches backend-security-design.md §5.4.
//
// Notes:
//   - \x1b (ANSI ESC) is < 0x20 and therefore stripped, so terminal escape
//     sequences are removed in addition to \r / \n / \x00.
//   - \t is preserved deliberately — repo paths and tool args occasionally
//     legitimately contain tabs; they don't break log/CLI display.
func sanitizeAuditText(s string, maxRunes int) string {
	if s == "" {
		return ""
	}
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return -1
		}
		return r
	}, s)
	if utf8.RuneCountInString(cleaned) > maxRunes {
		runes := []rune(cleaned)
		cleaned = string(runes[:maxRunes])
	}
	return cleaned
}

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

			// LLM-supplied tool name + repo_name arg flow into the DB
			// audit row; sanitise both BEFORE persist (see CWE-117 +
			// backend-security-design.md §5.4). The original `tool` value
			// (still available via req.Params.Name) drives behaviour like
			// IsMutating; the sanitised `toolName` is what we persist.
			rawTool := req.Params.Name
			args := req.GetArguments()

			toolName := sanitizeAuditText(rawTool, maxToolNameRunes)
			repoName := sanitizeAuditText(stringArg(args, "repo_name"), maxRepoNameRunes)

			params := discipline.InsertParams{
				SessionID:   s.sessionID,
				RepoName:    repoName,
				ToolName:    toolName,
				IsMutating:  discipline.IsMutating(rawTool),
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
							"tool", toolName,
							"panic", fmt.Sprintf("%v", r),
						)
					}
				}()
				bgCtx, cancel := context.WithTimeout(context.Background(), disciplineRecordTimeout)
				defer cancel()
				if insertErr := s.discipline.Insert(bgCtx, params); insertErr != nil {
					slog.Warn("disciplineMiddleware: failed to record event",
						"tool", toolName,
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
