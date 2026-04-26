package gtd

import (
	"errors"
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
	Description string // empty → NULL
	Priority    int32  // defaults to 3
	Assignee    string // empty → NULL
}
