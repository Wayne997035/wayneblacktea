// Package worksession implements the work_session first-class data model
// introduced in P0a-α Session Core. It is intentionally named worksession
// (not session) to avoid collision with the existing session_handoff package
// (internal/session).
package worksession

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrNotFound is returned when the requested work session does not exist.
var ErrNotFound = errors.New("worksession: not found")

// ErrAlreadyActive is returned when a second in_progress session is created
// for the same workspace+repo (partial unique index violation).
var ErrAlreadyActive = errors.New("worksession: another session is already in_progress for this repo")

// Session is the domain model for a work session.
// It mirrors the work_sessions table columns using Go-native types so both
// the Postgres and SQLite stores can return the same struct.
type Session struct {
	ID               uuid.UUID  `json:"id"`
	WorkspaceID      uuid.UUID  `json:"workspace_id"`
	RepoName         string     `json:"repo_name"`
	ProjectID        *uuid.UUID `json:"project_id,omitempty"`
	Title            string     `json:"title"`
	Goal             string     `json:"goal"`
	Status           string     `json:"status"`
	Source           string     `json:"source"`
	ConfirmedPlanID  *uuid.UUID `json:"confirmed_plan_id,omitempty"`
	CurrentTaskID    *uuid.UUID `json:"current_task_id,omitempty"`
	FinalSummary     *string    `json:"final_summary,omitempty"`
	StartedAt        *string    `json:"started_at,omitempty"`
	LastCheckpointAt *string    `json:"last_checkpoint_at,omitempty"`
	CompletedAt      *string    `json:"completed_at,omitempty"`
	CreatedAt        string     `json:"created_at"`
	UpdatedAt        string     `json:"updated_at"`
}

// SessionTask is the domain model for a work_session_tasks row.
type SessionTask struct {
	SessionID uuid.UUID `json:"session_id"`
	TaskID    uuid.UUID `json:"task_id"`
	Role      string    `json:"role"`
	CreatedAt string    `json:"created_at"`
}

// CreateParams holds the inputs for creating a new work session.
type CreateParams struct {
	WorkspaceID     uuid.UUID
	RepoName        string
	ProjectID       *uuid.UUID
	Title           string
	Goal            string
	Source          string
	ConfirmedPlanID *uuid.UUID
	TaskIDs         []uuid.UUID // linked with role=primary
}

// CheckpointParams holds the inputs for checkpointing a work session.
type CheckpointParams struct {
	SessionID         uuid.UUID
	Summary           string
	CompletedTaskIDs  []uuid.UUID
	NewTaskTitles     []string
	NewDecisionTitles []string
	Blockers          []string
	NextActions       []string
}

// FinishParams holds the inputs for finishing a work session.
type FinishParams struct {
	SessionID          uuid.UUID
	Summary            string
	CompletedTaskIDs   []uuid.UUID
	DeferredTaskIDs    []uuid.UUID
	Artifact           *string
	FollowUpTaskTitles []string
}

// ActiveSessionResult is returned by GetActive.
type ActiveSessionResult struct {
	Active                bool          `json:"active"`
	Session               *Session      `json:"session,omitempty"`
	LinkedTasks           []SessionTask `json:"linked_tasks,omitempty"`
	LastCheckpoint        *string       `json:"last_checkpoint,omitempty"`
	ImplementationAllowed bool          `json:"implementation_allowed"`
}

// StoreIface is the backend-agnostic contract for the worksession bounded
// context. Both the Postgres and SQLite stores must satisfy this interface.
type StoreIface interface {
	// Create inserts a new work session with status=in_progress and links the
	// supplied task IDs (role=primary). Returns ErrAlreadyActive if a session
	// is already in_progress for the same workspace+repo.
	Create(ctx context.Context, p CreateParams) (*Session, error)

	// GetActive returns the in_progress session for workspace+repo, or
	// ErrNotFound when none exists.
	GetActive(ctx context.Context, workspaceID uuid.UUID, repoName string) (*ActiveSessionResult, error)

	// Checkpoint updates last_checkpoint_at, sets status=checkpointed, and
	// returns the updated session.
	Checkpoint(ctx context.Context, p CheckpointParams) (*Session, error)

	// Finish sets status=completed, completed_at=now, stores final_summary,
	// and returns the updated session. It uses a conditional UPDATE
	// (WHERE status='in_progress' OR status='checkpointed') to prevent races.
	Finish(ctx context.Context, p FinishParams) (*Session, error)

	// GetByID returns the session with the given ID, scoped to workspaceID.
	GetByID(ctx context.Context, workspaceID, sessionID uuid.UUID) (*Session, error)

	// LinkTask attaches a task to a session with the given role. No-ops if
	// the (session_id, task_id) pair already exists.
	LinkTask(ctx context.Context, sessionID, taskID uuid.UUID, role string) error

	// LinkedTasks returns all task links for the given session.
	LinkedTasks(ctx context.Context, sessionID uuid.UUID) ([]SessionTask, error)
}
