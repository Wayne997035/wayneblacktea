// Package proposal manages pending agent-originated entities awaiting user
// confirmation before they become real (goals, projects, tasks, concepts).
package proposal

import (
	"errors"

	"github.com/google/uuid"
)

// Type enumerates the entity classes that an agent may propose.
type Type string

const (
	TypeGoal      Type = "goal"
	TypeProject   Type = "project"
	TypeTask      Type = "task"
	TypeConcept   Type = "concept"
	TypeKnowledge Type = "knowledge"
	TypePlaybook  Type = "playbook"
	// TypeDecision is created by the auto-decision-proposer middleware
	// (internal/mcp/middleware_decision_proposer.go) when a mutating MCP
	// tool fires without a recent log_decision/confirm_plan in the same
	// session. Payload shape: {title, decision, rationale, alternatives,
	// session_id, trigger_tool}. confirm_proposal materialises an accepted
	// row into the `decisions` table.
	TypeDecision Type = "decision"
)

// Status is the lifecycle of a proposal record.
type Status string

const (
	StatusPending  Status = "pending"
	StatusAccepted Status = "accepted"
	StatusRejected Status = "rejected"
)

// ErrNotFound is returned when no matching pending proposal exists. A proposal
// already resolved (accepted/rejected) also returns ErrNotFound on Resolve to
// keep the operation idempotent.
var ErrNotFound = errors.New("proposal: not found")

// ErrPayloadTooLarge is returned by Store.Create when CreateParams.Payload
// exceeds MaxPayloadBytes (store.go) — [F981-05]. Every caller of Create,
// MCP tool handler or otherwise, is protected by this single check at the
// write path, before any DB call.
var ErrPayloadTooLarge = errors.New("proposal: payload too large")

// CreateParams captures the fields required to record a new proposal.
type CreateParams struct {
	WorkspaceID *uuid.UUID // nil → unscoped (Phase B1: always nil; B2 wires real workspace)
	Type        Type
	Payload     []byte // JSON-encoded proposal body (entity-specific shape)
	ProposedBy  string // empty → NULL; e.g. "claude-code", "discord-bot"
}

// BatchItemResult reports the outcome of a single ID inside a BatchConfirm call.
type BatchItemResult struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	ErrMsg string `json:"error,omitempty"`
}

// BatchItemErrMsg is what BatchConfirm must put in BatchItemResult.ErrMsg.
//
// [GTD 1134a216] Both backends used to write err.Error() straight into that
// field, and it reaches the caller through jsonText — not through the MCP
// error path — so neither the tool-error redaction nor U14's needle ever saw
// it. The measured SQLite text was "sqlite ResolveProposal: SQL logic error:
// no such table: pending_proposals (1)"; the Postgres equivalent carries
// host, port, database, user and SQLSTATE.
//
// F170-08 fixed the sibling accept branch by routing it through
// storeErrorText. That helper lives in internal/mcp and takes an error, while
// this string is produced down in the store layer where the error identity is
// still available and the MCP layer only ever sees the flattened text — so
// the classification has to happen here, and living beside the sentinel it
// classifies is what keeps the two backends from drifting.
//
// ErrNotFound passes through because it is the caller's own answer: the id
// they supplied is not a pending proposal (Resolve returns it for
// already-resolved rows too, keeping the operation idempotent). Everything
// else is an internal failure the caller cannot act on and must not see.
// Callers that need the detail have the server log.
//
// It takes no id on purpose. BatchItemResult.ID already carries it in the same
// object, so repeating it would spend tokens an agent pays per failed item —
// and an id parameter is one more thing a caller can pass the wrong value for,
// in a loop where the right one is not the only one in scope.
func BatchItemErrMsg(err error) string {
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound.Error()
	}
	return "resolve failed"
}

// BatchConfirmResult is the aggregate result returned by BatchConfirm.
type BatchConfirmResult struct {
	Results []BatchItemResult `json:"results"`
	// Accepted is the count of proposals successfully resolved to the requested action.
	Accepted int `json:"accepted"`
	// Failed is the count of proposals that could not be resolved (not found,
	// already resolved, or other error). On the Postgres path a single failure
	// triggers a full rollback and all entries become failed.
	Failed int `json:"failed"`
}
