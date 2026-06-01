// Package behaviorrule implements the BehaviorRule domain: persisted AI-derived
// or manually-authored rules that guide agent behavior. Rules flow through a
// proposal queue (status='proposed') and are promoted to 'active' via
// apply_behavior_rules outcome='success'. Confidence adjusts with each outcome.
package behaviorrule

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested behavior rule does not exist.
var ErrNotFound = errors.New("behaviorrule: not found")

// AllowedSourceTypes is the set of valid source_type values.
var AllowedSourceTypes = map[string]bool{
	"reflection": true,
	"outcome":    true,
	"manual":     true,
}

// AllowedStatuses is the set of valid status values.
var AllowedStatuses = map[string]bool{
	"proposed":   true,
	"active":     true,
	"rejected":   true,
	"deprecated": true,
}

// BehaviorRule is the domain model for a persisted behavior rule record.
type BehaviorRule struct {
	ID          uuid.UUID  `json:"id"`
	WorkspaceID *uuid.UUID `json:"workspace_id,omitempty"`
	Condition   string     `json:"condition"`
	Action      string     `json:"action"`
	SourceType  string     `json:"source_type"`
	SourceID    *uuid.UUID `json:"source_id,omitempty"`
	Confidence  float64    `json:"confidence"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// CreateParams holds the fields required to propose a new behavior rule.
type CreateParams struct {
	WorkspaceID *uuid.UUID
	Condition   string
	Action      string
	SourceType  string
	SourceID    *uuid.UUID
	Confidence  float64 // defaults to 0.50 when zero
}

// ListParams narrows a List call.
type ListParams struct {
	WorkspaceID *uuid.UUID
	Status      *string // nil = all statuses
	Limit       int     // 0 = default 20
}

// UpdateStatusParams holds the fields required to update a rule's status.
type UpdateStatusParams struct {
	ID     uuid.UUID
	Status string
}

// UpdateConfidenceParams holds the fields required to update confidence.
type UpdateConfidenceParams struct {
	ID         uuid.UUID
	Confidence float64
}
