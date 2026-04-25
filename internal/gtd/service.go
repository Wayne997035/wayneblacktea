package gtd

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/waynechen/wayneblacktea/internal/db"
)

// Service wraps db.Queries and exposes a domain-friendly GTD API.
// Callers pass Go-native types; pgtype conversion is handled internally.
type Service struct {
	q *db.Queries
}

// New creates a Service backed by the given Queries.
func New(q *db.Queries) *Service {
	return &Service{q: q}
}

// CreateProjectParams holds the caller-facing fields for project creation.
type CreateProjectParams struct {
	GoalID      *uuid.UUID // nil → no parent goal
	Name        string
	Title       string
	Description string // empty → NULL
	Area        string // defaults to "projects"
	Priority    int32  // defaults to 3
}

// CreateTaskParams holds the caller-facing fields for task creation.
type CreateTaskParams struct {
	ProjectID   *uuid.UUID // nil → no project
	Title       string
	Description string // empty → NULL
	Priority    int32  // defaults to 3
	Assignee    string // empty → NULL
}

// toText converts a plain string to pgtype.Text, treating "" as NULL.
func toText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

// toUUID converts *uuid.UUID to pgtype.UUID, treating nil as NULL.
func toUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*id), Valid: true}
}

// ListActiveProjects returns all projects with status = 'active'.
func (s *Service) ListActiveProjects(ctx context.Context) ([]db.Project, error) {
	projects, err := s.q.ListActiveProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active projects: %w", err)
	}
	return projects, nil
}

// GetProjectByName returns a project by its unique name.
func (s *Service) GetProjectByName(ctx context.Context, name string) (db.Project, error) {
	p, err := s.q.GetProjectByName(ctx, name)
	if err != nil {
		return db.Project{}, fmt.Errorf("get project by name %q: %w", name, err)
	}
	return p, nil
}

// CreateProject inserts a new project and returns it.
func (s *Service) CreateProject(ctx context.Context, p CreateProjectParams) (db.Project, error) {
	area := p.Area
	if area == "" {
		area = "projects"
	}
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	proj, err := s.q.CreateProject(ctx, db.CreateProjectParams{
		GoalID:      toUUID(p.GoalID),
		Name:        p.Name,
		Title:       p.Title,
		Description: toText(p.Description),
		Area:        area,
		Priority:    priority,
	})
	if err != nil {
		return db.Project{}, fmt.Errorf("create project %q: %w", p.Name, err)
	}
	return proj, nil
}

// GetTasks returns pending/in-progress tasks. When projectID is non-nil, it
// filters to that project; otherwise all pending tasks are returned.
func (s *Service) GetTasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error) {
	if projectID != nil {
		tasks, err := s.q.GetTasksByProject(ctx, toUUID(projectID))
		if err != nil {
			return nil, fmt.Errorf("get tasks by project %s: %w", *projectID, err)
		}
		return tasks, nil
	}
	tasks, err := s.q.GetAllPendingTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all pending tasks: %w", err)
	}
	return tasks, nil
}

// CreateTask inserts a new task and returns it.
func (s *Service) CreateTask(ctx context.Context, p CreateTaskParams) (db.Task, error) {
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	task, err := s.q.CreateTask(ctx, db.CreateTaskParams{
		ProjectID:   toUUID(p.ProjectID),
		Title:       p.Title,
		Description: toText(p.Description),
		Priority:    priority,
		Assignee:    toText(p.Assignee),
	})
	if err != nil {
		return db.Task{}, fmt.Errorf("create task %q: %w", p.Title, err)
	}
	return task, nil
}

// CompleteTask marks a task as completed and records an optional artifact URL.
func (s *Service) CompleteTask(ctx context.Context, id uuid.UUID, artifact *string) (db.Task, error) {
	var art pgtype.Text
	if artifact != nil {
		art = pgtype.Text{String: *artifact, Valid: true}
	}
	task, err := s.q.CompleteTask(ctx, db.CompleteTaskParams{
		ID:       id,
		Artifact: art,
	})
	if err != nil {
		return db.Task{}, fmt.Errorf("complete task %s: %w", id, err)
	}
	return task, nil
}

// LogActivity records an activity log entry.
func (s *Service) LogActivity(ctx context.Context, actor, action string, projectID *uuid.UUID, notes string) error {
	_, err := s.q.CreateActivityLog(ctx, db.CreateActivityLogParams{
		Actor:     actor,
		ProjectID: toUUID(projectID),
		Action:    action,
		Notes:     toText(notes),
	})
	if err != nil {
		return fmt.Errorf("log activity %q by %q: %w", action, actor, err)
	}
	return nil
}

// ListActiveGoals returns all goals with status = 'active'.
func (s *Service) ListActiveGoals(ctx context.Context) ([]db.Goal, error) {
	goals, err := s.q.ListActiveGoals(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active goals: %w", err)
	}
	return goals, nil
}

// WeeklyProgress returns the number of tasks completed this week and the
// total number of active (pending + in_progress) tasks.
func (s *Service) WeeklyProgress(ctx context.Context) (completed int64, total int64, err error) {
	completed, err = s.q.CountCompletedTasksThisWeek(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("count completed tasks this week: %w", err)
	}
	total, err = s.q.CountTotalActiveTasks(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("count total active tasks: %w", err)
	}
	return completed, total, nil
}
