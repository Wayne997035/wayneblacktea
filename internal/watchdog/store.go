package watchdog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// EventType enumerates the 8 self-monitoring detection categories.
type EventType string

const (
	EventTypeStuckTask            EventType = "stuck_task"
	EventTypeUnloggedDecision     EventType = "unlogged_decision"
	EventTypePlanNoTask           EventType = "plan_no_task"
	EventTypeProposalFail         EventType = "proposal_fail"
	EventTypeStaleHandoff         EventType = "stale_handoff"
	EventTypeRepeatedCorrection   EventType = "repeated_correction"
	EventTypeTaskNoOutcome        EventType = "task_no_outcome"
	EventTypeDecisionNoReflection EventType = "decision_no_reflection"
)

// Severity is the urgency level for a discipline event.
type Severity string

const (
	SeverityInfo  Severity = "info"
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
)

// DisciplineEvent is the domain model for a single watchdog meta-cognition event.
type DisciplineEvent struct {
	ID          uuid.UUID       `json:"id"`
	WorkspaceID *uuid.UUID      `json:"workspace_id,omitempty"`
	EventType   EventType       `json:"event_type"`
	Severity    Severity        `json:"severity"`
	Detail      json.RawMessage `json:"detail"`
	ResolvedAt  *time.Time      `json:"resolved_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// InsertParams holds the fields needed to create a new discipline_events_m8 row.
type InsertParams struct {
	WorkspaceID *uuid.UUID
	EventType   EventType
	Severity    Severity
	Detail      json.RawMessage // nil becomes JSON null
}

// DisciplineEventStoreIface is the backend-agnostic contract for the
// discipline_events_m8 table. Both PgDisciplineEventStore and the SQLite
// implementation satisfy this interface.
type DisciplineEventStoreIface interface {
	// Insert records a single watchdog detection event.
	Insert(ctx context.Context, p InsertParams) error

	// ListUnresolved returns all open events (resolved_at IS NULL) for the
	// given workspace_id (nil = all workspaces). Ordered by created_at DESC.
	ListUnresolved(ctx context.Context, wsID *uuid.UUID) ([]DisciplineEvent, error)

	// MarkResolved sets resolved_at = NOW() for the given event ID.
	// Returns ErrEventNotFound when no matching row exists.
	MarkResolved(ctx context.Context, eventID uuid.UUID) error

	// PruneOlderThan hard-deletes rows with created_at < cutoff.
	// Returns the number of rows deleted.
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// ErrEventNotFound is returned by MarkResolved when the event ID does not
// exist in the store.
var ErrEventNotFound = errEventNotFound{}

type errEventNotFound struct{}

func (errEventNotFound) Error() string { return "watchdog: discipline event not found" }

// NullDetail returns a JSON null if detail is nil or empty.
// Exported so that the SQLite store (in internal/storage/sqlite) can use it.
func NullDetail(d json.RawMessage) json.RawMessage {
	if len(d) == 0 {
		return json.RawMessage("null")
	}
	return d
}
