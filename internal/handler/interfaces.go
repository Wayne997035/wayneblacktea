package handler

import (
	"context"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
)

// proposalStore covers the subset of proposal.Store used by handlers.
type proposalStore interface {
	AutoProposeConceptFromKnowledge(ctx context.Context, item *db.KnowledgeItem, proposedBy string) (*db.PendingProposal, error)
}

// gtdStore covers the subset of gtd.Store used by handlers.
type gtdStore interface {
	ListActiveProjects(ctx context.Context) ([]db.Project, error)
	GetProjectByID(ctx context.Context, id uuid.UUID) (*db.Project, error)
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
	All(ctx context.Context, limit int32) ([]db.Decision, error)
	ByRepo(ctx context.Context, repoName string, limit int32) ([]db.Decision, error)
	ByProject(ctx context.Context, projectID uuid.UUID, limit int32) ([]db.Decision, error)
	Log(ctx context.Context, p decision.LogParams) (*db.Decision, error)
}

// sessionStore covers the subset of session.Store used by handlers.
type sessionStore interface {
	LatestHandoff(ctx context.Context) (*db.SessionHandoff, error)
	SetHandoff(ctx context.Context, p session.HandoffParams) (*db.SessionHandoff, error)
}

// knowledgeStore covers the subset of knowledge.Store used by handlers.
type knowledgeStore interface {
	AddItem(ctx context.Context, p knowledge.AddItemParams) (*db.KnowledgeItem, error)
	Search(ctx context.Context, query string, limit int) ([]db.KnowledgeItem, error)
	List(ctx context.Context, limit, offset int) ([]db.KnowledgeItem, error)
	GetByID(ctx context.Context, id uuid.UUID) (*db.KnowledgeItem, error)
}

// suggestionDecisionStore covers the subset of decision.Store used by the
// learning suggestions endpoint.
type suggestionDecisionStore interface {
	All(ctx context.Context, limit int32) ([]db.Decision, error)
}

// learningStore covers the subset of learning.Store used by handlers.
type learningStore interface {
	DueReviews(ctx context.Context, limit int) ([]learning.DueReview, error)
	SubmitReview(ctx context.Context, scheduleID uuid.UUID, state learning.CardState, rating learning.Rating) error
	CreateConcept(ctx context.Context, title, content string, tags []string) (*db.Concept, error)
}

// autologGTDStore covers the subset of gtd.StoreIface used by AutologHandler.
type autologGTDStore interface {
	LogActivity(ctx context.Context, actor, action string, projectID *uuid.UUID, notes string) error
	Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error)
}

// autologSessionStore covers the subset of session.Store used by AutologHandler.
type autologSessionStore interface {
	LatestHandoff(ctx context.Context) (*db.SessionHandoff, error)
	SetHandoff(ctx context.Context, p session.HandoffParams) (*db.SessionHandoff, error)
	Resolve(ctx context.Context, id uuid.UUID) error
}

// autologDecisionStore covers the subset of decision.Store used by AutologHandler.
type autologDecisionStore interface {
	All(ctx context.Context, limit int32) ([]db.Decision, error)
	Log(ctx context.Context, p decision.LogParams) (*db.Decision, error)
}

// errResp returns a standard error response body.
func errResp(msg string) map[string]string {
	return map[string]string{"error": msg}
}
