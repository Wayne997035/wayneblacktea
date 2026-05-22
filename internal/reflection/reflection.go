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
	ID                uuid.UUID
	WorkspaceID       *uuid.UUID
	Type              string
	RelatedEntityType *string
	RelatedEntityID   *uuid.UUID
	Summary           string
	Insights          json.RawMessage
	PatternsDetected  json.RawMessage
	SuggestedActions  json.RawMessage
	Confidence        float64
	CreatedAt         time.Time
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
