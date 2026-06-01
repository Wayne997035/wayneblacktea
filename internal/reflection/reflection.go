// Package reflection implements the Reflection domain: persisted AI-generated
// reflection records for daily reviews, weekly summaries, and entity-specific
// analysis (task, decision, proposal, knowledge, system).
package reflection

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested reflection does not exist.
var ErrNotFound = errors.New("reflection: not found")

// AllowedTypes is the set of valid reflection type values.
var AllowedTypes = map[string]bool{
	"daily":     true,
	"weekly":    true,
	"task":      true,
	"decision":  true,
	"proposal":  true,
	"knowledge": true,
	"system":    true,
}

// Reflection is the domain model for a persisted AI reflection record.
type Reflection struct {
	ID                uuid.UUID       `json:"id"`
	WorkspaceID       *uuid.UUID      `json:"workspace_id,omitempty"`
	Type              string          `json:"type"`
	RelatedEntityType *string         `json:"related_entity_type,omitempty"`
	RelatedEntityID   *uuid.UUID      `json:"related_entity_id,omitempty"`
	Summary           string          `json:"summary"`
	Insights          json.RawMessage `json:"insights,omitempty"`
	PatternsDetected  json.RawMessage `json:"patterns_detected,omitempty"`
	SuggestedActions  json.RawMessage `json:"suggested_actions,omitempty"`
	Confidence        float64         `json:"confidence"`
	CreatedAt         time.Time       `json:"created_at"`
}

// CreateParams holds the fields required to record a new reflection.
type CreateParams struct {
	WorkspaceID       *uuid.UUID
	Type              string
	RelatedEntityType *string
	RelatedEntityID   *uuid.UUID
	Summary           string
	Insights          json.RawMessage
	PatternsDetected  json.RawMessage
	SuggestedActions  json.RawMessage
	Confidence        float64
}

// ListParams narrows a List call.
type ListParams struct {
	WorkspaceID *uuid.UUID
	Type        *string
	Limit       int // 0 = default 20
}
