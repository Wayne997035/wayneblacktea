package mcp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// TestSanitizeAuditText verifies the audit-text sanitiser used in
// middleware_discipline.go. It must:
//   - strip ASCII control bytes (< 0x20) — including \r, \n, \x00, ANSI ESC
//   - preserve \t (tab is the documented exception)
//   - cap output at maxRunes (rune count, not byte count, for UTF-8 safety)
//   - pass plain ASCII through unchanged
//   - handle empty strings as a fast path
//
// Backed by backend-security-design.md §5.4 and CWE-117 (log/audit
// injection — control chars in stored audit text can break CLI rendering
// and forge new log lines).
func TestSanitizeAuditText(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		maxRunes int
		want     string
	}{
		{
			name:     "empty string passes through",
			in:       "",
			maxRunes: 128,
			want:     "",
		},
		{
			name:     "plain ASCII passes through unchanged",
			in:       "add_task",
			maxRunes: 128,
			want:     "add_task",
		},
		{
			name:     "plain UTF-8 with multi-byte runes passes through",
			in:       "中文_repo_名稱",
			maxRunes: 128,
			want:     "中文_repo_名稱",
		},
		{
			name:     "tab is preserved",
			in:       "tool\tname",
			maxRunes: 128,
			want:     "tool\tname",
		},
		{
			name:     "newline stripped",
			in:       "line1\nline2",
			maxRunes: 128,
			want:     "line1line2",
		},
		{
			name:     "carriage return stripped",
			in:       "line1\rline2",
			maxRunes: 128,
			want:     "line1line2",
		},
		{
			name:     "null byte stripped",
			in:       "before\x00after",
			maxRunes: 128,
			want:     "beforeafter",
		},
		{
			name:     "ANSI escape sequence stripped (ESC = 0x1b)",
			in:       "before\x1b[31mred\x1b[0mafter",
			maxRunes: 128,
			want:     "before[31mred[0mafter",
		},
		{
			name:     "all common controls stripped at once",
			in:       "a\x00b\x01c\x07d\x0be\x1bf",
			maxRunes: 128,
			want:     "abcdef",
		},
		{
			name:     "tool name >128 runes truncated to exactly 128",
			in:       strings.Repeat("a", 256),
			maxRunes: 128,
			want:     strings.Repeat("a", 128),
		},
		{
			name:     "exactly maxRunes is preserved",
			in:       strings.Repeat("a", 128),
			maxRunes: 128,
			want:     strings.Repeat("a", 128),
		},
		{
			name:     "multi-byte rune cap counts by rune, not byte",
			in:       strings.Repeat("漢", 200), // 600 bytes UTF-8
			maxRunes: 128,
			want:     strings.Repeat("漢", 128),
		},
		{
			name:     "control char + length cap together",
			in:       strings.Repeat("a", 200) + "\n" + strings.Repeat("b", 200),
			maxRunes: 128,
			want:     strings.Repeat("a", 128),
		},
		{
			name:     "repo_name 256-rune cap (control char + length)",
			in:       strings.Repeat("r", 300),
			maxRunes: 256,
			want:     strings.Repeat("r", 256),
		},
		{
			name:     "high-byte runes (≥0x80) preserved (not control chars)",
			in:       "café",
			maxRunes: 128,
			want:     "café",
		},
		{
			name:     "0x7f DEL is NOT stripped (only < 0x20)",
			in:       "before\x7fafter",
			maxRunes: 128,
			want:     "before\x7fafter",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeAuditText(tc.in, tc.maxRunes)
			if got != tc.want {
				t.Errorf("sanitizeAuditText(%q, %d):\n  got  %q\n  want %q", tc.in, tc.maxRunes, got, tc.want)
			}
		})
	}
}

// TestSanitizeAuditText_ConstantsMatchRequirements documents the
// caller-side constants used in middleware_discipline.go. If these change,
// audit log behaviour changes — callers below assert the tier so that any
// future tweak surfaces in the test diff.
func TestSanitizeAuditText_ConstantsMatchRequirements(t *testing.T) {
	if maxToolNameRunes != 128 {
		t.Errorf("maxToolNameRunes drift: want 128, got %d", maxToolNameRunes)
	}
	if maxRepoNameRunes != 256 {
		t.Errorf("maxRepoNameRunes drift: want 256, got %d", maxRepoNameRunes)
	}
}

// captureDisciplineStore is a discipline.Store stub that records every
// InsertParams passed to Insert, for assertions in the disciplineMiddleware
// tests below. RecentMutating / RecentDecisionTimes are unused here.
type captureDisciplineStore struct {
	mu       sync.Mutex
	inserted []discipline.InsertParams
}

func (s *captureDisciplineStore) Insert(_ context.Context, p discipline.InsertParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inserted = append(s.inserted, p)
	return nil
}

func (s *captureDisciplineStore) RecentMutating(_ context.Context, _ time.Time, _ int) ([]discipline.Event, error) {
	return nil, nil
}

func (s *captureDisciplineStore) RecentDecisionTimes(_ context.Context, _ string, _ time.Time) ([]time.Time, error) {
	return nil, nil
}

func (s *captureDisciplineStore) snapshot() []discipline.InsertParams {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]discipline.InsertParams, len(s.inserted))
	copy(out, s.inserted)
	return out
}

var _ discipline.Store = (*captureDisciplineStore)(nil)

// textResult builds a minimal *CallToolResult carrying one text content
// block, mirroring the shape real tool handlers return.
func textResult(text string, isError bool) *mcpmsg.CallToolResult {
	return &mcpmsg.CallToolResult{
		IsError: isError,
		Content: []mcpmsg.Content{mcpmsg.TextContent{Type: "text", Text: text}},
	}
}

// fireDiscipline invokes disciplineMiddleware once with `next`, then waits up
// to 1s for the background Insert to land (mirrors fireProposer's poll
// pattern in middleware_decision_proposer_test.go). Returns the middleware's
// own (res, err) plus the eventually-captured InsertParams.
func fireDiscipline(
	t *testing.T, srv *Server, tool string, next server.ToolHandlerFunc,
) (*mcpmsg.CallToolResult, error, []discipline.InsertParams) {
	t.Helper()
	mw := srv.disciplineMiddleware()
	handler := mw(next)
	req := mcpmsg.CallToolRequest{}
	req.Params.Name = tool

	res, err := handler(context.Background(), req)

	store, _ := srv.discipline.(*captureDisciplineStore)
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if got := store.snapshot(); len(got) > 0 {
			return res, err, got
		}
		time.Sleep(5 * time.Millisecond)
	}
	return res, err, store.snapshot()
}

// TestDisciplineMiddleware_RecordsOkAndSize covers the happy-path user
// journey (spec 1f4c7b7f, Acceptance criteria row 1): a successful call
// records ok=true, error_class empty (NULL), response_bytes>0,
// duration_ms>=0. Mutation-proof: hardcoding `ok := true` in
// middleware_discipline.go would not turn this test red by itself (it
// already expects ok=true here) — see TestDisciplineMiddleware_
// RecordsFailedCalls for the mutation that catches that specific bug.
func TestDisciplineMiddleware_RecordsOkAndSize(t *testing.T) {
	disc := &captureDisciplineStore{}
	srv := &Server{discipline: disc, sessionID: "test-session-1"}

	_, err, got := fireDiscipline(t, srv, testTool, func(_ context.Context, _ mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
		return textResult("ok", false), nil
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 inserted event, got %d", len(got))
	}
	p := got[0]
	if !p.Ok {
		t.Errorf("ok: want true, got false")
	}
	if p.ErrorClass != "" {
		t.Errorf("error_class: want empty (NULL), got %q", p.ErrorClass)
	}
	if p.ResponseBytes == nil || *p.ResponseBytes <= 0 {
		t.Errorf("response_bytes: want >0, got %+v", p.ResponseBytes)
	}
	if p.DurationMs < 0 {
		t.Errorf("duration_ms: want >=0, got %d", p.DurationMs)
	}
}

// TestDisciplineMiddleware_RecordsFailedCalls covers the tool-level-failure
// user journey (Acceptance criteria row 2): res.IsError=true, err=nil now
// records a row — previously (before this ticket) failed calls were never
// recorded at all. Mutation-proof: reverting the early-return removal in
// middleware_discipline.go (`if err != nil || res == nil || res.IsError {
// return res, err }` placed before the discipline.Insert call) makes this
// test time out waiting for an insert that never happens (len(got) == 0).
func TestDisciplineMiddleware_RecordsFailedCalls(t *testing.T) {
	disc := &captureDisciplineStore{}
	srv := &Server{discipline: disc, sessionID: "test-session-1"}

	_, err, got := fireDiscipline(t, srv, "create_project", func(_ context.Context, _ mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
		return textResult("project not found", true), nil
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 inserted event (failed calls must now be recorded), got %d", len(got))
	}
	p := got[0]
	if p.Ok {
		t.Errorf("ok: want false, got true")
	}
	if p.ErrorClass != discipline.ErrorClassInternal {
		t.Errorf("error_class: want %q, got %q", discipline.ErrorClassInternal, p.ErrorClass)
	}
	if p.ResponseBytes == nil || *p.ResponseBytes <= 0 {
		t.Errorf("response_bytes: want >0 (res != nil), got %+v", p.ResponseBytes)
	}
}

// TestDisciplineMiddleware_RecordsGoLevelError covers the Go-level-error
// user journey (Acceptance criteria row 3): handler returns (nil, err).
// response_bytes MUST be nil (genuinely unmeasured, not zero) — there is no
// res to marshal. Mutation-proof: unconditionally allocating responseBytes
// even when res == nil makes the final assertion below fail.
func TestDisciplineMiddleware_RecordsGoLevelError(t *testing.T) {
	disc := &captureDisciplineStore{}
	srv := &Server{discipline: disc, sessionID: "test-session-1"}

	handlerErr := errors.New("boom")
	_, err, got := fireDiscipline(t, srv, "record_outcome", func(_ context.Context, _ mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
		return nil, handlerErr
	})
	if !errors.Is(err, handlerErr) {
		t.Fatalf("handler err: want %v, got %v", handlerErr, err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 inserted event, got %d", len(got))
	}
	p := got[0]
	if p.Ok {
		t.Errorf("ok: want false, got true")
	}
	if p.ErrorClass != discipline.ErrorClassInternal {
		t.Errorf("error_class: want %q, got %q", discipline.ErrorClassInternal, p.ErrorClass)
	}
	if p.ResponseBytes != nil {
		t.Errorf("response_bytes: want nil (res == nil), got %d", *p.ResponseBytes)
	}
}

// TestDisciplineMiddleware_DurationUsesWallClockNotNowFn covers Acceptance
// criteria row 4: duration_ms reflects real elapsed time even when s.nowFn
// is overridden to jump far forward — the same test-seam pattern
// zz_sec_171_02_atomic_spend_test.go uses elsewhere in this package.
// Mutation-proof: swapping time.Now()/time.Since(start) for s.now() in
// middleware_discipline.go makes duration_ms read the jumped nowFn value
// (tens of hours in milliseconds), which the upper bound below catches.
func TestDisciplineMiddleware_DurationUsesWallClockNotNowFn(t *testing.T) {
	disc := &captureDisciplineStore{}
	srv := &Server{
		discipline: disc,
		sessionID:  "test-session-1",
		nowFn: func() time.Time {
			return time.Now().Add(48 * time.Hour)
		},
	}

	_, err, got := fireDiscipline(t, srv, testTool, func(_ context.Context, _ mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
		return textResult("ok", false), nil
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 inserted event, got %d", len(got))
	}
	durationMs := got[0].DurationMs
	if durationMs < 0 || durationMs > 5000 {
		t.Errorf("duration_ms = %d, want a small real-elapsed value (nowFn's 48h jump must not leak in)", durationMs)
	}
}

// TestDisciplineMiddleware_DoesNotMutateResult is the behavioral proof
// required by the "does not change the response shape" acceptance
// criterion: disciplineMiddleware returns next()'s *CallToolResult
// unmodified — same pointer, every field identical, including IsError and
// Content. A `git diff --stat` on tools_*.go cannot catch a mutation inside
// this middleware itself; this asserts the returned value directly instead.
// Mutation-proof: change the middleware to flip res.IsError (or overwrite
// res.Content) before its final `return res, err`, and this test goes red.
func TestDisciplineMiddleware_DoesNotMutateResult(t *testing.T) {
	disc := &captureDisciplineStore{}
	srv := &Server{discipline: disc, sessionID: "test-session-1"}

	want := textResult("preserved", false)
	wantPtr := want
	// Snapshot expected values into plain locals BEFORE the call. next()
	// returns `want` itself (same pointer the middleware then returns), so
	// comparing `got` against a live field read on `want` would trivially
	// "pass" even if the middleware mutates that shared object in place —
	// got.IsError and want.IsError would both read the post-mutation value.
	// These primitive copies are the only way the comparison below can
	// actually distinguish "unmodified" from "mutated in place".
	wantIsError := want.IsError
	wantContentLen := len(want.Content)
	wantText := want.Content[0].(mcpmsg.TextContent).Text

	mw := srv.disciplineMiddleware()
	handler := mw(func(_ context.Context, _ mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
		return want, nil
	})
	req := mcpmsg.CallToolRequest{}
	req.Params.Name = testTool
	got, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if got != wantPtr {
		t.Fatalf("disciplineMiddleware returned a different *CallToolResult pointer than next() produced")
	}
	if got.IsError != wantIsError {
		t.Errorf("IsError mutated: got %v, want %v", got.IsError, wantIsError)
	}
	if len(got.Content) != wantContentLen {
		t.Fatalf("Content length mutated: got %d, want %d", len(got.Content), wantContentLen)
	}
	gotText, ok := got.Content[0].(mcpmsg.TextContent)
	if !ok {
		t.Fatalf("Content[0] type changed: %T", got.Content[0])
	}
	if gotText.Text != wantText {
		t.Errorf("Content[0].Text mutated: got %q, want %q", gotText.Text, wantText)
	}
}
