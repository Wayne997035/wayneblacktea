package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// unknownArgsRPCResult issues a real tools/call over ms.HandleMessage (same
// JSON-RPC entry point every transport uses) and returns the tool-level
// isError flag plus the first text content block, WITHOUT failing the test
// on isError — unlike rpcCallToolText (tools_expand_test.go), which exists
// for callers that expect success. A rejection is the expected outcome for
// several tests below, so asserting on it has to be the caller's job.
func unknownArgsRPCResult(t *testing.T, ms *server.MCPServer, ctx context.Context, name, argsJSON string) (isError bool, text string) {
	t.Helper()
	if argsJSON == "" {
		argsJSON = "{}"
	}
	req := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":%q,"arguments":%s}}`,
		name, argsJSON,
	)
	resp := ms.HandleMessage(ctx, json.RawMessage(req))
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal %s response: %v", name, err)
	}
	var decoded struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode %s response: %v", name, err)
	}
	if decoded.Error != nil {
		t.Fatalf("%s returned JSON-RPC error: %s", name, decoded.Error.Message)
	}
	if len(decoded.Result.Content) == 0 {
		t.Fatalf("%s returned no content", name)
	}
	return decoded.Result.IsError, decoded.Result.Content[0].Text
}

// TestUnknownArgsMiddleware_RejectsMisspelledArg [F0929-01] is the positive
// control: complete_task's real registered schema only declares task_id and
// artifact (tools_gtd.go), so sending artifact_url instead of artifact must
// be rejected before handleCompleteTask ever runs — task_id below is not a
// real row, which would surface as a DIFFERENT failure if the middleware
// let the call through.
func TestUnknownArgsMiddleware_RejectsMisspelledArg(t *testing.T) {
	t.Parallel()
	srv, _ := newTestMCPServer(t)

	var nextCalled bool
	next := func(ctx context.Context, req mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
		nextCalled = true
		return mcpmsg.NewToolResultText("handler reached"), nil
	}
	handler := srv.unknownArgsMiddleware()(next)

	req := mcpmsg.CallToolRequest{}
	req.Params.Name = toolCompleteTask
	req.Params.Arguments = map[string]any{
		"task_id":      "11111111-1111-1111-1111-111111111111",
		"artifact_url": "https://example.com/pr/1",
	}

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unknownArgsMiddleware returned a Go error: %v", err)
	}
	if nextCalled {
		t.Fatal("next was called despite an undeclared top-level argument (artifact_url)")
	}
	if res == nil || !res.IsError {
		t.Fatalf("want a tool error (IsError=true), got %+v", res)
	}
	text := extractResultText(res, 2000)
	if !strings.Contains(text, "artifact_url") {
		t.Errorf("error text %q does not name the unknown argument artifact_url", text)
	}
	if !strings.Contains(text, "artifact") {
		t.Errorf("error text %q does not name the valid argument artifact", text)
	}
}

// TestUnknownArgsMiddleware_AllowsDeclaredArgsForEveryTool is the reverse
// guarantee: every tool ms actually has registered (via ListTools, never a
// hand-typed list — that would drift the moment a tool's schema changes)
// must still work when called with exactly its own declared argument names.
// If any tool fails here, it means some handler reads a key it never
// declared via mcp.With*, which is the STOP condition in the dispatch ticket
// rather than something this test should paper over.
func TestUnknownArgsMiddleware_AllowsDeclaredArgsForEveryTool(t *testing.T) {
	t.Parallel()
	srv, ms := newTestMCPServer(t)

	registered := ms.ListTools()
	if len(registered) == 0 {
		t.Fatal("ms.ListTools() returned no tools — nothing to verify")
	}

	for name, tool := range registered {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			args := make(map[string]any, len(tool.Tool.InputSchema.Properties))
			for key := range tool.Tool.InputSchema.Properties {
				// The middleware only inspects KEYS, never value shape or
				// type, so a placeholder value exercises the same code path
				// as a real argument would.
				args[key] = "x"
			}

			var nextCalled bool
			var nextRes *mcpmsg.CallToolResult
			next := func(ctx context.Context, req mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
				nextCalled = true
				nextRes = mcpmsg.NewToolResultText("stub")
				return nextRes, nil
			}
			handler := srv.unknownArgsMiddleware()(next)

			req := mcpmsg.CallToolRequest{}
			req.Params.Name = name
			req.Params.Arguments = args

			res, err := handler(context.Background(), req)
			if err != nil {
				t.Fatalf("unknownArgsMiddleware returned a Go error for %s: %v", name, err)
			}
			if !nextCalled {
				t.Fatalf(
					"tool %s: middleware rejected a call using only its own declared arguments: %s",
					name, extractResultText(res, 2000),
				)
			}
			if res != nextRes {
				t.Fatalf("tool %s: middleware replaced next's result instead of passing it through unchanged", name)
			}
		})
	}
}

// TestUnknownArgsMiddleware_AllowsEmptyArgs covers both shapes an
// argument-free call can take on the wire: an entirely absent arguments
// field (Params.Arguments left at its zero value, a nil any) and an
// explicit empty object. Neither has any key to be unknown, so both must
// reach next regardless of which tool is named.
func TestUnknownArgsMiddleware_AllowsEmptyArgs(t *testing.T) {
	t.Parallel()
	srv, _ := newTestMCPServer(t)

	run := func(t *testing.T, label string, args any) {
		t.Helper()
		var nextCalled bool
		next := func(ctx context.Context, req mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
			nextCalled = true
			return mcpmsg.NewToolResultText("ok"), nil
		}
		handler := srv.unknownArgsMiddleware()(next)

		req := mcpmsg.CallToolRequest{}
		req.Params.Name = toolCompleteTask
		req.Params.Arguments = args

		res, err := handler(context.Background(), req)
		if err != nil {
			t.Fatalf("unknownArgsMiddleware returned a Go error: %v", err)
		}
		if !nextCalled {
			t.Fatalf("%s: middleware blocked a call carrying no arguments: %s", label, extractResultText(res, 2000))
		}
	}

	t.Run("nil arguments", func(t *testing.T) {
		t.Parallel()
		run(t, "nil arguments", nil)
	})
	t.Run("empty map arguments", func(t *testing.T) {
		t.Parallel()
		run(t, "empty map arguments", map[string]any{})
	})
}

// TestUnknownArgsMiddleware_EndToEnd proves the middleware is actually wired
// into the chain MCPServer() builds — every test above calls
// unknownArgsMiddleware() directly, which would stay green even if the
// server.go registration line were deleted. This one goes through the real
// server.WithToolHandlerMiddleware pipeline via ms.HandleMessage, exactly as
// cmd/server and internal/mcprunner invoke it.
func TestUnknownArgsMiddleware_EndToEnd(t *testing.T) {
	t.Parallel()
	_, ms := newTestMCPServer(t)
	ctx := ms.WithContext(context.Background(), newFakeSession("sess-unknown-args"))

	isError, text := unknownArgsRPCResult(t, ms, ctx, toolCompleteTask,
		`{"task_id":"11111111-1111-1111-1111-111111111111","artifact_url":"https://example.com/pr/1"}`)
	if !isError {
		t.Fatalf("complete_task with a misspelled artifact_url succeeded through the real MCPServer() wiring: %s", text)
	}
	if !strings.Contains(text, "artifact_url") || !strings.Contains(text, "artifact") {
		t.Errorf("error text %q does not name both artifact_url and artifact", text)
	}
}
