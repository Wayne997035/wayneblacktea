package session

import (
	"errors"

	"github.com/google/uuid"
)

var (
	// ErrNotFound is returned when no session handoff exists.
	ErrNotFound = errors.New("session: not found")
)

// HandoffParams holds parameters for recording a session handoff.
type HandoffParams struct {
	ProjectID      *uuid.UUID
	RepoName       string
	Intent         string
	ContextSummary string
}
