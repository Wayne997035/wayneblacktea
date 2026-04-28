package gtd

import (
	"context"

	"github.com/google/uuid"
	"github.com/waynechen/wayneblacktea/internal/db"
)

// StoreIface is the backend-agnostic contract for the GTD bounded context.
// Postgres-backed *Store satisfies this interface; an upcoming SQLite-backed
// store will satisfy the same surface to enable swapping without changing
// HTTP handlers or MCP tools.
//
// Transactional helpers (WithTx) are intentionally absent — atomic
// multi-store operations stay on concrete pg.Store until the cross-backend
// unit-of-work design is settled (Phase C+).
type StoreIface interface {
	ListActiveProjects(ctx context.Context) ([]db.Project, error)
	ProjectByName(ctx context.Context, name string) (*db.Project, error)
	CreateProject(ctx context.Context, p CreateProjectParams) (*db.Project, error)
	Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error)
	CreateTask(ctx context.Context, p CreateTaskParams) (*db.Task, error)
	CompleteTask(ctx context.Context, id uuid.UUID, artifact *string) (*db.Task, error)
	LogActivity(ctx context.Context, actor, action string, projectID *uuid.UUID, notes string) error
	ActiveGoals(ctx context.Context) ([]db.Goal, error)
	CreateGoal(ctx context.Context, p CreateGoalParams) (*db.Goal, error)
	UpdateTaskStatus(ctx context.Context, id uuid.UUID, status TaskStatus) (*db.Task, error)
	UpdateProjectStatus(ctx context.Context, id uuid.UUID, status ProjectStatus) (*db.Project, error)
	DeleteTask(ctx context.Context, id uuid.UUID) error
	WeeklyProgress(ctx context.Context) (completed, total int64, err error)
}

// Compile-time assertion: pg-backed Store implements StoreIface.
var _ StoreIface = (*Store)(nil)
