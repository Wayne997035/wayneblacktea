// Package skill provides the Skill Library domain — reusable Claude Code skill
// definitions extracted from successful sessions.
package skill

import "time"

// SkillExamplesMaxEntries bounds skills.examples to the most recently
// recorded entries, oldest first (FIFO) — OWASP LLM04 unbounded consumption;
// GTD 17f08ba8, closed by F0906-11..13. It is a retention cap, not a count of
// outcomes ever recorded: success_count/failure_count are not truncated by
// it. [F0906-32] Single source of truth for both backends
// (internal/skill/store.go, internal/storage/sqlite/skill.go).
const SkillExamplesMaxEntries = 20

// DefaultListLimit / MaxListLimit are the row-cap policy for search_skills and
// list_relevant_skills, and ClampListLimit is the only place either is applied.
//
// [GTD b0c90957] Before this, four call sites — both MCP handlers and both
// backends' Search/ListRelevant — each floored limit at 10 and none of them
// capped it, so `limit: 1000000` from an MCP caller became the SQL LIMIT
// verbatim. SkillExamplesMaxEntries bounds how many examples one skill
// carries; nothing bounded how many skills one response carried, which is the
// same OWASP LLM04 control one layer up.
//
// ⚠ This caps ROWS, not BYTES. A skill row is far fatter than a task row
// (name 200 + description 5,000 + four 2,000-rune list fields + up to
// SkillExamplesMaxEntries examples), which is why the cap is 100 rather than
// the 200 the task list tools use — but 100 fat rows is still a large
// response. A response-size budget is a separate control and is not in place
// here.
//
// A shared helper rather than four copies of the same two ifs: the copies are
// how the floor ended up in four places and the ceiling in none.
const (
	DefaultListLimit = 10
	MaxListLimit     = 100
)

// ClampListLimit maps a caller-supplied skill row limit onto [1, MaxListLimit],
// resolving "unset" (<= 0) to DefaultListLimit rather than to no cap.
func ClampListLimit(limit int) int {
	if limit <= 0 {
		return DefaultListLimit
	}
	if limit > MaxListLimit {
		return MaxListLimit
	}
	return limit
}

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
