package mcp

// Skeleton, MCP-6 stage 2 implements it. [F1127-01]
//
// This file fixes the contract only: the symbol names, the registration slot
// (innermost, directly after unknownArgsMiddleware) and the empty exempt map.
// The middleware below deliberately passes every call straight to next, so the
// tests in middleware_schema_validation_test.go go red on every case that
// must be blocked. Stage 2 replaces the bodies and moves deriveToolSpec into
// toolspec.go.

import (
	"context"
	"errors"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// schemaValidationExempt lists tools the middleware lets through unvalidated.
// Key = tool name, value = reason. Empty at ship. [F1127-01]
var schemaValidationExempt = map[string]string{}

// schemaValidationMiddleware is the pass-through skeleton. [F1127-01]
func (s *Server) schemaValidationMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
			return next(ctx, req)
		}
	}
}

// deriveToolSpec is a stub; stage 2 extracts it from registerToolSpec.
func deriveToolSpec(tool mcpmsg.Tool) (*toolSpec, error) {
	return nil, errors.New("deriveToolSpec: not implemented")
}
