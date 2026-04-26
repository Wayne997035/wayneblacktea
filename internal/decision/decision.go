package decision

import (
	"errors"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested decision does not exist.
var ErrNotFound = errors.New("decision: not found")

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
