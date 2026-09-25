package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// disciplineRecordTimeout caps the background discipline_events write so a
// stalled DB cannot leak a goroutine forever. The middleware itself returns
// to the caller before the goroutine even starts, so this only bounds
// resource use, not user-facing latency.
const disciplineRecordTimeout = 10 * time.Second

// Audit-text caps for fields persisted into discipline_events. These bound
// LLM-influenced strings (tool name + repo_name argument) before the row hits
// the DB (control-char strip + length cap; see CWE-117, log/audit injection).
const (
	maxToolNameRunes = 128
	maxRepoNameRunes = 256
)

// sanitizeAuditText strips ASCII control bytes (< 0x20 except \t) and caps
// the result at maxRunes runes. Any value with multi-byte runes counts by
// rune (so a 256-rune cap is at most ~1 KB on UTF-8). Empty input returns
// empty output — the audit-text sanitisation rule applied package-wide.
//
// Notes:
//   - \x1b (ANSI ESC) is < 0x20 and therefore stripped, so terminal escape
//     sequences are removed in addition to \r / \n / \x00.
//   - \t is preserved deliberately — repo paths and tool args occasionally
//     legitimately contain tabs; they don't break log/CLI display.
func sanitizeAuditText(s string, maxRunes int) string {
	if s == "" {
		return ""
	}
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return -1
		}
		return r
	}, s)
	if utf8.RuneCountInString(cleaned) > maxRunes {
		runes := []rune(cleaned)
		cleaned = string(runes[:maxRunes])
	}
	return cleaned
}

// disciplineMiddleware wraps every tool handler and records a
// discipline_events row for every call — success or failure. The write
// happens in a background goroutine using context.Background() so a
// request-context cancellation cannot drop the audit row mid-flight.
//
// [F184-05] Prior to this ticket, a failed call (err != nil, or
// res.IsError == true) was never recorded at all — genuinely absent from
// the table, not "recorded with a failure flag". That early return is
// removed: every call now yields one row carrying ok/error_class/
// response_bytes/duration_ms, so discipline_events can answer "which tool,
// how often does it fail, how large is each response" (spec 1f4c7b7f).
//
// Errors writing the event MUST NOT fail the tool call — they are logged via
// slog.Warn and we move on (observability sinks must never break the
// user-facing path).
func (s *Server) disciplineMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
			start := time.Now()
			res, err := next(ctx, req)

			if s.discipline == nil {
				return res, err
			}

			// [F184-05] ok / error_class / response_bytes / duration_ms are
			// computed synchronously HERE — before the goroutine launch
			// below, and before `res` is returned to the caller — matching
			// the existing pattern (toolName/repoName/sessionID are also
			// captured before the ctx switch) and avoiding a data race
			// reading res concurrently with whatever mcp-go does with the
			// same *CallToolResult after this middleware returns it. None of
			// this reads or mutates res/err; only the return statement at
			// the bottom of this closure does, unchanged.
			ok := err == nil && res != nil && !res.IsError

			// [F184-05] error_class: 2-way split only this ticket (decision
			// D-05). Every failure — Go-level
			// err != nil or res.IsError — classifies "internal"; empty
			// string (NULL) when ok. See discipline.ErrorClass's doc for why
			// a finer split isn't implemented here.
			var errClass discipline.ErrorClass
			if !ok {
				errClass = discipline.ErrorClassInternal
			}

			// [F184-05] response_bytes: nil when res == nil (nothing to
			// measure — genuinely unmeasured, not zero). Otherwise
			// len(json.Marshal(res)), a well-defined proxy for wire size
			// (the CallToolResult struct's own JSON encoding; not
			// byte-identical to the outer JSON-RPC envelope, which this
			// middleware has no access to).
			var responseBytes *int
			if res != nil {
				if b, marshalErr := json.Marshal(res); marshalErr == nil {
					n := len(b)
					responseBytes = &n
				} else {
					slog.Warn("disciplineMiddleware: failed to measure response size", "error", marshalErr)
				}
			}

			// [F184-05] duration_ms: real wall-clock only (time.Since),
			// never s.now()/s.nowFn — that seam is overridden elsewhere in
			// this package (zz_sec_171_02_atomic_spend_test.go) to jump or
			// interleave time, which would corrupt a genuine latency
			// measurement.
			durationMs := int(time.Since(start).Milliseconds())

			// LLM-supplied tool name + repo_name arg flow into the DB
			// audit row; sanitise both BEFORE persist (see CWE-117; control
			// chars must be stripped and length capped). The original `tool` value
			// (still available via req.Params.Name) drives behaviour like
			// IsMutating; the sanitised `toolName` is what we persist.
			rawTool := req.Params.Name
			args := req.GetArguments()

			toolName := sanitizeAuditText(rawTool, maxToolNameRunes)
			// [F0925-29] This row is written before the tool validates its
			// own arguments, so a repo_name breaking the workspace repo name
			// rule is audited as empty rather than persisted.
			repoName := sanitizeAuditText(validator.RepoNameOrEmpty(stringArg(args, "repo_name")), maxRepoNameRunes)
			// Captured from the request ctx BEFORE the goroutine below
			// switches to context.Background() — server.ClientSessionFromContext
			// only resolves off the live request context (U15).
			sessionID := s.auditSessionID(ctx)

			params := discipline.InsertParams{
				SessionID:     sessionID,
				RepoName:      repoName,
				ToolName:      toolName,
				IsMutating:    discipline.IsMutating(rawTool),
				WorkspaceID:   s.workspaceID,
				Ok:            ok,
				ErrorClass:    errClass,
				ResponseBytes: responseBytes,
				DurationMs:    durationMs,
			}

			// Background goroutine so the audit write cannot block / fail the
			// tool response. context.Background() with a fresh timeout — never
			// inherit the request ctx, which is about to be cancelled.
			//nolint:gosec // G118: intentional — goroutine must outlive request ctx so the audit row survives
			go func() {
				defer func() {
					if r := recover(); r != nil {
						slog.Warn(
							"disciplineMiddleware: panic in background goroutine",
							"tool", toolName,
							"panic", fmt.Sprintf("%v", r),
						)
					}
				}()
				bgCtx, cancel := context.WithTimeout(context.Background(), disciplineRecordTimeout)
				defer cancel()
				if insertErr := s.discipline.Insert(bgCtx, params); insertErr != nil {
					slog.Warn(
						"disciplineMiddleware: failed to record event",
						"tool", toolName,
						"session_id", sessionID,
						"is_mutating", params.IsMutating,
						"ok", params.Ok,
						"error", insertErr,
					)
				}
			}()

			return res, err
		}
	}
}
