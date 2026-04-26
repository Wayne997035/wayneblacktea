package handler

import (
	"context"

	"github.com/google/uuid"
	"github.com/waynechen/wayneblacktea/internal/db"
	"github.com/waynechen/wayneblacktea/internal/decision"
	"github.com/waynechen/wayneblacktea/internal/gtd"
	"github.com/waynechen/wayneblacktea/internal/session"
	"github.com/waynechen/wayneblacktea/internal/workspace"
)

// gtdStore covers the subset of gtd.Store used by handlers.
type gtdStore interface {
	ListActiveProjects(ctx context.Context) ([]db.Project, error)
	ActiveGoals(ctx context.Context) ([]db.Goal, error)
	CreateGoal(ctx context.Context, p gtd.CreateGoalParams) (*db.Goal, error)
	CreateProject(ctx context.Context, p gtd.CreateProjectParams) (*db.Project, error)
	Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error)
	CreateTask(ctx context.Context, p gtd.CreateTaskParams) (*db.Task, error)
	CompleteTask(ctx context.Context, id uuid.UUID, artifact *string) (*db.Task, error)
	UpdateTaskStatus(ctx context.Context, id uuid.UUID, status gtd.TaskStatus) (*db.Task, error)
	UpdateProjectStatus(ctx context.Context, id uuid.UUID, status gtd.ProjectStatus) (*db.Project, error)
	WeeklyProgress(ctx context.Context) (completed, total int64, err error)
}

// workspaceStore covers the subset of workspace.Store used by handlers.
type workspaceStore interface {
	ActiveRepos(ctx context.Context) ([]db.Repo, error)
	UpsertRepo(ctx context.Context, p workspace.UpsertRepoParams) (*db.Repo, error)
}

// decisionStore covers the subset of decision.Store used by handlers.
type decisionStore interface {
	ByRepo(ctx context.Context, repoName string, limit int32) ([]db.Decision, error)
	ByProject(ctx context.Context, projectID uuid.UUID, limit int32) ([]db.Decision, error)
	Log(ctx context.Context, p decision.LogParams) (*db.Decision, error)
}

// sessionStore covers the subset of session.Store used by handlers.
type sessionStore interface {
	LatestHandoff(ctx context.Context) (*db.SessionHandoff, error)
	SetHandoff(ctx context.Context, p session.HandoffParams) (*db.SessionHandoff, error)
}

// errResp returns a standard error response body.
func errResp(msg string) map[string]string {
	return map[string]string{"error": msg}
}
