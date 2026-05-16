package gtd

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ProjectStatus represents the lifecycle of a project.
type ProjectStatus string

const (
	ProjectStatusActive    ProjectStatus = "active"
	ProjectStatusCompleted ProjectStatus = "completed"
	ProjectStatusArchived  ProjectStatus = "archived"
	ProjectStatusOnHold    ProjectStatus = "on_hold"
)

// IsValid reports whether the status is a known GoalStatus value.
func (s GoalStatus) IsValid() bool {
	switch s {
	case GoalStatusActive, GoalStatusCompleted, GoalStatusArchived:
		return true
	}
	return false
}

// IsValid reports whether the status is a known ProjectStatus value.
func (s ProjectStatus) IsValid() bool {
	switch s {
	case ProjectStatusActive, ProjectStatusCompleted, ProjectStatusArchived, ProjectStatusOnHold:
		return true
	}
	return false
}

// TaskStatus represents the lifecycle of a task.
type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "pending"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusCompleted  TaskStatus = "completed"
	TaskStatusCancelled  TaskStatus = "cancelled"
)

// GoalStatus represents the lifecycle of a goal.
type GoalStatus string

const (
	GoalStatusActive    GoalStatus = "active"
	GoalStatusCompleted GoalStatus = "completed"
	GoalStatusArchived  GoalStatus = "archived"
)

var (
	// ErrNotFound is returned when a requested entity does not exist.
	ErrNotFound = errors.New("gtd: not found")
	// ErrConflict is returned on uniqueness violations (e.g. duplicate project name).
	ErrConflict = errors.New("gtd: conflict")
)

// CreateProjectParams holds parameters for creating a new project.
type CreateProjectParams struct {
	GoalID      *uuid.UUID // nil → no parent goal
	Name        string
	Title       string
	Description string // empty → NULL
	Area        string // defaults to "projects"
	Priority    int32  // defaults to 3
	RepoName    string // empty → NULL; links project to a VCS repo slug
}

// CreateGoalParams holds parameters for creating a new goal.
type CreateGoalParams struct {
	Title       string
	Description string     // empty → NULL
	Area        string     // empty → NULL
	DueDate     *time.Time // nil → NULL
}

// CreateTaskParams holds parameters for creating a new task.
type CreateTaskParams struct {
	ProjectID   *uuid.UUID // nil → no project
	Title       string
	Description string     // empty → NULL
	Priority    int32      // defaults to 3
	Assignee    string     // empty → NULL
	DueDate     *time.Time // nil → NULL
	Importance  *int16     // nil → NULL; valid range 1..3 (1=high, 2=med, 3=low)
	Context     string     // empty → NULL; free-form discussion background
	Kind        string     // empty → defaults to "general"; one of general/fix-pr/feature/refactor/research/chore
}

// UpdateGoalParams holds parameters for a full update of a goal.
type UpdateGoalParams struct {
	Title       string
	Description string // empty → NULL
	Area        string // empty → NULL
	Status      GoalStatus
	DueDate     *time.Time // nil → NULL
}

// UpdateProjectParams holds parameters for a full update of a project.
type UpdateProjectParams struct {
	Title       string
	Description string // empty → NULL
	Area        string // defaults to "projects" if empty
	Priority    int32  // defaults to 3 if zero
	Status      ProjectStatus
	GoalID      *uuid.UUID // nil → NULL
	RepoName    *string    // nil → preserve existing; empty string → clear to NULL
}

// UpdateTaskParams holds parameters for a partial update of a task.
// nil pointer = "don't change this field" (no null-clear support).
// All-nil = invalid; callers must set at least one field.
type UpdateTaskParams struct {
	Title       *string
	Description *string
	Priority    *int32
	Importance  *int16
	Assignee    *string
	DueDate     *time.Time
	Context     *string
	Status      *string
}

// ChecklistItem represents a single item in a task's structured checklist.
// Stored as a JSONB array in Postgres and a JSON TEXT array in SQLite.
type ChecklistItem struct {
	ID          uuid.UUID  `json:"id"`
	Title       string     `json:"title"`
	FileRef     string     `json:"file_ref,omitempty"`
	Done        bool       `json:"done"`
	EvidenceURL string     `json:"evidence_url,omitempty"`
	Notes       string     `json:"notes,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// UpdateChecklistItemParams holds the optional fields for patching a checklist item.
// nil pointer = "don't change this field".
type UpdateChecklistItemParams struct {
	Done        *bool
	Title       *string
	Notes       *string
	EvidenceURL *string
}

// ChecklistMaxTitle is the maximum length for a checklist item title in runes.
const ChecklistMaxTitle = 500

// ChecklistMaxText is the maximum length for notes/file_ref/evidence_url in runes.
const ChecklistMaxText = 2000

// SanitiseChecklistText strips null bytes and ASCII control characters (except tab)
// from a string, and trims leading/trailing whitespace.
func SanitiseChecklistText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\x00' || (r < 0x20 && r != '\t') {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
