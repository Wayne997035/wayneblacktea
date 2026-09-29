package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// unknownArgsMiddleware rejects a tools/call whose top-level arguments
// contain any key the target tool never declared in its InputSchema.
// Closes a silent-drop bug: complete_task called with artifact_url instead
// of the declared artifact used to report success with artifact left empty,
// because neither decodeToolArgs (toolspec.go) nor the ~72 legacy handlers
// reading req.GetArguments() directly ever look at keys they were not told
// to read. A misspelled argument therefore behaved exactly like an omitted
// one — silently, not as a rejected call.
//
// mcp-go's handleToolCall applies server.ToolHandlerMiddleware in reverse
// registration order (`for i := len(mw) - 1; i >= 0; i--`), so the LAST
// middleware appended in MCPServer() ends up wrapped directly
// around the tool's own handler — the innermost layer, run right before the
// real handler and right after every outer middleware has already run its
// "before next()" half. unknownArgsMiddleware is registered last (server.go,
// immediately after decisionProposerMiddleware) on purpose: when it rejects
// a call, next() is never invoked, so the tool's own handler never runs —
// but autoLogMiddleware and decisionProposerMiddleware, both registered
// earlier (further out), already gate their own post-next() work on
// res.IsError being false, so a rejection here produces no false audit-log
// or decision-proposal side effect.
//
// Only top-level argument keys are compared against the tool's declared
// property names. Nested objects and JSON-encoded string arguments (e.g.
// confirm_plan's phases) are not inspected — an unknown key one level down
// is out of scope for this middleware. A tool that declares zero
// parameters treats every key in a non-empty arguments map as unknown.
//
// The rejection message names the offending key(s) and the tool's valid
// argument names, but never echoes any argument VALUE: values are
// LLM-agent-supplied and may carry content that should not be reflected
// back into a tool-error string (e.g. into a log downstream of the caller).
func (s *Server) unknownArgsMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
			args := req.GetArguments()
			if len(args) == 0 {
				return next(ctx, req)
			}

			ms := s.mcpServer.Load()
			if ms == nil {
				// Hand-constructed &Server{} in a test, or called before
				// MCPServer() ran. Fail open rather than reject every call.
				slog.Warn(
					"unknownArgsMiddleware: MCPServer not set, allowing call through",
					"tool", req.Params.Name,
				)
				return next(ctx, req)
			}
			tool := ms.GetTool(req.Params.Name)
			if tool == nil {
				// GetTool reads the full registered set regardless of
				// progressive-disclosure filtering (that filter only
				// applies in tools/list), so nil here means the name is
				// genuinely not registered. There is no schema to validate
				// against and no handler this call could have reached
				// either way — fail open and let the "tool not found" path
				// downstream produce the real error.
				slog.Warn(
					"unknownArgsMiddleware: tool not registered, allowing call through",
					"tool", req.Params.Name,
				)
				return next(ctx, req)
			}

			valid := tool.Tool.InputSchema.Properties
			var unknown []string
			for key := range args {
				if _, ok := valid[key]; !ok {
					unknown = append(unknown, key)
				}
			}
			if len(unknown) == 0 { // [F0929-01]
				return next(ctx, req)
			}
			sort.Strings(unknown)

			validNames := make([]string, 0, len(valid))
			for name := range valid {
				validNames = append(validNames, name)
			}
			sort.Strings(validNames)
			validList := "(none)"
			if len(validNames) > 0 {
				validList = strings.Join(validNames, ", ")
			}

			msg := fmt.Sprintf(
				"unknown argument(s): %s; valid arguments: %s",
				strings.Join(unknown, ", "), validList,
			)
			return mcpmsg.NewToolResultError(msg), nil
		}
	}
}
