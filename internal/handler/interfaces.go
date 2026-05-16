package handler

import (
	"context"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/timeline"
	"github.com/Wayne997035/wayneblacktea/internal/vision"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
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
	// TasksByProjectAllStatuses backs the `?status=all` variant of
	// GET /api/projects/:id/tasks (used by the project-detail UI to render
	// the "completed" section). Default behaviour stays active-only.
	TasksByProjectAllStatuses(ctx context.Context, projectID uuid.UUID) ([]db.Task, error)
	CreateTask(ctx context.Context, p gtd.CreateTaskParams) (*db.Task, error)
	CompleteTask(ctx context.Context, id uuid.UUID, artifact *string) (*db.Task, error)
	UpdateTaskStatus(ctx context.Context, id uuid.UUID, status gtd.TaskStatus) (*db.Task, error)
	UpdateTask(ctx context.Context, id uuid.UUID, p gtd.UpdateTaskParams) (*db.Task, error)
	UpdateProjectStatus(ctx context.Context, id uuid.UUID, status gtd.ProjectStatus) (*db.Project, error)
	UpdateGoal(ctx context.Context, id uuid.UUID, p gtd.UpdateGoalParams) (*db.Goal, error)
	UpdateProject(ctx context.Context, id uuid.UUID, p gtd.UpdateProjectParams) (*db.Project, error)
	WeeklyProgress(ctx context.Context) (completed, total int64, err error)
	AddChecklistItem(ctx context.Context, taskID uuid.UUID, workspaceID uuid.UUID, item gtd.ChecklistItem) ([]gtd.ChecklistItem, error)
	UpdateChecklistItem(
		ctx context.Context, taskID uuid.UUID, workspaceID uuid.UUID,
		itemID uuid.UUID, update gtd.UpdateChecklistItemParams,
	) ([]gtd.ChecklistItem, error)
	DeleteChecklistItem(ctx context.Context, taskID uuid.UUID, workspaceID uuid.UUID, itemID uuid.UUID) error
	// WorkspaceID returns the workspace UUID scoped to this store. The handler
	// passes it through to the checklist methods so they can enforce workspace
	// isolation without relying on the request (IDOR protection).
	WorkspaceID() pgtype.UUID
}

// workspaceStore covers the subset of workspace.Store used by the basic
// workspace endpoints (list / upsert).
type workspaceStore interface {
	ActiveRepos(ctx context.Context) ([]db.Repo, error)
	UpsertRepo(ctx context.Context, p workspace.UpsertRepoParams) (*db.Repo, error)
}

// repoOverviewWorkspaceStore covers the workspace.StoreIface methods needed
// by GET /api/workspace/repos/:id/overview.
type repoOverviewWorkspaceStore interface {
	RepoByID(ctx context.Context, id uuid.UUID) (*db.Repo, error)
}

// repoOverviewGTDStore covers the gtd.StoreIface methods needed by the
// repo-overview endpoint: project lookup (legacy by-name + new
// repo-name binding), pending+completed tasks, and recent activity.
type repoOverviewGTDStore interface {
	ProjectByName(ctx context.Context, name string) (*db.Project, error)
	// ProjectsByRepoName returns every project bound to the given repo via the
	// new projects.repo_name column (migration 000037). Empty slice = no
	// project paired (not an error).
	ProjectsByRepoName(ctx context.Context, repoName string) ([]db.Project, error)
	Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error)
	RecentCompletedTasks(ctx context.Context, projectID uuid.UUID, limit int32) ([]db.Task, error)
	RecentActivityByProject(ctx context.Context, projectID uuid.UUID, since time.Time, maxRows int32) ([]db.ActivityLog, error)
}

// repoOverviewDecisionStore covers the decision.StoreIface methods needed by
// the repo-overview endpoint.
type repoOverviewDecisionStore interface {
	ByRepo(ctx context.Context, repoName string, limit int32) ([]db.Decision, error)
}

// repoOverviewSessionStore covers the session.StoreIface methods needed by
// the repo-overview endpoint.
type repoOverviewSessionStore interface {
	HandoffsByRepo(ctx context.Context, repoName string, limit int) ([]db.SessionHandoff, error)
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
	UpdateEmbeddingByID(ctx context.Context, id uuid.UUID, embedding []byte) error
}

// knowledgeStore covers the subset of knowledge.Store used by handlers.
type knowledgeStore interface {
	AddItem(ctx context.Context, p knowledge.AddItemParams) (*db.KnowledgeItem, error)
	Search(ctx context.Context, query string, limit int) ([]db.KnowledgeItem, error)
	List(ctx context.Context, limit, offset int) ([]db.KnowledgeItem, error)
	GetByID(ctx context.Context, id uuid.UUID) (*db.KnowledgeItem, error)
	UpdateLearningValue(ctx context.Context, id uuid.UUID, value int) error
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
	ReviewHistory(ctx context.Context) ([]learning.ConceptHistoryRow, error)
	LearningStats(ctx context.Context) (*learning.LearningStatsResult, error)
}

// autologGTDStore covers the subset of gtd.StoreIface used by AutologHandler.
type autologGTDStore interface {
	LogActivity(ctx context.Context, actor, action string, projectID *uuid.UUID, notes string) error
	Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error)
	CreateTask(ctx context.Context, p gtd.CreateTaskParams) (*db.Task, error)
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

// searchKnowledgeStore covers the subset of knowledge.Store used by SearchHandler.
type searchKnowledgeStore interface {
	Search(ctx context.Context, query string, limit int) ([]db.KnowledgeItem, error)
}

// searchDecisionStore covers the subset of decision.Store used by SearchHandler.
type searchDecisionStore interface {
	All(ctx context.Context, limit int32) ([]db.Decision, error)
}

// searchGTDStore covers the subset of gtd.Store used by SearchHandler.
type searchGTDStore interface {
	Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error)
}

// activityClassifier is the narrow interface AutologHandler needs from ActivityClassifier.
// *ai.ActivityClassifier satisfies this interface.
type activityClassifier interface {
	Classify(ctx context.Context, actor, action, notes string) ai.ClassifyResult
}

// visionStore covers the subset of vision.StoreIface used by VisionHandler.
type visionStore interface {
	Add(ctx context.Context, p vision.AddVisionParams) (*vision.VisionItem, error)
	List(ctx context.Context, filter vision.ListVisionFilter) ([]vision.VisionItemSummary, error)
	Update(ctx context.Context, id uuid.UUID, p vision.UpdateVisionParams) (*vision.VisionItem, error)
}

// timelineAggregator is the narrow interface TimelineHandler needs.
// *timeline.Aggregator satisfies this interface.
type timelineAggregator interface {
	Aggregate(ctx context.Context, from, to time.Time) ([]timeline.Event, error)
}

// errResp returns a standard error response body.
func errResp(msg string) map[string]string {
	return map[string]string{"error": msg}
}

// statusAll is the canonical query-param value that opts into "no status
// filter". Used by both ListProposals (?status=all) and ListProjectTasks
// (?status=all); kept as a single constant so the wire contract stays
// consistent across endpoints.
const statusAll = "all"
