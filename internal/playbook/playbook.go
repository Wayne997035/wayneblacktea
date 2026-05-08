// Package playbook implements the Playbook domain: generalised procedural rules
// derived from recurring decisions ("if I see X situation, do Y action").
// Playbooks are produced by the weekly playbook-promoter cron job and surfaced
// via the list_playbooks MCP tool so Claude can consult them before complex tasks.
package playbook

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested playbook does not exist.
var ErrNotFound = errors.New("playbook: not found")

// Playbook is the domain model for a procedural rule entry.
type Playbook struct {
	ID                uuid.UUID
	WorkspaceID       *uuid.UUID
	TriggerPattern    string
	ActionTemplate    string
	SourceDecisionIDs []uuid.UUID
	Confidence        float64
	Hits              int
	LastUsedAt        *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CreateParams holds the fields required to record a new playbook.
type CreateParams struct {
	WorkspaceID       *uuid.UUID
	TriggerPattern    string
	ActionTemplate    string
	SourceDecisionIDs []uuid.UUID
	Confidence        float64
}

// ListParams narrows a List call. WorkspaceID scopes results; ContextKeywords
// filters by keyword match against trigger_pattern or action_template; Limit
// caps the row count (0 = default 20).
type ListParams struct {
	WorkspaceID     *uuid.UUID
	ContextKeywords []string // optional — OR-combined ILIKE filter
	Limit           int      // 0 = default 20
}

// StoreIface is the backend-agnostic contract for the Playbook bounded context.
type StoreIface interface {
	Create(ctx context.Context, p CreateParams) (*Playbook, error)
	List(ctx context.Context, p ListParams) ([]*Playbook, error)
	IncrementHits(ctx context.Context, id uuid.UUID) error
}
