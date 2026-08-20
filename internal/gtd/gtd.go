package gtd

import (
	"errors"
	"fmt"
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
	// ErrInvalidRepoName is returned when a non-empty repo_name fails
	// validator.IsValidRepoName. Store-layer backstop for CreateProject /
	// UpdateProject on both backends — MCP and HTTP callers already validate
	// before reaching the store, but this closes the gap for any other
	// caller (CLI, reconcile, future integrations) that writes projects
	// directly through the store.
	ErrInvalidRepoName = errors.New("gtd: repo_name must match [a-zA-Z0-9_.-]{1,100}")
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
	ProjectID    *uuid.UUID // nil → no project
	Title        string
	Description  string     // empty → NULL
	Priority     int32      // defaults to 3
	Assignee     string     // empty → NULL
	DueDate      *time.Time // nil → NULL
	Importance   *int16     // nil → NULL; valid range 1..3 (1=high, 2=med, 3=low)
	Context      string     // empty → NULL; free-form discussion background
	Kind         string     // empty → defaults to "general"; one of general/fix-pr/feature/refactor/research/chore
	BranchName   *string    // nil → NULL; git branch name (migration 000047)
	PRUrl        *string    // nil → NULL; GitHub PR URL (migration 000047)
	VisionItemID *uuid.UUID // nil → NULL; vision item this task was promoted from (migration 000050)
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
	Kind        *string // nil → preserve existing; set to one of validator.ValidTaskKinds (GTD-c282cc04)
	BranchName  *string // nil → preserve existing; set to update (migration 000047)
	PRUrl       *string // nil → preserve existing; set to update (migration 000047)
	// AppendCommitSHA: nil → no-op, commit_shas is left untouched. Non-nil →
	// the store atomically appends this single SHA to commit_shas at the SQL
	// layer (array_append in Postgres, json_insert in SQLite) — never a
	// Go-side read-modify-write, which raced under concurrent complete_task
	// calls on the same task (P7, 2026-08-20 mcp-surface-spec, GTD 25537a73
	// follow-up). There is no "replace the whole array" operation; commit_shas
	// is append-only through this API.
	AppendCommitSHA *string
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

// TaskFilter holds the parameters for the filtered task list query used by
// TasksFiltered. Status "" or "active" means pending + in_progress; "all"
// means every task status; any other exact status value is matched literally.
// Limit and Offset drive pagination; callers should pass Limit+1 to detect
// has_more without a COUNT query.
type TaskFilter struct {
	ProjectID *uuid.UUID
	Status    string
	Limit     int
	Offset    int

	// UpdatedSince, when non-nil, restricts results to rows with
	// updated_at >= *UpdatedSince, applied in the WHERE clause (both PG and
	// SQLite backends) rather than filtered client-side after the LIMIT. Nil
	// (the zero value) preserves prior behaviour for existing callers.
	//
	// wbt-2.0 review round2 F2: detectTaskNoOutcome's ORDER BY priority ASC,
	// created_at ASC + Limit 500 lets a growing total completed-task count
	// silently push recently-completed rows out of the window before the
	// 7-day cutoff filter ever runs in Go. Pushing the cutoff into the WHERE
	// clause makes the LIMIT apply to the already-filtered set, so rows
	// inside the recency window are never dropped by row count alone.
	UpdatedSince *time.Time
}

// UpdateChecklistItemParams holds the optional fields for patching a checklist item.
// nil pointer = "don't change this field".
type UpdateChecklistItemParams struct {
	Done        *bool
	Title       *string
	Notes       *string
	EvidenceURL *string
}

// PullForwardCap is the maximum number of tasks PullForwardTasks returns.
// Fixed per user decision (2026-07-19): pull-forward is intentionally small so
// it surfaces important-but-not-yet-due work without drowning out today's
// actual due work in the session-start context.
const PullForwardCap = 5

// PullForwardTomorrowStart returns the absolute instant that is midnight
// "tomorrow" in Asia/Taipei relative to refDate. This is the boundary both
// PullForwardTasks backends (internal/gtd/store.go for Postgres,
// internal/storage/sqlite/gtd.go for SQLite) use to decide whether a task is
// due today (excluded — that's today's work, not pull-forward) vs due
// tomorrow or later (eligible). Shared here so the two backends cannot drift.
//
// MUST build the boundary via time.Date on refDate.In(loc), NOT
// refDate.UTC().Truncate(24*time.Hour) — Time.Truncate rounds against the
// absolute duration since the zero time (effectively a UTC-aligned
// operation), not the wall-clock day in the given Location. The old
// UTC-truncation boundary therefore landed at Taipei 08:00, not Taipei
// 00:00 — a task due later THIS Taipei calendar day (e.g. 23:00 Taipei) has
// a UTC due_date that already fell on/after that skewed boundary and was
// wrongly pulled forward as if it were tomorrow's work (production bug,
// confirmed 2026-07-20). Taiwan has no DST, so this was a plain offset bug,
// not a DST edge case. Mirrors the Asia/Taipei source used throughout
// internal/scheduler (see scheduler.go's own time.LoadLocation calls).
func PullForwardTomorrowStart(refDate time.Time) (time.Time, error) {
	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		return time.Time{}, fmt.Errorf("PullForwardTomorrowStart: loading Asia/Taipei timezone: %w", err)
	}
	inTaipei := refDate.In(loc)
	y, m, d := inTaipei.Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, loc), nil
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
