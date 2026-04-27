package gtd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/waynechen/wayneblacktea/internal/db"
)

// Store handles all database operations for the GTD bounded context.
type Store struct {
	q *db.Queries
}

// NewStore returns a Store backed by the given DBTX (pool or transaction).
func NewStore(dbtx db.DBTX) *Store {
	return &Store{q: db.New(dbtx)}
}

// WithTx returns a Store bound to tx, for use in multi-store transactions.
func (s *Store) WithTx(tx pgx.Tx) *Store {
	return &Store{q: s.q.WithTx(tx)}
}

func toText(v string) pgtype.Text {
	return pgtype.Text{String: v, Valid: v != ""}
}

func toTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func toUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*id), Valid: true}
}

// ListActiveProjects returns all active projects ordered by priority.
func (s *Store) ListActiveProjects(ctx context.Context) ([]db.Project, error) {
	rows, err := s.q.ListActiveProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing active projects: %w", err)
	}
	return rows, nil
}

// ProjectByName returns a single project by unique name.
func (s *Store) ProjectByName(ctx context.Context, name string) (*db.Project, error) {
	row, err := s.q.GetProjectByName(ctx, name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying project %q: %w", name, err)
	}
	return &row, nil
}

// CreateProject inserts a new project.
func (s *Store) CreateProject(ctx context.Context, p CreateProjectParams) (*db.Project, error) {
	area := p.Area
	if area == "" {
		area = "projects"
	}
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	row, err := s.q.CreateProject(ctx, db.CreateProjectParams{
		GoalID:      toUUID(p.GoalID),
		Name:        p.Name,
		Title:       p.Title,
		Description: toText(p.Description),
		Area:        area,
		Priority:    priority,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("creating project %q: %w", p.Name, err)
	}
	return &row, nil
}

// Tasks returns pending/in-progress tasks, optionally filtered by project.
func (s *Store) Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error) {
	if projectID != nil {
		rows, err := s.q.GetTasksByProject(ctx, toUUID(projectID))
		if err != nil {
			return nil, fmt.Errorf("listing tasks for project %s: %w", *projectID, err)
		}
		return rows, nil
	}
	rows, err := s.q.GetAllPendingTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing all pending tasks: %w", err)
	}
	return rows, nil
}

// CreateTask inserts a new task.
func (s *Store) CreateTask(ctx context.Context, p CreateTaskParams) (*db.Task, error) {
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	row, err := s.q.CreateTask(ctx, db.CreateTaskParams{
		ProjectID:   toUUID(p.ProjectID),
		Title:       p.Title,
		Description: toText(p.Description),
		Priority:    priority,
		Assignee:    toText(p.Assignee),
		Importance:  toInt2(p.Importance),
		Context:     toText(p.Context),
	})
	if err != nil {
		return nil, fmt.Errorf("creating task %q: %w", p.Title, err)
	}
	return &row, nil
}

func toInt2(v *int16) pgtype.Int2 {
	if v == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: *v, Valid: true}
}

// CompleteTask marks a task as completed with an optional artifact URL.
func (s *Store) CompleteTask(ctx context.Context, id uuid.UUID, artifact *string) (*db.Task, error) {
	var art pgtype.Text
	if artifact != nil {
		art = pgtype.Text{String: *artifact, Valid: true}
	}
	row, err := s.q.CompleteTask(ctx, db.CompleteTaskParams{
		ID:       id,
		Artifact: art,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("completing task %s: %w", id, err)
	}
	return &row, nil
}

// LogActivity records an activity entry.
func (s *Store) LogActivity(ctx context.Context, actor, action string, projectID *uuid.UUID, notes string) error {
	_, err := s.q.CreateActivityLog(ctx, db.CreateActivityLogParams{
		Actor:     actor,
		ProjectID: toUUID(projectID),
		Action:    action,
		Notes:     toText(notes),
	})
	if err != nil {
		return fmt.Errorf("logging activity: %w", err)
	}
	return nil
}

// ActiveGoals returns all active goals ordered by due date.
func (s *Store) ActiveGoals(ctx context.Context) ([]db.Goal, error) {
	rows, err := s.q.ListActiveGoals(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing active goals: %w", err)
	}
	return rows, nil
}

// CreateGoal inserts a new goal.
func (s *Store) CreateGoal(ctx context.Context, p CreateGoalParams) (*db.Goal, error) {
	row, err := s.q.CreateGoal(ctx, db.CreateGoalParams{
		Title:       p.Title,
		Description: toText(p.Description),
		Area:        toText(p.Area),
		DueDate:     toTimestamptz(p.DueDate),
	})
	if err != nil {
		return nil, fmt.Errorf("creating goal %q: %w", p.Title, err)
	}
	return &row, nil
}

// UpdateTaskStatus sets the status of a task by ID.
func (s *Store) UpdateTaskStatus(ctx context.Context, id uuid.UUID, status TaskStatus) (*db.Task, error) {
	row, err := s.q.UpdateTaskStatus(ctx, db.UpdateTaskStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("updating task %s status: %w", id, err)
	}
	return &row, nil
}

// UpdateProjectStatus sets the status of a project by ID.
func (s *Store) UpdateProjectStatus(ctx context.Context, id uuid.UUID, status ProjectStatus) (*db.Project, error) {
	row, err := s.q.UpdateProjectStatus(ctx, db.UpdateProjectStatusParams{
		ID:     id,
		Status: string(status),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("updating project %s status: %w", id, err)
	}
	return &row, nil
}

// DeleteTask permanently removes a task by ID.
func (s *Store) DeleteTask(ctx context.Context, id uuid.UUID) error {
	if err := s.q.DeleteTask(ctx, id); err != nil {
		return fmt.Errorf("deleting task %s: %w", id, err)
	}
	return nil
}

// WeeklyProgress returns completed task count this week and total active task count.
func (s *Store) WeeklyProgress(ctx context.Context) (completed, total int64, err error) {
	completed, err = s.q.CountCompletedTasksThisWeek(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("counting completed tasks: %w", err)
	}
	total, err = s.q.CountTotalActiveTasks(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("counting active tasks: %w", err)
	}
	return completed, total, nil
}
