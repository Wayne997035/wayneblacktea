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

// rawErrorInResult matches a NewToolResultError call whose argument
// interpolates an error value. The three shapes cover everything this package
// used before U14:
//
//	NewToolResultError(fmt.Sprintf("...: %v", err))
//	NewToolResultError(err.Error())
//	NewToolResultError("prefix: " + err.Error())
//
// The scan is textual on purpose. A type-aware version would need to resolve
// what each identifier refers to, and the property being enforced here is a
// syntactic one — "no error value appears inside this constructor's argument"
// — which is exactly what a reviewer checks by eye and exactly what stops
// being checked once the file is 1,600 lines long.
var rawErrorInResult = regexp.MustCompile(`NewToolResultError\([^)]*\b(?:\w*[eE]rr)\b`)

// TestNoRawErrorReachesToolResult is U14's structural gate.
//
// Before the sweep this matched 125 call sites across 24 files. It is the test
// that makes the fix stick: the leak was never one bug, it was the absence of
// a shared exit, so every new handler written the obvious way reintroduced it.
// With this test the obvious way is red and the two helpers in tool_errors.go
// are the only way through.
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
		for i, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if rawErrorInResult.MatchString(line) {
				violations = append(violations, name+":"+itoa(i+1)+"  "+strings.TrimSpace(line))
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
