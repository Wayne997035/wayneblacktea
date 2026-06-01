// Package skill provides the Skill Library domain — reusable Claude Code skill
// definitions extracted from successful sessions.
package skill

import "time"

// Skill is a reusable skill definition extracted from a Claude Code session.
type Skill struct {
	ID                    string     `json:"id"`
	WorkspaceID           *string    `json:"workspace_id,omitempty"`
	Name                  string     `json:"name"`
	Description           string     `json:"description"`
	Triggers              []string   `json:"triggers"`
	Steps                 []string   `json:"steps"`
	FailureModes          []string   `json:"failure_modes"`
	VerificationChecklist []string   `json:"verification_checklist"`
	Examples              []any      `json:"examples"`
	SourceAtomIDs         []string   `json:"source_atom_ids"`
	SuccessCount          int        `json:"success_count"`
	FailureCount          int        `json:"failure_count"`
	LastUsedAt            *time.Time `json:"last_used_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// AddParams carries the inputs for creating a new Skill row.
type AddParams struct {
	WorkspaceID           *string
	Name                  string
	Description           string
	Triggers              []string
	Steps                 []string
	FailureModes          []string
	VerificationChecklist []string
	Examples              []any
	SourceAtomIDs         []string
}

// UpdateFromOutcomeParams carries inputs for recording an outcome against a skill.
type UpdateFromOutcomeParams struct {
	SkillID   string
	OutcomeID string // code-layer reference; Memory-3 outcome row ID (TEXT, no FK)
	Success   bool
	Notes     string
}

// SearchFilter constrains a Search or ListRelevant query.
type SearchFilter struct {
	WorkspaceID *string
	Query       string
	Limit       int
}
