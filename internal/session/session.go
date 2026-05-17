package session

import (
	"errors"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no session handoff exists.
var ErrNotFound = errors.New("session: not found")

// NextActionStatus represents the completion state of a next_action step.
type NextActionStatus string

const (
	NextActionPending NextActionStatus = "pending"
	NextActionDone    NextActionStatus = "done"
	NextActionSkipped NextActionStatus = "skipped"
)

// NextAction represents a single actionable step in a session handoff.
// Stored as a JSONB array in Postgres and a JSON TEXT array in SQLite.
type NextAction struct {
	Step      int              `json:"step"`
	Title     string           `json:"title"`
	Command   string           `json:"command,omitempty"`
	Expected  string           `json:"expected,omitempty"`
	Status    NextActionStatus `json:"status"`
	RefTaskID *string          `json:"ref_task_id,omitempty"` // UUID string or null
}

// HandoffParams holds parameters for recording a session handoff.
type HandoffParams struct {
	ProjectID      *uuid.UUID
	RepoName       string
	Intent         string
	ContextSummary string
	NextActions    []NextAction // optional; defaults to empty array
}
