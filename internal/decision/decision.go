package decision

import (
	"errors"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested decision does not exist.
var ErrNotFound = errors.New("decision: not found")

// LogParams holds parameters for recording a new architectural decision.
type LogParams struct {
	ProjectID *uuid.UUID
	// TaskID links this decision to a specific task (migration 000048).
	// No FK constraint (CLAUDE.md red-line §9); referential integrity in code.
	TaskID       *uuid.UUID
	RepoName     string
	Title        string
	Context      string
	Decision     string
	Rationale    string
	Alternatives string
}
