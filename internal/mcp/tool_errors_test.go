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
//
// unparseable is [F160-05]'s fail-closed signal: matchClosingParen could not
// find this call's closing paren (see scanNewToolResultErrorCalls). args is
// empty in that case — there is no reliable argument text to show — and the
// caller (TestNoRawErrorReachesToolResult) MUST treat unparseable as a
// violation on its own, not merely skip it.
type scanNewToolResultErrorCall struct {
	line        int
	args        string
	unparseable bool
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
//
// [F160-04] The NEEDLE search itself is ALSO masked against comments/strings
// via maskCommentsAndStrings, run once up front — not just the argument scan
// after a needle is found. Without this, a needle that merely APPEARS inside
// a `//` comment (this file's own doc comments name-drop
// "NewToolResultError(" as prose, e.g. the paragraph above) is mistaken for a
// real call site whenever the comment text happens to contain a balanced
// pair of parens after it — a false positive that inflates the call-site
// count for no code reason.
//
// [F160-05] When a needle IS a real call site but matchClosingParen still
// can't balance it (malformed/truncated input, or — as found in
// tool_errors.go's own pre-fix doc comment before [F160-04]'s masking
// existed — a needle inside a comment whose "closing" paren never actually
// arrives before EOF), this used to `break` and silently abandon the rest of
// the file: every real call site AFTER the unparseable one went unscanned
// and unchecked. That is fail-OPEN — the exact failure mode this whole gate
// exists to prevent, just moved into the gate itself. The fix is fail-
// CLOSED: record an `unparseable: true` violation entry for the needle that
// couldn't be balanced, and keep scanning from right after IT so every
// subsequent real call site in the file is still found and still checked.
func scanNewToolResultErrorCalls(body string) []scanNewToolResultErrorCall {
	const needle = "NewToolResultError("
	masked := maskCommentsAndStrings(body) // [F160-04]
	var calls []scanNewToolResultErrorCall
	searchFrom := 0
	for {
		rel := strings.Index(body[searchFrom:], needle)
		if rel < 0 {
			break
		}
		callStart := searchFrom + rel
		if masked[callStart] {
			// [F160-04] The needle text itself sits inside a comment or
			// string literal — not a real call site. Skip past it and keep
			// looking; do NOT run matchClosingParen on it at all, since
			// running it is exactly what produced tool_errors.go's real
			// [F160-05] failure (a comment's own prose parens never close in
			// a way that matches the call-site shape).
			searchFrom = callStart + len(needle)
			continue
		}
		argStart := callStart + len(needle)
		argEnd, ok := matchClosingParen(body, argStart)
		if !ok {
			// [F160-05] Fail closed: record it, then keep scanning past this
			// needle rather than aborting the whole file.
			calls = append(calls, scanNewToolResultErrorCall{
				line:        1 + strings.Count(body[:callStart], "\n"),
				unparseable: true,
			})
			searchFrom = argStart
			continue
		}
		calls = append(calls, scanNewToolResultErrorCall{
			line: 1 + strings.Count(body[:callStart], "\n"),
			args: body[argStart:argEnd],
		})
		searchFrom = argEnd + 1
	}
	return calls
}

// maskCommentsAndStrings returns a slice the same length as body where index
// i is true iff body[i] lies inside a `//` line comment, a `/* */` block
// comment, a double-quoted string/rune literal, or a backtick raw string —
// [F160-04]. scanNewToolResultErrorCalls consults this BEFORE treating a
// needle match as a real call site, so example text inside a doc comment (or
// a needle-shaped string literal) is never counted.
//
// This deliberately duplicates matchClosingParen's four skip* helpers'
// classification logic rather than sharing a walker with it: matchClosingParen
// only needs to know when IT should stop treating parens as call-grouping
// (a local, depth-scoped question starting mid-call), while this function
// answers a different, whole-file question — "is byte i comment/string text
// at all" — asked BEFORE any call site has been found. Forcing one shared
// walker to answer both would need it to track two different starting
// positions and two different stopping conditions simultaneously.
func maskCommentsAndStrings(body string) []bool {
	masked := make([]bool, len(body))
	i := 0
	for i < len(body) {
		switch {
		case body[i] == '"':
			start := i
			i = skipQuoted(body, i, '"')
			markMasked(masked, start, i)
		case body[i] == '`':
			start := i
			i = skipRawString(body, i)
			markMasked(masked, start, i)
		case body[i] == '\'':
			start := i
			i = skipQuoted(body, i, '\'')
			markMasked(masked, start, i)
		case body[i] == '/' && i+1 < len(body) && body[i+1] == '/':
			start := i
			i = skipToEOL(body, i)
			markMasked(masked, start, i)
		case body[i] == '/' && i+1 < len(body) && body[i+1] == '*':
			start := i
			i = skipBlockComment(body, i)
			markMasked(masked, start, i)
		default:
			i++
		}
	}
	return masked
}

// markMasked sets masked[start:end] to true, clamped to masked's bounds.
func markMasked(masked []bool, start, end int) {
	if end > len(masked) {
		end = len(masked)
	}
	for j := start; j < end; j++ {
		masked[j] = true
	}
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
			if call.unparseable {
				// [F160-05] Fail closed: a call the scanner could not
				// balance is itself a violation — "the scanner couldn't
				// verify this one" must block the gate, not silently pass
				// it through unexamined.
				violations = append(violations, name+":"+itoa(call.line)+
					"  <UNPARSEABLE: matchClosingParen could not balance this call's parens>")
				continue
			}
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

// TestF160_04_CommentNeedleWithBalancedParensNotCountedAsCallSite is
// [F160-04]'s required bad case: a `//` comment that name-drops
// "NewToolResultError(" as prose and happens to have a balanced closing
// paren shortly after it — exactly tool_errors.go's real line 40 shape
// before this fix (the doc comment explaining what the gate checks, which
// itself contains the literal text it is describing). Without needle-search
// masking, matchClosingParen finds a spurious "call" here (its args are the
// prose between the two parens) and counts it as a call site even though no
// code calls anything on this line.
func TestF160_04_CommentNeedleWithBalancedParensNotCountedAsCallSite(t *testing.T) {
	src := `package mcp

// mcp.NewToolResultError(...) call anywhere in this package (outside this
// file) may contain an error-shaped identifier in its argument list.
func h() {
	return mcp.NewToolResultError("real call, unrelated to the comment above")
}
`
	calls := scanNewToolResultErrorCalls(src)
	if len(calls) != 1 {
		t.Fatalf("scanNewToolResultErrorCalls found %d call(s), want exactly 1 (the real call on the "+
			"return line) — the comment's own name-dropped \"NewToolResultError(...) ... (outside this\" "+
			"text must not be counted as a second call site: %+v", len(calls), calls)
	}
	if calls[0].unparseable {
		t.Fatalf("the one call found was flagged unparseable, want the real call's balanced args: %+v", calls[0])
	}
	if !strings.Contains(calls[0].args, "real call") {
		t.Errorf("the call found was not the real one on the return line: %q", calls[0].args)
	}
}

// TestF160_05_UnparseableCallDoesNotAbortRestOfFile is [F160-05]'s required
// bad case: hits the "scanner gives up early" direction none of the
// pre-existing tests covered. A needle whose parens matchClosingParen can
// never balance (an opening paren inside it that is never closed anywhere
// else in the file) must NOT stop the scan — the old `break` behaviour is
// exactly the tool_errors.go bug: real call sites physically AFTER the
// unparseable one went unscanned and unchecked, silently.
func TestF160_05_UnparseableCallDoesNotAbortRestOfFile(t *testing.T) {
	src := `package mcp

func broken() {
	return mcp.NewToolResultError(unbalanced(
}

func legit() {
	return mcp.NewToolResultError("this call sits after the broken one"), nil
}
`
	calls := scanNewToolResultErrorCalls(src)
	if len(calls) != 2 {
		t.Fatalf("scanNewToolResultErrorCalls found %d call(s), want 2 (the unparseable one AND the "+
			"real one that follows it) — a `break` on the first would silently drop the second: %+v",
			len(calls), calls)
	}
	if !calls[0].unparseable {
		t.Errorf("first call should be flagged unparseable (its parens never balance): %+v", calls[0])
	}
	if calls[1].unparseable {
		t.Errorf("second call is a real, balanced call and must not be flagged unparseable: %+v", calls[1])
	}
	if !strings.Contains(calls[1].args, "sits after the broken one") {
		t.Errorf("second call's args were not recovered correctly: %q", calls[1].args)
	}
}

// TestF160_05_MaskedNeedleCountMatchesScanCountAcrossPackage is this
// dispatch's acceptance criterion for [F160-04]/[F160-05] together (dispatch
// note: superseding the prior report's raw strings.Count-based criterion,
// which would leave tool_errors.go permanently red at 6-vs-4 — that compared
// against the WRONG baseline, one that still counted the two comment-only
// needles). For every non-test .go file in this package, the needle count
// AFTER masking out comments/strings must equal
// len(scanNewToolResultErrorCalls(src)) exactly: every real call site is
// found (no [F160-05] fail-open gap) and nothing else is counted (no
// [F160-04] false positive).
func TestF160_05_MaskedNeedleCountMatchesScanCountAcrossPackage(t *testing.T) {
	const needle = "NewToolResultError("
	files := goSourceFilesInPackageDir(t)
	for name, body := range files {
		masked := maskCommentsAndStrings(body)
		wantCount := 0
		searchFrom := 0
		for {
			rel := strings.Index(body[searchFrom:], needle)
			if rel < 0 {
				break
			}
			pos := searchFrom + rel
			if !masked[pos] {
				wantCount++
			}
			searchFrom = pos + len(needle)
		}
		gotCount := len(scanNewToolResultErrorCalls(body))
		if gotCount != wantCount {
			t.Errorf("%s: scanNewToolResultErrorCalls found %d call(s), want %d "+
				"(masked-needle count) — coverage gap or false positive", name, gotCount, wantCount)
		}
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
