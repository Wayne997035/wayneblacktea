package mcp

// no-op StoreIface fakes for assemble_context handler tests.
//
// newContextPackTestServer used to wire NewAssembler with 11 literal nils —
// safe back when internal/contextpack's retrieve()/score()/trimToBudget()
// were stubs that never dereferenced the store fields. Now that retrieval.go
// is a real implementation (internal/contextpack is owned by another
// engineer in this sprint), a.gtd.WorkspaceID() is called unconditionally on
// every Assemble(), and a literal nil interface panics on first method call
// regardless of which method — every store field needs a working (even if
// no-op) implementation.
//
// These handler tests only assert on the shape of the emitted JSON envelope,
// not on retrieval behavior — that is retrieval_test.go's job, in package
// contextpack, using its own (unexported, same-package) fakes. Duplicating
// those here isn't possible across package boundaries, so this file is a
// deliberately minimal, all-zero-value counterpart: every method returns a
// nil/zero value and no error, except where retrieval.go dereferences the
// returned pointer without a nil-check on success (GetTaskByID,
// GetProjectByID, session.LatestHandoff) — those return a not-found error
// instead, matching the real stores' documented contract.

import (
	"context"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---------------------------------------------------------------------------
// noopGTDStore
// ---------------------------------------------------------------------------

type noopGTDStore struct{}

var _ gtd.StoreIface = noopGTDStore{}

func (noopGTDStore) ListActiveProjects(context.Context) ([]db.Project, error) { return nil, nil }
func (noopGTDStore) GetProjectByID(context.Context, uuid.UUID) (*db.Project, error) {
	return nil, gtd.ErrNotFound
}
func (noopGTDStore) ProjectByName(context.Context, string) (*db.Project, error) { return nil, nil }
func (noopGTDStore) ProjectsByRepoName(context.Context, string) ([]db.Project, error) {
	return nil, nil
}

func (noopGTDStore) CreateProject(context.Context, gtd.CreateProjectParams) (*db.Project, error) {
	return nil, nil
}
func (noopGTDStore) Tasks(context.Context, *uuid.UUID) ([]db.Task, error) { return nil, nil }
func (noopGTDStore) TasksFiltered(context.Context, gtd.TaskFilter) ([]db.Task, error) {
	return nil, nil
}

func (noopGTDStore) TasksByProjectAllStatuses(context.Context, uuid.UUID) ([]db.Task, error) {
	return nil, nil
}

func (noopGTDStore) TasksByDueDateRange(context.Context, time.Time, time.Time) ([]db.Task, error) {
	return nil, nil
}

func (noopGTDStore) UpcomingTasks(context.Context, time.Time, int, int) ([]db.Task, error) {
	return nil, nil
}

func (noopGTDStore) PullForwardTasks(context.Context, time.Time) ([]db.Task, error) {
	return nil, nil
}

func (noopGTDStore) TasksForTimeline(context.Context, time.Time, time.Time) ([]db.Task, error) {
	return nil, nil
}

func (noopGTDStore) CreateTask(context.Context, gtd.CreateTaskParams) (*db.Task, error) {
	return nil, nil
}

func (noopGTDStore) CompleteTask(context.Context, uuid.UUID, *string) (*db.Task, error) {
	return nil, nil
}

func (noopGTDStore) BatchCompleteTasksByPRMatch(context.Context, []gtd.Match) (map[uuid.UUID]bool, error) {
	return nil, nil
}

func (noopGTDStore) BeginTask(context.Context, uuid.UUID) (*db.Task, error) {
	return nil, nil
}

func (noopGTDStore) LogActivity(context.Context, string, string, *uuid.UUID, string) error {
	return nil
}

func (noopGTDStore) ListActivityLogsSince(context.Context, time.Time, int32) ([]db.ActivityLog, error) {
	return nil, nil
}
func (noopGTDStore) ActiveGoals(context.Context) ([]db.Goal, error) { return nil, nil }
func (noopGTDStore) CreateGoal(context.Context, gtd.CreateGoalParams) (*db.Goal, error) {
	return nil, nil
}

func (noopGTDStore) UpdateTaskStatus(context.Context, uuid.UUID, gtd.TaskStatus) (*db.Task, error) {
	return nil, nil
}

func (noopGTDStore) UpdateTask(context.Context, uuid.UUID, gtd.UpdateTaskParams) (*db.Task, error) {
	return nil, nil
}

func (noopGTDStore) GetTaskByID(context.Context, uuid.UUID) (*db.Task, error) {
	return nil, gtd.ErrNotFound
}

func (noopGTDStore) UpdateProjectStatus(context.Context, uuid.UUID, gtd.ProjectStatus) (*db.Project, error) {
	return nil, nil
}

func (noopGTDStore) UpdateGoal(context.Context, uuid.UUID, gtd.UpdateGoalParams) (*db.Goal, error) {
	return nil, nil
}

func (noopGTDStore) UpdateProject(context.Context, uuid.UUID, gtd.UpdateProjectParams) (*db.Project, error) {
	return nil, nil
}
func (noopGTDStore) DeleteTask(context.Context, uuid.UUID) error          { return nil }
func (noopGTDStore) WeeklyProgress(context.Context) (int64, int64, error) { return 0, 0, nil }
func (noopGTDStore) PruneOlderThan(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (noopGTDStore) AddChecklistItem(context.Context, uuid.UUID, uuid.UUID, gtd.ChecklistItem) ([]gtd.ChecklistItem, error) {
	return nil, nil
}

func (noopGTDStore) UpdateChecklistItem(
	context.Context, uuid.UUID, uuid.UUID, uuid.UUID, gtd.UpdateChecklistItemParams,
) ([]gtd.ChecklistItem, error) {
	return nil, nil
}

func (noopGTDStore) DeleteChecklistItem(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}
func (noopGTDStore) TopPendingTask(context.Context) (*db.Task, error) { return nil, nil }
func (noopGTDStore) RecentCompletedTasks(context.Context, uuid.UUID, int32) ([]db.Task, error) {
	return nil, nil
}

func (noopGTDStore) RecentActivityByProject(context.Context, uuid.UUID, time.Time, int32) ([]db.ActivityLog, error) {
	return nil, nil
}

// WorkspaceID returns an invalid pgtype.UUID (unscoped), matching how a
// fresh/unconfigured workspace behaves — retrieve() treats this the same as
// "no workspace filter" rather than an error.
func (noopGTDStore) WorkspaceID() pgtype.UUID { return pgtype.UUID{} }

// ---------------------------------------------------------------------------
// noopDecisionStore
// ---------------------------------------------------------------------------

type noopDecisionStore struct{}

var _ decision.StoreIface = noopDecisionStore{}

func (noopDecisionStore) Log(context.Context, decision.LogParams) (*db.Decision, error) {
	return nil, nil
}

func (noopDecisionStore) ByRepo(context.Context, string, int32) ([]db.Decision, error) {
	return nil, nil
}
func (noopDecisionStore) All(context.Context, int32) ([]db.Decision, error) { return nil, nil }
func (noopDecisionStore) ByProject(context.Context, uuid.UUID, int32) ([]db.Decision, error) {
	return nil, nil
}

func (noopDecisionStore) ByTask(context.Context, uuid.UUID, int32) ([]db.Decision, error) {
	return nil, nil
}

func (noopDecisionStore) SearchByCosine(context.Context, []float32, int) ([]db.Decision, error) {
	return nil, nil
}

func (noopDecisionStore) List(context.Context, decision.ListParams) ([]db.Decision, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// noopKnowledgeStore
// ---------------------------------------------------------------------------

type noopKnowledgeStore struct{}

var _ knowledge.StoreIface = noopKnowledgeStore{}

func (noopKnowledgeStore) AddItem(context.Context, knowledge.AddItemParams) (*db.KnowledgeItem, error) {
	return nil, nil
}

func (noopKnowledgeStore) Search(context.Context, string, int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (noopKnowledgeStore) SearchReadOnly(context.Context, string, int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (noopKnowledgeStore) SearchCoarse(context.Context, string, int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (noopKnowledgeStore) List(context.Context, int, int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (noopKnowledgeStore) GetByID(context.Context, uuid.UUID) (*db.KnowledgeItem, error) {
	return nil, nil
}
func (noopKnowledgeStore) UpdateLearningValue(context.Context, uuid.UUID, int) error { return nil }
func (noopKnowledgeStore) SearchByCosine(context.Context, []float32, int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (noopKnowledgeStore) ListChildren(context.Context, uuid.UUID) ([]*db.KnowledgeItem, error) {
	return nil, nil
}

func (noopKnowledgeStore) ListRoots(context.Context) ([]*db.KnowledgeItem, error) { return nil, nil }

func (noopKnowledgeStore) ListByProjectID(context.Context, uuid.UUID, int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (noopKnowledgeStore) ListByTaskID(context.Context, uuid.UUID, int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// noopAtomStore
// ---------------------------------------------------------------------------

type noopAtomStore struct{}

var _ atom.StoreIface = noopAtomStore{}

func (noopAtomStore) AddAtom(context.Context, atom.AddAtomParams) (*atom.Atom, error) {
	return nil, nil
}
func (noopAtomStore) AddLink(context.Context, atom.AddLinkParams) error { return nil }
func (noopAtomStore) ListByParent(context.Context, string, uuid.UUID) ([]atom.Atom, error) {
	return nil, nil
}

func (noopAtomStore) Traverse(context.Context, uuid.UUID, int) (*atom.TraverseResult, error) {
	return nil, nil
}

func (noopAtomStore) Search(context.Context, *uuid.UUID, string, int) ([]atom.Atom, error) {
	return nil, nil
}

func (noopAtomStore) PruneAtoms(context.Context, time.Time) (int64, error) { return 0, nil }

func (noopAtomStore) SetDigestStatus(context.Context, uuid.UUID, string, string) error { return nil }

func (noopAtomStore) CountByDigestStatus(context.Context, *uuid.UUID, string) (int64, error) {
	return 0, nil
}

func (noopAtomStore) ListByDigestStatus(context.Context, *uuid.UUID, string, int) ([]atom.Atom, error) {
	return nil, nil
}
func (noopAtomStore) CountTotal(context.Context, *uuid.UUID) (int64, error) { return 0, nil }

// ---------------------------------------------------------------------------
// noopProceduralStore
// ---------------------------------------------------------------------------

type noopProceduralStore struct{}

var _ procedural.StoreIface = noopProceduralStore{}

func (noopProceduralStore) Add(context.Context, procedural.AddParams) (*procedural.ProceduralMemory, error) {
	return nil, nil
}

func (noopProceduralStore) Query(context.Context, procedural.QueryFilter) ([]procedural.ProceduralMemory, error) {
	return nil, nil
}

func (noopProceduralStore) MarkUsed(context.Context, uuid.UUID) (*procedural.ProceduralMemory, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// noopSkillStore
// ---------------------------------------------------------------------------

type noopSkillStore struct{}

var _ skill.StoreIface = noopSkillStore{}

func (noopSkillStore) Add(context.Context, skill.AddParams) (*skill.Skill, error) { return nil, nil }

func (noopSkillStore) Search(context.Context, skill.SearchFilter) ([]*skill.Skill, error) {
	return nil, nil
}

func (noopSkillStore) IncrementSuccess(context.Context, string, *string) (*skill.Skill, error) {
	return nil, nil
}

func (noopSkillStore) UpdateFromOutcome(context.Context, skill.UpdateFromOutcomeParams, *string) (*skill.Skill, error) {
	return nil, nil
}

func (noopSkillStore) ListRelevant(context.Context, *string, string, int) ([]*skill.Skill, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// noopOutcomeStore
// ---------------------------------------------------------------------------

type noopOutcomeStore struct{}

var _ outcome.StoreIface = noopOutcomeStore{}

func (noopOutcomeStore) CreateOutcome(context.Context, outcome.CreateOutcomeParams) (outcome.Outcome, error) {
	return outcome.Outcome{}, nil
}

func (noopOutcomeStore) GetOutcomeByID(context.Context, uuid.UUID, *uuid.UUID) (outcome.Outcome, error) {
	return outcome.Outcome{}, nil
}

func (noopOutcomeStore) ListRecentOutcomes(context.Context, *uuid.UUID, string, int) ([]outcome.Outcome, error) {
	return nil, nil
}

func (noopOutcomeStore) CreateEvaluation(context.Context, outcome.CreateEvaluationParams) (outcome.Evaluation, error) {
	return outcome.Evaluation{}, nil
}

func (noopOutcomeStore) ListEvaluationsByOutcomeID(context.Context, uuid.UUID, *uuid.UUID) ([]outcome.Evaluation, error) {
	return nil, nil
}

func (noopOutcomeStore) ListFailedOutcomes(context.Context, *uuid.UUID, int) ([]outcome.Outcome, error) {
	return nil, nil
}
func (noopOutcomeStore) PruneOlderThan(context.Context, time.Time) (int64, error) { return 0, nil }

func (noopOutcomeStore) ExistsForEntity(context.Context, *uuid.UUID, string, uuid.UUID) (bool, error) {
	return false, nil
}

// ---------------------------------------------------------------------------
// noopReflectionStore
// ---------------------------------------------------------------------------

type noopReflectionStore struct{}

var _ reflection.StoreIface = noopReflectionStore{}

func (noopReflectionStore) Create(context.Context, reflection.CreateParams) (*reflection.Reflection, error) {
	return nil, nil
}

func (noopReflectionStore) List(context.Context, reflection.ListParams) ([]*reflection.Reflection, error) {
	return nil, nil
}

func (noopReflectionStore) GetLatest(context.Context, *uuid.UUID, string) (*reflection.Reflection, error) {
	return nil, nil
}

func (noopReflectionStore) ByRelatedEntity(context.Context, *uuid.UUID, string, uuid.UUID, int) ([]*reflection.Reflection, error) {
	return nil, nil
}

func (noopReflectionStore) RecentWithPatterns(context.Context, *uuid.UUID, time.Time, int) ([]*reflection.Reflection, error) {
	return nil, nil
}

func (noopReflectionStore) PruneOlderThan(context.Context, time.Time) (int64, error) { return 0, nil }

// ---------------------------------------------------------------------------
// noopBehaviorRuleStore
// ---------------------------------------------------------------------------

type noopBehaviorRuleStore struct{}

var _ behaviorrule.StoreIface = noopBehaviorRuleStore{}

func (noopBehaviorRuleStore) Propose(context.Context, behaviorrule.CreateParams) (*behaviorrule.BehaviorRule, error) {
	return nil, nil
}

func (noopBehaviorRuleStore) List(context.Context, behaviorrule.ListParams) ([]*behaviorrule.BehaviorRule, error) {
	return nil, nil
}

func (noopBehaviorRuleStore) ApplyOutcome(context.Context, uuid.UUID, string) (*behaviorrule.BehaviorRule, error) {
	return nil, nil
}

func (noopBehaviorRuleStore) Deprecate(context.Context, uuid.UUID) (*behaviorrule.BehaviorRule, error) {
	return nil, nil
}

func (noopBehaviorRuleStore) PruneOlderThan(context.Context, time.Time) (int64, error) {
	return 0, nil
}

// ---------------------------------------------------------------------------
// noopSessionStore
// ---------------------------------------------------------------------------

type noopSessionStore struct{}

var _ session.StoreIface = noopSessionStore{}

func (noopSessionStore) SetHandoff(context.Context, session.HandoffParams) (*db.SessionHandoff, error) {
	return nil, nil
}

func (noopSessionStore) LatestHandoff(context.Context) (*db.SessionHandoff, error) {
	return nil, session.ErrNotFound
}
func (noopSessionStore) Resolve(context.Context, uuid.UUID) error { return nil }
func (noopSessionStore) MarkNextActionDone(context.Context, uuid.UUID, int) (*db.SessionHandoff, error) {
	return nil, nil
}
func (noopSessionStore) UpdateSummary(context.Context, string) error   { return nil }
func (noopSessionStore) UpdateEmbedding(context.Context, []byte) error { return nil }
func (noopSessionStore) UpdateEmbeddingByID(context.Context, uuid.UUID, []byte, string, int) error {
	return nil
}

func (noopSessionStore) SearchByCosine(context.Context, []float32, int) ([]db.SessionHandoff, error) {
	return nil, nil
}

func (noopSessionStore) HandoffsSince(context.Context, time.Time, int) ([]db.SessionHandoff, error) {
	return nil, nil
}

func (noopSessionStore) HandoffsByRepo(context.Context, string, int) ([]db.SessionHandoff, error) {
	return nil, nil
}

func (noopSessionStore) PruneOlderThan(context.Context, time.Time) (int64, error) {
	return 0, nil
}

// ---------------------------------------------------------------------------
// noopWorkSessionStore
// ---------------------------------------------------------------------------

type noopWorkSessionStore struct{}

var _ worksession.StoreIface = noopWorkSessionStore{}

func (noopWorkSessionStore) Create(context.Context, worksession.CreateParams) (*worksession.Session, error) {
	return nil, nil
}

func (noopWorkSessionStore) GetActive(context.Context, uuid.UUID, string) (*worksession.ActiveSessionResult, error) {
	return &worksession.ActiveSessionResult{}, nil
}

func (noopWorkSessionStore) Checkpoint(context.Context, worksession.CheckpointParams) (*worksession.Session, error) {
	return nil, nil
}

func (noopWorkSessionStore) Finish(context.Context, worksession.FinishParams) (*worksession.Session, error) {
	return nil, nil
}

func (noopWorkSessionStore) GetByID(context.Context, uuid.UUID, uuid.UUID) (*worksession.Session, error) {
	return nil, nil
}

func (noopWorkSessionStore) LinkTask(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}

func (noopWorkSessionStore) LinkedTasks(context.Context, uuid.UUID) ([]worksession.SessionTask, error) {
	return nil, nil
}

func (noopWorkSessionStore) ListRecent(context.Context, uuid.UUID, string, int) ([]worksession.Session, error) {
	return nil, nil
}

func (noopWorkSessionStore) AddEvidence(context.Context, worksession.Evidence) (*worksession.Evidence, error) {
	return nil, nil
}

func (noopWorkSessionStore) GetEvidence(context.Context, uuid.UUID) ([]worksession.Evidence, error) {
	return nil, nil
}

func (noopWorkSessionStore) SetOutcomeLink(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (noopWorkSessionStore) PruneOlderThan(context.Context, time.Time) (int64, error) {
	return 0, nil
}
