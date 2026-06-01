// Package atom implements the MemoryAtom domain: atomic fact units extracted
// from decisions, knowledge items, and procedural memories. Atoms form a
// directed graph via memory_links, enabling graph traversal queries.
package atom

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested atom does not exist.
var ErrNotFound = errors.New("atom: not found")

// Atom is the domain model for an atomic fact unit.
type Atom struct {
	ID           uuid.UUID  `json:"id"`
	WorkspaceID  *uuid.UUID `json:"workspace_id,omitempty"`
	ParentTable  string     `json:"parent_table"`
	ParentID     uuid.UUID  `json:"parent_id"`
	Content      string     `json:"content"`
	Keywords     []string   `json:"keywords"`
	Tags         []string   `json:"tags"`
	CreatedAt    time.Time  `json:"created_at"`
	DigestStatus *string    `json:"digest_status,omitempty"`
}

// Link is a directed edge between two atoms.
type Link struct {
	FromAtomID uuid.UUID `json:"from_atom_id"`
	ToAtomID   uuid.UUID `json:"to_atom_id"`
	LinkType   string    `json:"link_type"` // same_entity|same_action|same_time|same_project
	Confidence float64   `json:"confidence"`
	CreatedAt  time.Time `json:"created_at"`
}

// AddAtomParams holds parameters for creating a new atom.
type AddAtomParams struct {
	WorkspaceID *uuid.UUID
	ParentTable string
	ParentID    uuid.UUID
	Content     string
	Keywords    []string
	Tags        []string
}

// AddLinkParams holds parameters for creating a directed link between two atoms.
type AddLinkParams struct {
	FromAtomID uuid.UUID
	ToAtomID   uuid.UUID
	LinkType   string
	Confidence float64
}

// TraverseResult contains atoms and links visited during a BFS traversal.
type TraverseResult struct {
	Atoms []Atom `json:"atoms"`
	Links []Link `json:"links"`
}
