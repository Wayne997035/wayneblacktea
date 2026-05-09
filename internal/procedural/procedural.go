// Package procedural implements the ProceduralMemory domain: first-class
// storage for "how-to" knowledge derived from past experience. Procedural
// memories capture reusable approaches, the tools they use, and the files
// they touch, with a success counter that tracks real-world usage.
package procedural

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested procedural memory does not exist.
var ErrNotFound = errors.New("procedural: not found")

// ProceduralMemory is the domain model for a procedural memory entry.
type ProceduralMemory struct {
	ID           uuid.UUID  `json:"id"`
	WorkspaceID  *uuid.UUID `json:"workspace_id,omitempty"`
	RepoName     string     `json:"repo_name,omitempty"`
	ProjectID    *uuid.UUID `json:"project_id,omitempty"`
	Title        string     `json:"title"`
	WhenToUse    string     `json:"when_to_use"`
	ApproachMD   string     `json:"approach_md"`
	ToolsUsed    []string   `json:"tools_used"`
	FilesTouched []string   `json:"files_touched"`
	SuccessCount int        `json:"success_count"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// AddParams holds parameters for creating a new procedural memory.
type AddParams struct {
	WorkspaceID  *uuid.UUID
	RepoName     string
	ProjectID    *uuid.UUID
	Title        string
	WhenToUse    string
	ApproachMD   string
	ToolsUsed    []string
	FilesTouched []string
}

// QueryFilter narrows a Query call.
type QueryFilter struct {
	WorkspaceID *uuid.UUID
	RepoName    string // empty = all repos
	Keywords    string // free-text search in title + when_to_use + approach_md
	Limit       int    // 0 → default 10
}
