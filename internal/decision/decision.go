package decision

import (
	"errors"

	"github.com/google/uuid"
)

var (
	// ErrNotFound is returned when a requested decision does not exist.
	ErrNotFound = errors.New("decision: not found")
)

// LogParams holds parameters for recording a new architectural decision.
type LogParams struct {
	ProjectID    *uuid.UUID
	RepoName     string
	Title        string
	Context      string
	Decision     string
	Rationale    string
	Alternatives string
}
