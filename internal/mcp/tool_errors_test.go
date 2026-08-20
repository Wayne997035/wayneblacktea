package mcp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
)

// sentinelDriverError is shaped like the class of text U14 exists to keep out
// of an LLM's context: a driver-level message naming the schema object that
// failed and the connection it failed on. If a handler ever renders this
// verbatim, the assertions below can point at the exact substring.
const sentinelDriverError = `ERROR: duplicate key value violates unique constraint ` +
	`"tasks_workspace_id_title_key" (SQLSTATE 23505) [host=db.internal.example port=5432 db=wbt_prod]`

// rawErrorIdent matches an identifier shaped like an error variable — `err`,
// `readErr`, `mErr`, `dErr`, `Err` — used to flag an interpolated error value
// inside an already bracket-matched NewToolResultError(...) argument string.
//
// The scan is textual/syntactic on purpose, not type-aware: a type-aware
// version would need to resolve what each identifier refers to, and the
// property being enforced here is syntactic — "no error-shaped identifier
// appears inside this constructor's argument list" — which is exactly what a
// reviewer checks by eye and exactly what stops being checked once the file
// is 1,600 lines long.
var rawErrorIdent = regexp.MustCompile(`\b\w*[eE]rr\b`)

// scanNewToolResultErrorCall is one `NewToolResultError(` call site found by
// scanNewToolResultErrorCalls: its 1-based source line and its complete,
// bracket-matched argument text.
type scanNewToolResultErrorCall struct {
	line int
	args string
}

// scanNewToolResultErrorCalls finds every `NewToolResultError(` call in body
// and returns each one's COMPLETE argument text, matched by paren depth
// rather than by a `[^)]*`-up-to-the-first-`)` regex.
//
// The regex this replaced broke on any argument containing its own literal
// ')' — e.g. `NewToolResultError(fmt.Sprintf("resolving proposal (entity
// already created): %v", err))` (tools_proposal.go): `[^)]*` matched only up
// to the ')' inside "(entity already created)" and never reached `err`, so
// the call passed the gate with a raw driver/domain error still reaching the
// client. Depth-counting fixes that AND, as a side effect, no longer cares
// whether the call is on one line or wrapped across several — both were the
// same underlying gap (the scan matched TEXT SHAPES, not the call's actual
// grouping).
//
// Depth-counting alone would introduce a different miss: a string, rune
// literal, or comment can contain an unbalanced paren that does not belong to
// the call's own grouping (ordinary Go source, not a contrived case — see the
// example above). matchClosingParen treats those spans as opaque so their
// parens never affect depth.
func scanNewToolResultErrorCalls(body string) []scanNewToolResultErrorCall {
	const needle = "NewToolResultError("
	var calls []scanNewToolResultErrorCall
	searchFrom := 0
	for {
		rel := strings.Index(body[searchFrom:], needle)
		if rel < 0 {
			break
		}
		callStart := searchFrom + rel
		argStart := callStart + len(needle)
		argEnd, ok := matchClosingParen(body, argStart)
		if !ok {
			// Unbalanced/truncated — cannot happen in a file that compiles,
			// but stop scanning this file rather than loop forever on
			// malformed input.
			break
		}
		calls = append(calls, scanNewToolResultErrorCall{
			line: 1 + strings.Count(body[:callStart], "\n"),
			args: body[argStart:argEnd],
		})
		searchFrom = argEnd + 1
	}
	return calls
}

// matchClosingParen returns the index of the ')' that closes the '(' whose
// argument list starts at from (from already points PAST that opening paren,
// so depth starts at 1) — skipping over string/rune literals and comments so
// a paren embedded in one of those never changes depth. Returns (0, false)
// if body ends before depth returns to 0 (unbalanced input).
func matchClosingParen(body string, from int) (int, bool) {
	depth := 1
	i := from
	for i < len(body) {
		switch {
		case body[i] == '(':
			depth++
			i++
		case body[i] == ')':
			depth--
			if depth == 0 {
				return i, true
			}
			i++
		case body[i] == '"':
			i = skipQuoted(body, i, '"')
		case body[i] == '`':
			i = skipRawString(body, i)
		case body[i] == '\'':
			i = skipQuoted(body, i, '\'')
		case body[i] == '/' && i+1 < len(body) && body[i+1] == '/':
			i = skipToEOL(body, i)
		case body[i] == '/' && i+1 < len(body) && body[i+1] == '*':
			i = skipBlockComment(body, i)
		default:
			i++
		}
	}
	return 0, false
}

// skipQuoted advances past a double-quoted string or rune literal starting at
// body[i] (the opening quote character q), honouring backslash escapes, and
// returns the index just past the closing quote — or len(body) if the
// literal is unterminated.
func skipQuoted(body string, i int, q byte) int {
	i++ // past opening quote
	for i < len(body) {
		if body[i] == '\\' {
			i += 2 // skip the escaped character too, including an escaped quote
			continue
		}
		if body[i] == q {
			return i + 1
		}
		i++
	}
	return i
}

// skipRawString advances past a backtick raw string literal (Go raw strings
// have no backslash escapes) and returns the index just past the closing
// backtick, or len(body) if unterminated.
func skipRawString(body string, i int) int {
	i++
	for i < len(body) && body[i] != '`' {
		i++
	}
	if i < len(body) {
		i++
	}
	return i
}

// skipToEOL advances to (not past) the next newline, for a `//` comment.
func skipToEOL(body string, i int) int {
	for i < len(body) && body[i] != '\n' {
		i++
	}
	return i
}

// skipBlockComment advances past a /* ... */ comment starting at body[i] and
// returns the index just past the closing "*/", or len(body) if unterminated.
func skipBlockComment(body string, i int) int {
	i += 2 // past "/*"
	for i+1 < len(body) && (body[i] != '*' || body[i+1] != '/') {
		i++
	}
	if i+1 < len(body) {
		return i + 2
	}
	return len(body)
}

// oneLine collapses a (possibly multi-line) NewToolResultError argument list
// to a single line for a readable violation message.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TestNoRawErrorReachesToolResult is U14's structural gate.
//
// Before the sweep this matched 125 call sites across 24 files. It is the test
// that makes the fix stick: the leak was never one bug, it was the absence of
// a shared exit, so every new handler written the obvious way reintroduced it.
// With this test the obvious way is red and the two helpers in tool_errors.go
// are the only way through — for call sites this test actually scans. See
// tool_errors.go's doc comment for what that scope does NOT cover (any of the
// package's 260+ direct mcp.NewToolResultError(...) call sites that never
// interpolate an error at all are invisible to this test by construction; it
// only catches the pattern this file is named for).
//
// The exemption list is empty and should stay that way. A handler that
// genuinely needs to echo an error is describing the CALLER's input, and
// inputErrorResult already covers that case by construction.
func TestNoRawErrorReachesToolResult(t *testing.T) {
	files := goSourceFilesInPackageDir(t)
	var violations []string
	for name, body := range files {
		if name == "tool_errors.go" {
			continue // the helpers themselves are where err legitimately lives
		}
		for _, call := range scanNewToolResultErrorCalls(body) {
			if rawErrorIdent.MatchString(call.args) {
				violations = append(violations, name+":"+itoa(call.line)+"  "+oneLine(call.args))
			}
		}
	}
	if len(violations) > 0 {
		t.Errorf("%d tool result(s) interpolate a raw error — route them through "+
			"storeErrorResult (server-side failure) or inputErrorResult (caller's own "+
			"argument failed validation), see tool_errors.go:", len(violations))
		for _, v := range violations {
			t.Error("  " + v)
		}
	}
}

// TestScanNewToolResultErrorCalls_ParenInMessageCaught is M-1's required bad
// case: an error message containing its own parenthesised clause — the exact
// shape (tools_proposal.go:567, pre-fix) that let a raw error reach the
// client under the old `[^)]*`-terminated-at-the-first-')' regex. The old
// scan matched zero calls here because "(detail)"'s own ')' ended the match
// before `err` was ever reached; the new one must find the call AND still
// flag it as carrying a raw error identifier.
func TestScanNewToolResultErrorCalls_ParenInMessageCaught(t *testing.T) {
	src := `func h() {
	return mcp.NewToolResultError(fmt.Sprintf("loading task (detail): %v", err)), nil
}
`
	calls := scanNewToolResultErrorCalls(src)
	if len(calls) != 1 {
		t.Fatalf("scanNewToolResultErrorCalls found %d call(s), want 1: %+v", len(calls), calls)
	}
	if !rawErrorIdent.MatchString(calls[0].args) {
		t.Errorf("call args did not flag as carrying a raw error identifier: %q", calls[0].args)
	}
	if calls[0].line != 2 {
		t.Errorf("call line = %d, want 2 (file:line must point at the actual call)", calls[0].line)
	}
}

// TestScanNewToolResultErrorCalls_CrossLineCallCaught is M-1's required
// cross-line bad case: the call and its raw error argument sit on different
// source lines. A line-scoped scan (the pre-fix regex ran per-line) cannot
// see across the newline at all; the bracket-matching scan must.
func TestScanNewToolResultErrorCalls_CrossLineCallCaught(t *testing.T) {
	src := `func h() {
	return mcp.NewToolResultError(
		fmt.Sprintf("beginning task: %v",
			err),
	), nil
}
`
	calls := scanNewToolResultErrorCalls(src)
	if len(calls) != 1 {
		t.Fatalf("scanNewToolResultErrorCalls found %d call(s), want 1: %+v", len(calls), calls)
	}
	if !rawErrorIdent.MatchString(calls[0].args) {
		t.Errorf("cross-line call args did not flag as carrying a raw error identifier: %q", calls[0].args)
	}
	if calls[0].line != 2 {
		t.Errorf("call line = %d, want 2 (the line NewToolResultError( itself starts on)", calls[0].line)
	}
}

// TestScanNewToolResultErrorCalls_LegitCallsDoNotFalsePositive is M-1's
// positive control: shapes that must NOT be flagged, so the tightened scan
// has not become trigger-happy. Covers inputErrorResult's own legitimate
// interpolation (a different function name — never matches the
// `NewToolResultError(` needle at all) and non-error field names that merely
// contain "err" as a substring without being an error identifier boundary
// (e.g. "Terr" a place name would still match \b\w*[eE]rr\b by design — this
// case instead covers a call with NO error-shaped identifier anywhere in its
// arguments).
func TestScanNewToolResultErrorCalls_LegitCallsDoNotFalsePositive(t *testing.T) {
	src := `func h() {
	x := inputErrorResult("field", err)
	return mcp.NewToolResultError("repo_name must match [a-zA-Z0-9_.-]{1,100}"), nil
}
`
	calls := scanNewToolResultErrorCalls(src)
	if len(calls) != 1 {
		t.Fatalf("scanNewToolResultErrorCalls found %d call(s), want 1 (inputErrorResult must not match "+
			"the NewToolResultError( needle): %+v", len(calls), calls)
	}
	if rawErrorIdent.MatchString(calls[0].args) {
		t.Errorf("static validation message was flagged as carrying a raw error: %q", calls[0].args)
	}
}

// TestMatchClosingParen_StringLiteralParenDoesNotMiscountDepth is the
// unit-level regression for the exact bug class this dispatch fixes: a ')'
// living inside a string literal must not be mistaken for the call's own
// closing paren, even independent of the error-identifier check above.
func TestMatchClosingParen_StringLiteralParenDoesNotMiscountDepth(t *testing.T) {
	body := `foo("a) b") + bar()`
	// from points just past `foo(` — depth 1 begins here.
	end, ok := matchClosingParen(body, len(`foo(`))
	if !ok {
		t.Fatal("matchClosingParen reported unbalanced input on balanced source")
	}
	got := body[len(`foo(`):end]
	want := `"a) b"`
	if got != want {
		t.Errorf("matched args = %q, want %q — the ')' inside the string literal was treated as closing the call", got, want)
	}
}

// bufferLogger installs a slog handler writing into buf for the duration of
// the test, and restores the previous default on cleanup.
func bufferLogger(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// TestStoreErrorResult_LogRetainsFullError is U14 acceptance criterion 2:
// redaction is client-facing only, not a diagnostics loss.
//
// Without this, the cheapest way to pass criterion 1 would be to drop the
// error on the floor, which trades an information leak for an unfixable
// production incident. Both halves are asserted in one test on purpose —
// they are two directions of the same property and splitting them invites
// one to be deleted alone.
func TestStoreErrorResult_LogRetainsFullError(t *testing.T) {
	buf := bufferLogger(t)
	err := errors.New(sentinelDriverError)

	res := storeErrorResult("loading projects", err)

	client := resultText(res)
	if strings.Contains(client, "SQLSTATE") || strings.Contains(client, "db.internal.example") {
		t.Errorf("driver detail reached the client: %q", client)
	}
	if !strings.Contains(client, "loading projects") {
		t.Errorf("client message lost the operation name, leaving the caller nothing: %q", client)
	}

	logged := buf.String()
	if !strings.Contains(logged, "SQLSTATE 23505") || !strings.Contains(logged, "db.internal.example") {
		t.Errorf("server log did NOT retain the full error — redaction must be client-facing "+
			"only, otherwise this change costs an operator the only copy: %q", logged)
	}
	if !strings.Contains(logged, "loading projects") {
		t.Errorf("server log lost the op field, making the entry hard to attribute: %q", logged)
	}
}

// TestInputErrorResult_EchoesValidationDetail is the positive control for the
// half of U14 that must NOT change.
//
// A sweep that replaced every error message with "<op> failed" would pass the
// structural gate above and still be a regression: an LLM that sent a
// malformed metrics_json needs to be told what was malformed, and that text
// describes its own argument, not the server.
func TestInputErrorResult_EchoesValidationDetail(t *testing.T) {
	err := errors.New("invalid character 'x' looking for beginning of value")

	withField := resultText(inputErrorResult("metrics_json", err))
	if !strings.Contains(withField, "invalid character 'x'") {
		t.Errorf("validation detail was suppressed, leaving the caller unable to fix its "+
			"own argument: %q", withField)
	}
	if !strings.Contains(withField, "metrics_json") {
		t.Errorf("field name missing, so the caller cannot tell WHICH argument failed: %q", withField)
	}

	bare := resultText(inputErrorResult("", err))
	if !strings.Contains(bare, "invalid character 'x'") {
		t.Errorf("empty-field form dropped the detail: %q", bare)
	}
	if strings.HasPrefix(bare, ": ") {
		t.Errorf("empty-field form emitted a dangling separator: %q", bare)
	}
}

// failingGTDStore fails GetTaskByID with the sentinel driver error. It is the
// narrowest possible end-to-end path from "a store returned a driver error" to
// "what the MCP client actually sees", which is the thing criterion 1 asserts
// and which neither of the unit tests above touches.
//
// It embeds noopGTDStore (tools_contextpack_fakes_test.go) so it satisfies
// gtd.StoreIface without restating the other ~40 methods; only the one method
// under test is overridden.
type failingGTDStore struct {
	noopGTDStore
}

func (failingGTDStore) GetTaskByID(context.Context, uuid.UUID) (*db.Task, error) {
	return nil, errors.New(sentinelDriverError)
}

// TestHandler_StoreFailureDoesNotLeakDriverError is U14 acceptance criterion 1
// end-to-end through a real handler: a store returning a driver error must not
// put that error's text into the tool response.
//
// One handler, not all 100+: the structural gate above is what covers the
// whole surface, and a per-handler fake would be 100 copies of this same
// assertion whose real content is "the helper works". This test proves the
// helper works where it actually runs — inside a handler, on the error path
// that sits beside jsonText's success path — so the structural gate's claim
// has a behavioural floor under it rather than being purely syntactic.
//
// get_task is the handler chosen because it has the shortest path from store
// call to response: one GetTaskByID, one errors.Is branch for the sentinel
// not-found case, and the generic failure below it.
func TestHandler_StoreFailureDoesNotLeakDriverError(t *testing.T) {
	buf := bufferLogger(t)
	s := &Server{gtd: failingGTDStore{}}

	res, err := s.handleGetTask(context.Background(), GetTaskArgs{TaskID: uuid.New()})
	if err != nil {
		t.Fatalf("handleGetTask returned a transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("handler reported success though the store failed")
	}

	client := resultText(res)
	if strings.Contains(client, "SQLSTATE") || strings.Contains(client, "tasks_workspace_id_title_key") ||
		strings.Contains(client, "db.internal.example") {
		t.Errorf("store driver error reached the MCP client verbatim: %q", client)
	}
	if !strings.Contains(client, "loading task") {
		t.Errorf("client lost every hint of what failed: %q", client)
	}
	if !strings.Contains(buf.String(), "SQLSTATE 23505") {
		t.Errorf("the error was suppressed instead of logged: %q", buf.String())
	}
}
