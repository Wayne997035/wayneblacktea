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

// CreateParams captures the fields required to record a new proposal.
type CreateParams struct {
	WorkspaceID *uuid.UUID // nil → unscoped (Phase B1: always nil; B2 wires real workspace)
	Type        Type
	Payload     []byte // JSON-encoded proposal body (entity-specific shape)
	ProposedBy  string // empty → NULL; e.g. "claude-code", "discord-bot"
}
