package mcp

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
)

// taskIDPrefixLimit caps how many candidate rows resolveTaskID asks the
// store for. 3 is enough to tell "exactly one match" apart from "ambiguous"
// while keeping the ambiguous-match error message short (D3).
const taskIDPrefixLimit = 3

// errMsgTaskNotFound is resolveTaskID's 0-candidate message, shared with its
// own tests (task_id_resolver_test.go, task_id_manual_resolver_test.go) so
// the literal has exactly one source of truth (goconst).
const errMsgTaskNotFound = "task not found"

// taskIDPrefixRe is D3's locked regex for a resolvable task_id prefix:
// lowercase hex, 8+ chars, anchored. Uppercase is deliberately NOT
// normalized — an uppercase-hex string falls through to the invalid-UUID
// case below, per D3's own locked decision, not a gap left by this file.
var taskIDPrefixRe = regexp.MustCompile(`^[0-9a-f]{8,}$`)

// resolveTaskID resolves raw into a full task UUID, workspace-scoped
// [F0930-17][F0930-18][F0930-19]:
//
//  1. raw already parses as a UUID (including the 32-char dashless form,
//     google/uuid's own case-32 branch) → return it unchanged. A 32-char
//     lowercase-hex string therefore never reaches step 2, even though it
//     also matches taskIDPrefixRe.
//  2. raw matches taskIDPrefixRe → s.gtd.FindTaskIDsByPrefix(ctx, raw, 3):
//     - 0 rows → "task not found"
//     - 1 row → its id
//     - >=2 rows → an error listing every candidate's id + clipSafe'd title
//     (U13 — never an untruncated caller-supplied title), so the caller can
//     retry with a longer prefix; no write happens either way.
//  3. otherwise (7 chars, non-hex, or malformed some other way) →
//     errMsgInvalidTaskIDUUID, byte-identical to the message a malformed
//     task_id produced before this feature existed.
func (s *Server) resolveTaskID(ctx context.Context, raw string) (uuid.UUID, *mcp.CallToolResult) {
	if id, err := uuid.Parse(raw); err == nil {
		return id, nil
	}
	if !taskIDPrefixRe.MatchString(raw) {
		return uuid.UUID{}, mcp.NewToolResultError(errMsgInvalidTaskIDUUID)
	}
	candidates, err := s.gtd.FindTaskIDsByPrefix(ctx, raw, taskIDPrefixLimit)
	if err != nil {
		return uuid.UUID{}, storeErrorResult("resolving task_id prefix", err)
	}
	switch len(candidates) {
	case 0:
		return uuid.UUID{}, mcp.NewToolResultError(errMsgTaskNotFound)
	case 1:
		return candidates[0].ID, nil
	default:
		return uuid.UUID{}, mcp.NewToolResultError(ambiguousTaskIDPrefixMessage(candidates))
	}
}

// resolveTaskIDForEntityID resolves rawEntityID as a task_id-shaped value for
// record_outcome's entity_id argument (entity_type=="task" only, F0930-19).
// Wraps resolveTaskID and remaps only the malformed-input branch's message:
// resolveTaskID's own errMsgInvalidTaskIDUUID names an argument ("task_id")
// record_outcome does not have — its argument is "entity_id". The not-found
// and ambiguous branches are returned unchanged: those describe the
// underlying task lookup, not the argument's name, and remapping the
// ambiguous branch would lose its candidate list.
func (s *Server) resolveTaskIDForEntityID(ctx context.Context, rawEntityID string) (uuid.UUID, *mcp.CallToolResult) {
	id, errResult := s.resolveTaskID(ctx, rawEntityID)
	if errResult == nil {
		return id, nil
	}
	if _, err := uuid.Parse(rawEntityID); err != nil && !taskIDPrefixRe.MatchString(rawEntityID) {
		return uuid.UUID{}, mcp.NewToolResultError("invalid entity_id UUID")
	}
	return id, errResult
}

// resolveTaskIDForRefTaskID resolves raw as a task_id-shaped value for
// set_session_handoff's next_actions[].ref_task_id field (F0930-19). Wraps
// resolveTaskID and remaps only the malformed-input branch's message to
// "ref_task_id must be a valid UUID" — the exact message this field
// produced before this feature existed (a plain uuid.Parse failure);
// preserving it keeps existing callers' error-text matching intact. The
// not-found and ambiguous branches are returned unchanged, same rationale as
// resolveTaskIDForEntityID above.
func (s *Server) resolveTaskIDForRefTaskID(ctx context.Context, raw string) (uuid.UUID, *mcp.CallToolResult) {
	id, errResult := s.resolveTaskID(ctx, raw)
	if errResult == nil {
		return id, nil
	}
	if _, err := uuid.Parse(raw); err != nil && !taskIDPrefixRe.MatchString(raw) {
		return uuid.UUID{}, mcp.NewToolResultError("ref_task_id must be a valid UUID")
	}
	return id, errResult
}

// ambiguousTaskIDPrefixMessage renders the >=2-candidate error text: every
// candidate's id plus its clipSafe'd title (U13 — never echo an untruncated
// caller-supplied title back to the caller). D3's "never log title text on
// this path" requirement is about slog output, not this caller-facing
// response — resolveTaskID above never logs at all, so that requirement is
// satisfied by omission rather than needing a redaction step here.
func ambiguousTaskIDPrefixMessage(candidates []gtd.TaskIDTitle) string {
	var b strings.Builder
	b.WriteString("task_id prefix is ambiguous, matches multiple tasks: ")
	for i, c := range candidates {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s (%q)", c.ID, clipSafe(c.Title, gtdTitleMaxRunes))
	}
	b.WriteString(" — retry with a longer prefix or the full UUID")
	return b.String()
}
