package mcp

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/mark3labs/mcp-go/mcp"
)

// Error-message hygiene for the MCP tool surface — U14
// (.specs/2026-08-20-mcp-surface-spec.md, root cause F15).
//
// Every tool handler that fails has to say something to its caller, and the
// caller is an LLM whose context is, in practice, readable by whoever is
// talking to it. Before this file the whole surface answered with
// fmt.Sprintf("<what I was doing>: %v", err), which hands that caller the
// verbatim error from whatever failed — pgx wire messages naming tables,
// columns and constraints, SQLite file paths, driver-level connection details,
// "context deadline exceeded" on a specific query. None of it is actionable
// for the caller and all of it describes how the server is built.
//
// The split below IS the design, and choosing between the two is a judgement
// the author makes at every call site — which is the point. A single
// "sanitise everything" helper would have quietly destroyed the validation
// messages that are the only useful thing a caller gets when it sends a
// malformed argument, and those messages are not a leak: they describe what
// the caller itself just sent.
//
//   - storeErrorResult — the failure came from inside the server (a store, a
//     transaction, a snapshot build). Full error to the server log, operation
//     name only to the client.
//   - inputErrorResult — the failure came from validating the caller's own
//     argument. Echoed back verbatim, because it is about that argument.
//
// What the gate actually guarantees, precisely — TestNoRawErrorReachesToolResult
// (tool_errors_test.go) does NOT enforce "every error goes through these two
// helpers". It enforces a narrower, purely syntactic property: no
// mcp.NewToolResultError(...) call anywhere in this package (outside this
// file) may contain an error-shaped identifier in its argument list. That
// closes exactly the failure mode this file exists for — a handler
// interpolating err/someErr/xErr straight into a NewToolResultError call —
// and nothing else.
//
// It is NOT structural in the stronger sense the previous version of this
// comment claimed ("the two helpers are the only way through"). Both
// storeErrorResult and inputErrorResult are themselves thin wrappers around
// mcp.NewToolResultError — nothing in the type system stops a new handler
// from calling mcp.NewToolResultError directly with a STATIC string (no err
// identifier in sight) that nonetheless leaks something else: a hardcoded
// snippet of internal state, a value assembled without going through either
// helper, or simply the wrong message for the failure. Of the ~266 direct
// mcp.NewToolResultError( call sites in this package as of this dispatch,
// only the 4 inside this file are provably routed through the helpers by
// construction — the other ~262 pass this gate today because none of them
// currently happens to interpolate an err-shaped identifier, which is a
// property of the CURRENT code, not a guarantee the gate imposes on future
// code. A handler that builds its error text some other way (string
// concatenation from a non-error variable, a field pulled from a struct that
// happens to hold driver text, etc.) would not trip this test at all.
//
// Closing that fully — routing every call site through the helpers so the
// choice is structurally forced rather than incidentally true — is call-site
// surgery across ~262 locations and is explicitly out of this dispatch's
// scope (R2 dispatch, U13/U14 gate-hardening round); it is the natural next
// step if this file's guarantee needs to be strengthened rather than just
// accurately described.

// storeErrorResult reports a server-side failure to an MCP client without
// disclosing what actually broke.
//
// op is a short present-participle description of what the server was doing
// ("loading projects", "creating evaluation") — the same text that used to sit
// before the colon in the old fmt.Sprintf. The client sees "<op> failed".
//
// The full error is not dropped: it goes to the server log at Error level with
// op as a field, so diagnosing a production failure still needs only the log,
// which is where it belonged in the first place. Redaction is client-facing
// only — TestStoreErrorResult_LogRetainsFullError pins that, because a
// "hygiene" change that also blinded the operator would be a worse bug than
// the leak it fixed.
func storeErrorResult(op string, err error) *mcp.CallToolResult {
	logToolError(op, err)
	for _, sentinel := range callerFacingSentinels {
		if errors.Is(err, sentinel) {
			// The SENTINEL's own text, not err's. err is the wrapped chain
			// ("beginning task <uuid>: assignee is required …: sqlite: …") and
			// echoing it would put back exactly what this function exists to
			// remove; the sentinel's message is the declared, reviewed,
			// caller-facing half.
			return mcp.NewToolResultError(op + ": " + sentinel.Error())
		}
	}
	return mcp.NewToolResultError(op + " failed")
}

// callerFacingSentinels are domain errors that describe the CALLER's request
// rather than the server's internals, and therefore must survive
// storeErrorResult's redaction.
//
// They exist because validation does not all happen before the store call. A
// tool argument like assignee or repo_name is checked against workspace state
// the handler does not hold, so the rejection comes back OUT of the store,
// mixed into the same error channel as connection failures. Redacting by
// call-site position — "it came from a store, suppress it" — therefore
// suppresses real user-facing validation too: the U14 sweep did exactly that,
// and TestMCPBeginTask_RequiresAssignee went red because the caller was told
// only "beginning task failed" when the actual answer was "you must set an
// assignee".
//
// Membership is deliberately narrow. Each entry is a sentinel whose message
// is a fixed, reviewed string naming a caller argument and its rule, with no
// interpolated server state — internal/handler/gtd_handler.go:231 already
// treats ErrInvalidRepoName as a 400-class caller error, which is the same
// judgement reached independently on the HTTP surface. An ErrNotFound-style
// sentinel does NOT belong here: those already have explicit errors.Is
// branches at the call sites that care, which produce better messages than a
// generic pass-through would.
var callerFacingSentinels = []error{
	gtd.ErrInvalidAssignee,
	gtd.ErrAssigneeRequiredForInProgress,
	gtd.ErrInvalidRepoName,
}

// logToolError writes the full error to the server log in the one shape every
// tool failure uses, so a log query for these does not have to know which
// helper produced the entry.
//
// It exists separately from storeErrorResult for the handlers whose failure
// response is not just an op name — confirm_plan reports which tasks and
// decisions were created BEFORE the failure, and that listing is the whole
// value of its error path. Such a handler composes its own text and calls
// this to make sure the error itself is still recorded rather than dropped.
func logToolError(op string, err error) {
	slog.Error("mcp tool operation failed", "op", op, "err", err)
}

// storeErrorResultf is storeErrorResult with a formatted op, for the call
// sites that name the entity they were working on ("listing children of
// <uuid>").
//
// The format arguments are the caller's OWN just-supplied arguments echoed
// back in the same turn, so they carry no stored content and need no boundary
// treatment (U13's same-turn-echo exemption). err is never part of the
// formatted text — it is passed separately and only reaches the log.
func storeErrorResultf(err error, format string, args ...any) *mcp.CallToolResult {
	return storeErrorResult(fmt.Sprintf(format, args...), err)
}

// inputErrorResult reports that the caller's own argument failed validation.
//
// err is echoed verbatim on purpose. These errors come from uuid.Parse,
// json.Unmarshal of a caller-supplied blob, and the validator/sanitize
// packages; they describe the shape of the value the caller just sent, which
// the caller already holds. Suppressing them would leave an LLM to guess which
// of its arguments was wrong — the failure mode U14 must not create while
// closing F15.
//
// field names the argument at fault ("notes", "metrics_json"); pass "" when
// the error text already identifies it.
func inputErrorResult(field string, err error) *mcp.CallToolResult {
	if field == "" {
		return mcp.NewToolResultError(err.Error())
	}
	return mcp.NewToolResultError(field + ": " + err.Error())
}

// inputErrorResultf is inputErrorResult with a formatted field description,
// for the validators that report which of several arguments failed ("invalid
// completed_task_ids UUID"). Same contract as inputErrorResult: err is echoed,
// because it describes the caller's own value.
func inputErrorResultf(err error, format string, args ...any) *mcp.CallToolResult {
	return inputErrorResult(fmt.Sprintf(format, args...), err)
}
