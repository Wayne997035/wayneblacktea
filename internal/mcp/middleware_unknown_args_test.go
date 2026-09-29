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

// wantCompleteTaskUnknownArgText is the exact rejection text
// unknownArgsMiddleware produces for a complete_task call whose only
// unknown key is artifact_url: the dispatch ticket fixes the format as
// `unknown argument(s): <%q-quoted,sorted,joined>[ (+N more)]; valid
// arguments: <sorted,joined>` ([SEC-194-01]; the unknown half is %q-quoted,
// the valid half is not — valid argument names come from the server's own
// registered schema, never from caller input). complete_task declares
// exactly {artifact, task_id} (tools_gtd.go), sorted alphabetically.
//
// Asserted with an EXACT match below, not strings.Contains: "artifact_url"
// itself contains the substring "artifact", so a Contains(text, "artifact")
// check passes trivially the moment Contains(text, "artifact_url") already
// passed — it never actually verifies the valid-arguments half of the
// message is present.
const wantCompleteTaskUnknownArgText = `unknown argument(s): "artifact_url"; valid arguments: artifact, task_id`

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
	if text != wantCompleteTaskUnknownArgText {
		t.Errorf("error text = %q, want exact %q", text, wantCompleteTaskUnknownArgText)
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
	if text != wantCompleteTaskUnknownArgText {
		t.Errorf("error text = %q, want exact %q", text, wantCompleteTaskUnknownArgText)
	}
}

// TestUnknownArgsMiddleware_SanitizesControlCharsInKey [SEC-194-01]: an
// unknown key is caller-controlled and this middleware's rejection message
// is later persisted verbatim by disciplineMiddleware and returned verbatim
// by system_health — so a key carrying a newline or an ANSI escape must not
// reach the message unsanitised. sanitizeAuditText strips bytes < 0x20
// (except \t) before %q-quoting.
func TestUnknownArgsMiddleware_SanitizesControlCharsInKey(t *testing.T) {
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
		"task_id":          "11111111-1111-1111-1111-111111111111",
		"bad\nkey\x1b[31m": "x",
	}

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unknownArgsMiddleware returned a Go error: %v", err)
	}
	if nextCalled {
		t.Fatal("next was called despite an undeclared top-level argument")
	}
	text := extractResultText(res, 2000)
	if strings.ContainsAny(text, "\n\x1b") {
		t.Errorf("error text %q still contains a raw control byte", text)
	}
	const wantQuoted = `"badkey[31m"`
	if !strings.Contains(text, wantQuoted) {
		t.Errorf("error text %q does not contain the sanitised+quoted key %s", text, wantQuoted)
	}
}

// TestUnknownArgsMiddleware_TruncatesLongKey [SEC-194-01]: a 1000-byte
// unknown key must be capped at unknownArgKeyMaxRunes before it enters the
// message, so a caller cannot use a rejected call to smuggle an
// unboundedly large string into discipline_events / system_health.
func TestUnknownArgsMiddleware_TruncatesLongKey(t *testing.T) {
	t.Parallel()
	srv, _ := newTestMCPServer(t)

	longKey := strings.Repeat("a", 1000)

	var nextCalled bool
	next := func(ctx context.Context, req mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
		nextCalled = true
		return mcpmsg.NewToolResultText("handler reached"), nil
	}
	handler := srv.unknownArgsMiddleware()(next)

	req := mcpmsg.CallToolRequest{}
	req.Params.Name = toolCompleteTask
	req.Params.Arguments = map[string]any{
		"task_id": "11111111-1111-1111-1111-111111111111",
		longKey:   "x",
	}

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unknownArgsMiddleware returned a Go error: %v", err)
	}
	if nextCalled {
		t.Fatal("next was called despite an undeclared top-level argument")
	}
	text := extractResultText(res, 2000)

	wantQuoted := `"` + strings.Repeat("a", unknownArgKeyMaxRunes) + `"`
	if !strings.Contains(text, wantQuoted) {
		t.Errorf("error text does not contain the key capped at exactly %d runes: %q", unknownArgKeyMaxRunes, text)
	}
	if strings.Contains(text, strings.Repeat("a", unknownArgKeyMaxRunes+1)) {
		t.Errorf("error text contains more than %d consecutive a's — the cap did not truncate: %q", unknownArgKeyMaxRunes, text)
	}
	if len(text) >= 500 {
		t.Errorf("error text is %d bytes, want < 500: %q", len(text), text)
	}
}

// TestUnknownArgsMiddleware_CapsListedCount [SEC-194-01]: 30 unknown keys
// must not all be echoed — only the first unknownArgMaxListed (sorted),
// with the remainder collapsed into a "(+N more)" suffix, so a caller
// cannot use a rejected call to inflate the message with an arbitrary
// number of keys.
func TestUnknownArgsMiddleware_CapsListedCount(t *testing.T) {
	t.Parallel()
	srv, _ := newTestMCPServer(t)

	args := map[string]any{
		"task_id": "11111111-1111-1111-1111-111111111111",
	}
	const totalUnknown = 30
	for i := 0; i < totalUnknown; i++ {
		args[fmt.Sprintf("k%02d", i)] = "x"
	}

	var nextCalled bool
	next := func(ctx context.Context, req mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
		nextCalled = true
		return mcpmsg.NewToolResultText("handler reached"), nil
	}
	handler := srv.unknownArgsMiddleware()(next)

	req := mcpmsg.CallToolRequest{}
	req.Params.Name = toolCompleteTask
	req.Params.Arguments = args

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unknownArgsMiddleware returned a Go error: %v", err)
	}
	if nextCalled {
		t.Fatal("next was called despite 30 undeclared top-level arguments")
	}
	text := extractResultText(res, 2000)

	for i := 0; i < unknownArgMaxListed; i++ {
		want := fmt.Sprintf("%q", fmt.Sprintf("k%02d", i))
		if !strings.Contains(text, want) {
			t.Errorf("error text missing listed key %s: %q", want, text)
		}
	}
	for i := unknownArgMaxListed; i < totalUnknown; i++ {
		want := fmt.Sprintf("%q", fmt.Sprintf("k%02d", i))
		if strings.Contains(text, want) {
			t.Errorf("error text lists key %s beyond the %d-key cap: %q", want, unknownArgMaxListed, text)
		}
	}
	wantSuffix := fmt.Sprintf("(+%d more)", totalUnknown-unknownArgMaxListed)
	if !strings.Contains(text, wantSuffix) {
		t.Errorf("error text does not contain the %q suffix: %q", wantSuffix, text)
	}
}

// TestUnknownArgsMiddleware_EscapesNonControlUnprintableRune [SEC-194-01]:
// U+2028 LINE SEPARATOR is not an ASCII control byte, so sanitizeAuditText
// does not strip it — %q-quoting is what must catch it. strconv.Quote (the
// semantics of %q) escapes any rune unicode.IsPrint rejects, and U+2028
// (category Zl) is one of them.
func TestUnknownArgsMiddleware_EscapesNonControlUnprintableRune(t *testing.T) {
	t.Parallel()
	srv, _ := newTestMCPServer(t)

	const key = "a b"

	var nextCalled bool
	next := func(ctx context.Context, req mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
		nextCalled = true
		return mcpmsg.NewToolResultText("handler reached"), nil
	}
	handler := srv.unknownArgsMiddleware()(next)

	req := mcpmsg.CallToolRequest{}
	req.Params.Name = toolCompleteTask
	req.Params.Arguments = map[string]any{
		"task_id": "11111111-1111-1111-1111-111111111111",
		key:       "x",
	}

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unknownArgsMiddleware returned a Go error: %v", err)
	}
	if nextCalled {
		t.Fatal("next was called despite an undeclared top-level argument")
	}
	text := extractResultText(res, 2000)

	// wantEscape is built via concatenation, not a literal escape sequence in
	// source: a bare backslash-u-2028 text run in this file has been observed
	// to get pre-decoded into the raw U+2028 rune somewhere upstream of the
	// Go compiler, which would defeat the point of this assertion.
	wantEscape := `\` + "u2028"
	if !strings.Contains(text, wantEscape) {
		t.Errorf("error text does not contain the escaped %s sequence: %q", wantEscape, text)
	}
	if strings.ContainsRune(text, ' ') {
		t.Errorf("error text still contains the raw U+2028 rune: %q", text)
	}
}
