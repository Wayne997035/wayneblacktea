package mcp

// Generic tool-argument validation middleware (MCP-6, GTD:822f9ae7).
//
// mcp-go advertises each tool's InputSchema but never enforces it, and the 71
// raw handlers silently read a wrong-typed value as its zero value. This
// middleware checks every tools/call against the schema the tool registered
// with, before any handler runs: required present (and a required string not
// blank), declared type matches, enum member. Rejections are isError tool
// results naming parameters only, never values.
//
// It is registered innermost, directly after unknownArgsMiddleware, so an
// unknown or misspelled key is still reported first.
//
// It fails closed: with no MCPServer, no registered tool, or a schema that
// deriveToolSpec cannot read, the call is rejected and next is never invoked.
// Letting such a call through would silently switch the whole layer off.
//
// Risk: mcp-go resolves session-scoped tools before global ones while GetTool
// reads global tools only, so a session tool would be validated against the
// wrong schema. This repo registers none; revisit if one is ever added.

import (
	"context"
	"strings"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// schemaValidationExempt lists tools the middleware lets through unvalidated.
// Key = tool name, value = reason. Empty at ship. [F1127-01]
var schemaValidationExempt = map[string]string{}

// schemaValidationMiddleware validates arguments against the registered schema.
// [F1127-02]
func (s *Server) schemaValidationMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
			name := req.Params.Name

			ms := s.mcpServer.Load()
			if ms == nil {
				return mcpmsg.NewToolResultError(`schema unavailable for tool "` + name + `"`), nil
			}
			tool := ms.GetTool(name)
			if tool == nil {
				return mcpmsg.NewToolResultError(`schema unavailable for tool "` + name + `"`), nil
			}

			if _, exempt := schemaValidationExempt[name]; exempt {
				return next(ctx, req)
			}

			derived, err := deriveToolSpec(tool.Tool)
			if err != nil {
				return mcpmsg.NewToolResultError(`tool "` + name + `" has no readable input schema`), nil
			}

			// Seam tools validate with their registered spec so requiredMsg
			// overrides, noEnum and uuid blank rules are shared. [F1127-07]
			spec := derived
			toolSpecMu.RLock()
			if reg, ok := toolSpecRegistry[name]; ok {
				spec = reg
			}
			toolSpecMu.RUnlock()

			if res := spec.validateShape(req.GetArguments()); res != nil {
				return res, nil
			}
			return next(ctx, req)
		}
	}
}

// validateShape checks args against ts in order and returns the first failure
// (nil when the shape is fine). Not checked here: blank optional strings, UUID
// format, maxLength, task_id prefixes, numeric ranges; those stay with the
// handler or seam. Messages carry parameter names only.
func (ts *toolSpec) validateShape(args map[string]any) *mcpmsg.CallToolResult {
	if res := ts.checkRequired(args); res != nil {
		return res
	}
	if res := ts.checkTypes(args); res != nil {
		return res
	}
	return ts.checkEnums(args)
}

// checkRequired is step a: a required argument that is absent, JSON null, or a
// blank string. [F1127-04]
func (ts *toolSpec) checkRequired(args map[string]any) *mcpmsg.CallToolResult {
	for _, name := range ts.requiredOrder {
		v, present := args[name]
		a := ts.args[name]
		blank := !present || v == nil
		if !blank && a != nil && a.typ == argTypeString {
			// A non-string value on a non-UUID string arg is a type error
			// (step b). A UUID arg follows the seam instead: non-string is
			// blank, so it gets the same "<name> is required [...]" text.
			if _, isStr := v.(string); (isStr || a.isUUID) && ts.blankFor(name, v) {
				blank = true
			}
		}
		if !blank {
			continue
		}
		base := name + " is required"
		if a != nil && !a.isUUID && a.requiredMsg != "" {
			base = a.requiredMsg
		}
		return requiredArgsErrorWith(base, args, ts.requiredOrder, ts.blankFor)
	}
	return nil
}

// checkTypes is step b: the declared type of every present, non-null argument,
// in alphabetical order. [F1127-05]
func (ts *toolSpec) checkTypes(args map[string]any) *mcpmsg.CallToolResult {
	for _, name := range ts.orderedNames {
		v, present := args[name]
		if !present || v == nil {
			continue
		}
		typ := ts.args[name].typ
		if !valueHasType(v, typ) {
			return mcpmsg.NewToolResultError(name + " must be " + typeArticle(typ))
		}
	}
	return nil
}

// valueHasType reports whether a decoded JSON value matches a schema type.
func valueHasType(v any, typ string) bool {
	var ok bool
	switch typ {
	case argTypeString:
		_, ok = v.(string)
	case argTypeNumber:
		_, ok = v.(float64)
	case argTypeBoolean:
		_, ok = v.(bool)
	case "array":
		_, ok = v.([]any)
	case "object":
		_, ok = v.(map[string]any)
	default:
		ok = true
	}
	return ok
}

// checkEnums is step c: enum membership; "" means unset and passes. [F1127-06]
func (ts *toolSpec) checkEnums(args map[string]any) *mcpmsg.CallToolResult {
	for _, name := range ts.orderedNames {
		a := ts.args[name]
		if len(a.enum) == 0 {
			continue
		}
		sv, isStr := args[name].(string)
		if !isStr || sv == "" || containsString(a.enum, sv) {
			continue
		}
		msg := a.enumMsg
		if msg == "" {
			msg = name + " must be one of: " + strings.Join(a.enum, ", ")
		}
		return mcpmsg.NewToolResultError(msg)
	}
	return nil
}

// typeArticle renders a schema type as the noun phrase used in type errors:
// "a string", "a number", "a boolean", "an array", "an object".
func typeArticle(typ string) string {
	if typ == "array" || typ == "object" {
		return "an " + typ
	}
	return "a " + typ
}
