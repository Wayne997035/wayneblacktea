// Package vision implements the Vision domain: first-class storage for
// "future ideas that can't be acted on now". Vision items capture work that
// is conceptually important but currently blocked by dependencies, resources,
// or timing. They can be promoted to GTD tasks when conditions change.
package vision

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// VisionStatus represents the lifecycle of a vision item.
type VisionStatus string

const (
	VisionStatusOpen       VisionStatus = "open"
	VisionStatusDiscussing VisionStatus = "discussing"
	VisionStatusMaturing   VisionStatus = "maturing"
	VisionStatusPromoted   VisionStatus = "promoted"
	VisionStatusDismissed  VisionStatus = "dismissed"
)

// ErrNotFound is returned when a requested vision item does not exist.
var ErrNotFound = errors.New("vision: not found")

// VisionItem is the domain model for a vision item.
type VisionItem struct {
	ID               uuid.UUID    `json:"id"`
	WorkspaceID      *uuid.UUID   `json:"workspace_id,omitempty"`
	RepoName         string       `json:"repo_name,omitempty"`
	ProjectID        *uuid.UUID   `json:"project_id,omitempty"`
	Title            string       `json:"title"`
	WhyBlocked       string       `json:"why_blocked"`
	DependsOn        []string     `json:"depends_on"`
	ParentInitiative string       `json:"parent_initiative,omitempty"`
	Status           VisionStatus `json:"status"`
	ContextMD        string       `json:"context_md,omitempty"`
	PromotedTaskID   *uuid.UUID   `json:"promoted_task_id,omitempty"`
	LastDiscussedAt  *time.Time   `json:"last_discussed_at,omitempty"`
	CreatedAt        time.Time    `json:"created_at"`
}

// VisionItemSummary is a compact projection of VisionItem that omits context_md
// for list endpoints (reduces response size for frequent polling).
type VisionItemSummary struct {
	ID               uuid.UUID    `json:"id"`
	WorkspaceID      *uuid.UUID   `json:"workspace_id,omitempty"`
	RepoName         string       `json:"repo_name,omitempty"`
	ProjectID        *uuid.UUID   `json:"project_id,omitempty"`
	Title            string       `json:"title"`
	WhyBlocked       string       `json:"why_blocked"`
	DependsOn        []string     `json:"depends_on"`
	ParentInitiative string       `json:"parent_initiative,omitempty"`
	Status           VisionStatus `json:"status"`
	PromotedTaskID   *uuid.UUID   `json:"promoted_task_id,omitempty"`
	LastDiscussedAt  *time.Time   `json:"last_discussed_at,omitempty"`
	CreatedAt        time.Time    `json:"created_at"`
}

// AddVisionParams holds parameters for creating a new vision item.
type AddVisionParams struct {
	WorkspaceID      *uuid.UUID
	RepoName         string
	ProjectID        *uuid.UUID
	Title            string
	WhyBlocked       string
	DependsOn        []string // nil/empty → []
	ParentInitiative string   // empty → NULL
	ContextMD        string   // empty → NULL
}

// UpdateVisionParams holds parameters for updating a vision item.
// Zero values are ignored (nil pointer = no-op for optional fields).
type UpdateVisionParams struct {
	Status          *VisionStatus
	ContextMD       *string
	LastDiscussedAt *time.Time
}

// PromoteParams holds parameters for promoting a vision item to a GTD task.
// The caller is responsible for actually creating the GTD task and passing
// back the resulting task ID.
type PromoteParams struct {
	PromotedTaskID uuid.UUID
}

// ListVisionFilter narrows a List call to specific statuses or initiatives.
type ListVisionFilter struct {
	ParentInitiative string       // empty = all initiatives
	Status           VisionStatus // empty = all non-dismissed
}
