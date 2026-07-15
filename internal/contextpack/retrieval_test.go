package contextpack

import (
	"context"
	"errors"
	"strings"
	"testing"
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
// fake gtd.StoreIface — only GetTaskByID/GetProjectByID/ProjectsByRepoName/
// WorkspaceID are exercised by retrieve(); the rest are unused stubs.
// ---------------------------------------------------------------------------

type fakeGTDStore struct {
	workspaceID uuid.UUID

	taskByID *db.Task
	taskErr  error

	projectByID *db.Project
	projectErr  error

	projectsByRepo    []db.Project
	projectsByRepoErr error
}

var _ gtd.StoreIface = (*fakeGTDStore)(nil)

func (f *fakeGTDStore) WorkspaceID() pgtype.UUID {
	return pgtype.UUID{Bytes: f.workspaceID, Valid: true}
}

func (f *fakeGTDStore) GetTaskByID(_ context.Context, _ uuid.UUID) (*db.Task, error) {
	if f.taskByID != nil {
		return f.taskByID, nil
	}
	if f.taskErr != nil {
		return nil, f.taskErr
	}
	return nil, gtd.ErrNotFound
}

func (f *fakeGTDStore) GetProjectByID(_ context.Context, _ uuid.UUID) (*db.Project, error) {
	if f.projectByID != nil {
		return f.projectByID, nil
	}
	if f.projectErr != nil {
		return nil, f.projectErr
	}
	return nil, gtd.ErrNotFound
}

func (f *fakeGTDStore) ProjectsByRepoName(_ context.Context, _ string) ([]db.Project, error) {
	return f.projectsByRepo, f.projectsByRepoErr
}

func (f *fakeGTDStore) ListActiveProjects(_ context.Context) ([]db.Project, error) { return nil, nil }

func (f *fakeGTDStore) ProjectByName(_ context.Context, _ string) (*db.Project, error) {
	return nil, nil
}

func (f *fakeGTDStore) CreateProject(_ context.Context, _ gtd.CreateProjectParams) (*db.Project, error) {
	return nil, nil
}
func (f *fakeGTDStore) Tasks(_ context.Context, _ *uuid.UUID) ([]db.Task, error) { return nil, nil }
func (f *fakeGTDStore) TasksFiltered(_ context.Context, _ gtd.TaskFilter) ([]db.Task, error) {
	return nil, nil
}

func (f *fakeGTDStore) TasksByProjectAllStatuses(_ context.Context, _ uuid.UUID) ([]db.Task, error) {
	return nil, nil
}

func (f *fakeGTDStore) TasksByDueDateRange(_ context.Context, _, _ time.Time) ([]db.Task, error) {
	return nil, nil
}

func (f *fakeGTDStore) UpcomingTasks(_ context.Context, _ time.Time, _, _ int) ([]db.Task, error) {
	return nil, nil
}

func (f *fakeGTDStore) TasksForTimeline(_ context.Context, _, _ time.Time) ([]db.Task, error) {
	return nil, nil
}

func (f *fakeGTDStore) CreateTask(_ context.Context, _ gtd.CreateTaskParams) (*db.Task, error) {
	return nil, nil
}

func (f *fakeGTDStore) CompleteTask(_ context.Context, _ uuid.UUID, _ *string) (*db.Task, error) {
	return nil, nil
}

func (f *fakeGTDStore) BatchCompleteTasksByPRMatch(_ context.Context, _ []gtd.Match) (map[uuid.UUID]bool, error) {
	return nil, nil
}

func (f *fakeGTDStore) BeginTask(_ context.Context, _ uuid.UUID, _ uuid.UUID) (*db.Task, error) {
	return nil, nil
}

func (f *fakeGTDStore) LogActivity(_ context.Context, _, _ string, _ *uuid.UUID, _ string) error {
	return nil
}

func (f *fakeGTDStore) ListActivityLogsSince(_ context.Context, _ time.Time, _ int32) ([]db.ActivityLog, error) {
	return nil, nil
}
func (f *fakeGTDStore) ActiveGoals(_ context.Context) ([]db.Goal, error) { return nil, nil }
func (f *fakeGTDStore) CreateGoal(_ context.Context, _ gtd.CreateGoalParams) (*db.Goal, error) {
	return nil, nil
}

func (f *fakeGTDStore) UpdateTaskStatus(_ context.Context, _ uuid.UUID, _ gtd.TaskStatus) (*db.Task, error) {
	return nil, nil
}

func (f *fakeGTDStore) UpdateTask(_ context.Context, _ uuid.UUID, _ gtd.UpdateTaskParams) (*db.Task, error) {
	return nil, nil
}

func (f *fakeGTDStore) UpdateProjectStatus(_ context.Context, _ uuid.UUID, _ gtd.ProjectStatus) (*db.Project, error) {
	return nil, nil
}

func (f *fakeGTDStore) UpdateGoal(_ context.Context, _ uuid.UUID, _ gtd.UpdateGoalParams) (*db.Goal, error) {
	return nil, nil
}

func (f *fakeGTDStore) UpdateProject(_ context.Context, _ uuid.UUID, _ gtd.UpdateProjectParams) (*db.Project, error) {
	return nil, nil
}
func (f *fakeGTDStore) DeleteTask(_ context.Context, _ uuid.UUID) error { return nil }
func (f *fakeGTDStore) WeeklyProgress(_ context.Context) (int64, int64, error) {
	return 0, 0, nil
}

func (f *fakeGTDStore) AddChecklistItem(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ gtd.ChecklistItem) ([]gtd.ChecklistItem, error) {
	return nil, nil
}

func (f *fakeGTDStore) UpdateChecklistItem(
	_ context.Context, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID, _ gtd.UpdateChecklistItemParams,
) ([]gtd.ChecklistItem, error) {
	return nil, nil
}

func (f *fakeGTDStore) DeleteChecklistItem(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID) error {
	return nil
}
func (f *fakeGTDStore) TopPendingTask(_ context.Context) (*db.Task, error) { return nil, nil }
func (f *fakeGTDStore) RecentCompletedTasks(_ context.Context, _ uuid.UUID, _ int32) ([]db.Task, error) {
	return nil, nil
}

func (f *fakeGTDStore) RecentActivityByProject(_ context.Context, _ uuid.UUID, _ time.Time, _ int32) ([]db.ActivityLog, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// fake decision.StoreIface
// ---------------------------------------------------------------------------

type fakeDecisionStore struct {
	byRepo    []db.Decision
	byRepoErr error

	byProject    []db.Decision
	byProjectErr error

	byTask    []db.Decision
	byTaskErr error
}

var _ decision.StoreIface = (*fakeDecisionStore)(nil)

func (f *fakeDecisionStore) ByRepo(_ context.Context, _ string, _ int32) ([]db.Decision, error) {
	return f.byRepo, f.byRepoErr
}

func (f *fakeDecisionStore) ByProject(_ context.Context, _ uuid.UUID, _ int32) ([]db.Decision, error) {
	return f.byProject, f.byProjectErr
}

func (f *fakeDecisionStore) ByTask(_ context.Context, _ uuid.UUID, _ int32) ([]db.Decision, error) {
	return f.byTask, f.byTaskErr
}

func (f *fakeDecisionStore) Log(_ context.Context, _ decision.LogParams) (*db.Decision, error) {
	return nil, nil
}

func (f *fakeDecisionStore) All(_ context.Context, _ int32) ([]db.Decision, error) { return nil, nil }

func (f *fakeDecisionStore) SearchByCosine(_ context.Context, _ []float32, _ int) ([]db.Decision, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// fake knowledge.StoreIface
// ---------------------------------------------------------------------------

type fakeKnowledgeStore struct {
	searchResults []db.KnowledgeItem
	searchErr     error
}

var _ knowledge.StoreIface = (*fakeKnowledgeStore)(nil)

func (f *fakeKnowledgeStore) Search(_ context.Context, _ string, _ int) ([]db.KnowledgeItem, error) {
	return f.searchResults, f.searchErr
}

func (f *fakeKnowledgeStore) SearchReadOnly(_ context.Context, _ string, _ int) ([]db.KnowledgeItem, error) {
	return f.searchResults, f.searchErr
}

func (f *fakeKnowledgeStore) AddItem(_ context.Context, _ knowledge.AddItemParams) (*db.KnowledgeItem, error) {
	return nil, nil
}

func (f *fakeKnowledgeStore) SearchCoarse(_ context.Context, _ string, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (f *fakeKnowledgeStore) List(_ context.Context, _, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (f *fakeKnowledgeStore) GetByID(_ context.Context, _ uuid.UUID) (*db.KnowledgeItem, error) {
	return nil, nil
}

func (f *fakeKnowledgeStore) UpdateLearningValue(_ context.Context, _ uuid.UUID, _ int) error {
	return nil
}

func (f *fakeKnowledgeStore) SearchByCosine(_ context.Context, _ []float32, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (f *fakeKnowledgeStore) ListChildren(_ context.Context, _ uuid.UUID) ([]*db.KnowledgeItem, error) {
	return nil, nil
}

func (f *fakeKnowledgeStore) ListRoots(_ context.Context) ([]*db.KnowledgeItem, error) {
	return nil, nil
}

func (f *fakeKnowledgeStore) ListByProjectID(_ context.Context, _ uuid.UUID, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

func (f *fakeKnowledgeStore) ListByTaskID(_ context.Context, _ uuid.UUID, _ int) ([]db.KnowledgeItem, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// fake atom.StoreIface
// ---------------------------------------------------------------------------

type fakeAtomStore struct {
	searchResults []atom.Atom
	searchErr     error
}

var _ atom.StoreIface = (*fakeAtomStore)(nil)

func (f *fakeAtomStore) Search(_ context.Context, _ *uuid.UUID, _ string, _ int) ([]atom.Atom, error) {
	return f.searchResults, f.searchErr
}

func (f *fakeAtomStore) AddAtom(_ context.Context, _ atom.AddAtomParams) (*atom.Atom, error) {
	return nil, nil
}
func (f *fakeAtomStore) AddLink(_ context.Context, _ atom.AddLinkParams) error { return nil }
func (f *fakeAtomStore) ListByParent(_ context.Context, _ string, _ uuid.UUID) ([]atom.Atom, error) {
	return nil, nil
}

func (f *fakeAtomStore) Traverse(_ context.Context, _ uuid.UUID, _ int) (*atom.TraverseResult, error) {
	return nil, nil
}
func (f *fakeAtomStore) PruneAtoms(_ context.Context, _ time.Time) (int64, error) { return 0, nil }
func (f *fakeAtomStore) SetDigestStatus(_ context.Context, _ uuid.UUID, _ string, _ string) error {
	return nil
}

func (f *fakeAtomStore) CountByDigestStatus(_ context.Context, _ *uuid.UUID, _ string) (int64, error) {
	return 0, nil
}

func (f *fakeAtomStore) ListByDigestStatus(_ context.Context, _ *uuid.UUID, _ string, _ int) ([]atom.Atom, error) {
	return nil, nil
}
func (f *fakeAtomStore) CountTotal(_ context.Context, _ *uuid.UUID) (int64, error) { return 0, nil }

// ---------------------------------------------------------------------------
// fake procedural.StoreIface
// ---------------------------------------------------------------------------

type fakeProceduralStore struct {
	queryResults []procedural.ProceduralMemory
	queryErr     error
}

var _ procedural.StoreIface = (*fakeProceduralStore)(nil)

func (f *fakeProceduralStore) Query(_ context.Context, _ procedural.QueryFilter) ([]procedural.ProceduralMemory, error) {
	return f.queryResults, f.queryErr
}

func (f *fakeProceduralStore) Add(_ context.Context, _ procedural.AddParams) (*procedural.ProceduralMemory, error) {
	return nil, nil
}

func (f *fakeProceduralStore) MarkUsed(_ context.Context, _ uuid.UUID) (*procedural.ProceduralMemory, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// fake skill.StoreIface
// ---------------------------------------------------------------------------

type fakeSkillStore struct {
	searchResults []*skill.Skill
	searchErr     error
}

var _ skill.StoreIface = (*fakeSkillStore)(nil)

func (f *fakeSkillStore) Search(_ context.Context, _ skill.SearchFilter) ([]*skill.Skill, error) {
	return f.searchResults, f.searchErr
}

func (f *fakeSkillStore) Add(_ context.Context, _ skill.AddParams) (*skill.Skill, error) {
	return nil, nil
}

func (f *fakeSkillStore) GetByID(_ context.Context, _ string, _ *string) (*skill.Skill, error) {
	return nil, nil
}

func (f *fakeSkillStore) IncrementSuccess(_ context.Context, _ string, _ *string) (*skill.Skill, error) {
	return nil, nil
}

func (f *fakeSkillStore) UpdateFromOutcome(_ context.Context, _ skill.UpdateFromOutcomeParams, _ *string) (*skill.Skill, error) {
	return nil, nil
}

func (f *fakeSkillStore) ListRelevant(_ context.Context, _ *string, _ string, _ int) ([]*skill.Skill, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// fake outcome.StoreIface
// ---------------------------------------------------------------------------

type fakeOutcomeStore struct {
	failedResults []outcome.Outcome
	failedErr     error
}

var _ outcome.StoreIface = (*fakeOutcomeStore)(nil)

func (f *fakeOutcomeStore) ListFailedOutcomes(_ context.Context, _ *uuid.UUID, _ int) ([]outcome.Outcome, error) {
	return f.failedResults, f.failedErr
}

func (f *fakeOutcomeStore) CreateOutcome(_ context.Context, _ outcome.CreateOutcomeParams) (outcome.Outcome, error) {
	return outcome.Outcome{}, nil
}

func (f *fakeOutcomeStore) GetOutcomeByID(_ context.Context, _ uuid.UUID, _ *uuid.UUID) (outcome.Outcome, error) {
	return outcome.Outcome{}, nil
}

func (f *fakeOutcomeStore) ListRecentOutcomes(_ context.Context, _ *uuid.UUID, _ string, _ int) ([]outcome.Outcome, error) {
	return nil, nil
}

func (f *fakeOutcomeStore) CreateEvaluation(_ context.Context, _ outcome.CreateEvaluationParams) (outcome.Evaluation, error) {
	return outcome.Evaluation{}, nil
}

func (f *fakeOutcomeStore) ListEvaluationsByOutcomeID(_ context.Context, _ uuid.UUID, _ *uuid.UUID) ([]outcome.Evaluation, error) {
	return nil, nil
}

func (f *fakeOutcomeStore) PruneOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// ---------------------------------------------------------------------------
// fake reflection.StoreIface
// ---------------------------------------------------------------------------

type fakeReflectionStore struct {
	recent    []*reflection.Reflection
	recentErr error
}

var _ reflection.StoreIface = (*fakeReflectionStore)(nil)

func (f *fakeReflectionStore) RecentWithPatterns(_ context.Context, _ *uuid.UUID, _ time.Time, _ int) ([]*reflection.Reflection, error) {
	return f.recent, f.recentErr
}

func (f *fakeReflectionStore) Create(_ context.Context, _ reflection.CreateParams) (*reflection.Reflection, error) {
	return nil, nil
}

func (f *fakeReflectionStore) List(_ context.Context, _ reflection.ListParams) ([]*reflection.Reflection, error) {
	return nil, nil
}

func (f *fakeReflectionStore) GetLatest(_ context.Context, _ *uuid.UUID, _ string) (*reflection.Reflection, error) {
	return nil, nil
}

func (f *fakeReflectionStore) ByRelatedEntity(
	_ context.Context, _ *uuid.UUID, _ string, _ uuid.UUID, _ int,
) ([]*reflection.Reflection, error) {
	return nil, nil
}

func (f *fakeReflectionStore) PruneOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// ---------------------------------------------------------------------------
// fake behaviorrule.StoreIface — List filters by Status like the real store,
// so tests can assert retrieve() actually requests Status="active" only.
// ---------------------------------------------------------------------------

type fakeBehaviorRuleStore struct {
	rules   []*behaviorrule.BehaviorRule
	listErr error
}

var _ behaviorrule.StoreIface = (*fakeBehaviorRuleStore)(nil)

func (f *fakeBehaviorRuleStore) List(_ context.Context, p behaviorrule.ListParams) ([]*behaviorrule.BehaviorRule, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if p.Status == nil {
		return f.rules, nil
	}
	var out []*behaviorrule.BehaviorRule
	for _, r := range f.rules {
		if r.Status == *p.Status {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeBehaviorRuleStore) Propose(_ context.Context, _ behaviorrule.CreateParams) (*behaviorrule.BehaviorRule, error) {
	return nil, nil
}

func (f *fakeBehaviorRuleStore) GetByID(_ context.Context, _ uuid.UUID) (*behaviorrule.BehaviorRule, error) {
	return nil, nil
}

func (f *fakeBehaviorRuleStore) ApplyOutcome(_ context.Context, _ uuid.UUID, _ string) (*behaviorrule.BehaviorRule, error) {
	return nil, nil
}

func (f *fakeBehaviorRuleStore) Deprecate(_ context.Context, _ uuid.UUID) (*behaviorrule.BehaviorRule, error) {
	return nil, nil
}

func (f *fakeBehaviorRuleStore) PruneOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// ---------------------------------------------------------------------------
// fake session.StoreIface — LatestHandoff defaults to ErrNotFound, mirroring
// the real store's contract when no unresolved handoff exists.
// ---------------------------------------------------------------------------

type fakeSessionStore struct {
	latestHandoff *db.SessionHandoff
	latestErr     error
}

var _ session.StoreIface = (*fakeSessionStore)(nil)

func (f *fakeSessionStore) LatestHandoff(_ context.Context) (*db.SessionHandoff, error) {
	if f.latestHandoff != nil {
		return f.latestHandoff, nil
	}
	if f.latestErr != nil {
		return nil, f.latestErr
	}
	return nil, session.ErrNotFound
}

func (f *fakeSessionStore) SetHandoff(_ context.Context, _ session.HandoffParams) (*db.SessionHandoff, error) {
	return nil, nil
}
func (f *fakeSessionStore) Resolve(_ context.Context, _ uuid.UUID) error { return nil }
func (f *fakeSessionStore) MarkNextActionDone(_ context.Context, _ uuid.UUID, _ int) (*db.SessionHandoff, error) {
	return nil, nil
}
func (f *fakeSessionStore) UpdateSummary(_ context.Context, _ string) error   { return nil }
func (f *fakeSessionStore) UpdateEmbedding(_ context.Context, _ []byte) error { return nil }
func (f *fakeSessionStore) UpdateEmbeddingByID(_ context.Context, _ uuid.UUID, _ []byte, _ string, _ int) error {
	return nil
}

func (f *fakeSessionStore) SearchByCosine(_ context.Context, _ []float32, _ int) ([]db.SessionHandoff, error) {
	return nil, nil
}

func (f *fakeSessionStore) HandoffsSince(_ context.Context, _ time.Time, _ int) ([]db.SessionHandoff, error) {
	return nil, nil
}

func (f *fakeSessionStore) HandoffsByRepo(_ context.Context, _ string, _ int) ([]db.SessionHandoff, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// fake worksession.StoreIface — GetActive defaults to Active:false, mirroring
// the real store's "no in_progress session" contract.
// ---------------------------------------------------------------------------

type fakeWorkSessionStore struct {
	active    *worksession.ActiveSessionResult
	activeErr error
}

var _ worksession.StoreIface = (*fakeWorkSessionStore)(nil)

func (f *fakeWorkSessionStore) GetActive(_ context.Context, _ uuid.UUID, _ string) (*worksession.ActiveSessionResult, error) {
	if f.activeErr != nil {
		return nil, f.activeErr
	}
	if f.active != nil {
		return f.active, nil
	}
	return &worksession.ActiveSessionResult{}, nil
}

func (f *fakeWorkSessionStore) Create(_ context.Context, _ worksession.CreateParams) (*worksession.Session, error) {
	return nil, nil
}

func (f *fakeWorkSessionStore) Checkpoint(_ context.Context, _ worksession.CheckpointParams) (*worksession.Session, error) {
	return nil, nil
}

func (f *fakeWorkSessionStore) Finish(_ context.Context, _ worksession.FinishParams) (*worksession.Session, error) {
	return nil, nil
}

func (f *fakeWorkSessionStore) GetByID(_ context.Context, _, _ uuid.UUID) (*worksession.Session, error) {
	return nil, nil
}

func (f *fakeWorkSessionStore) LinkTask(_ context.Context, _, _ uuid.UUID, _ string) error {
	return nil
}

func (f *fakeWorkSessionStore) LinkedTasks(_ context.Context, _ uuid.UUID) ([]worksession.SessionTask, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// test harness
// ---------------------------------------------------------------------------

// fakes bundles one fake per Assembler field so each test only has to set
// the handful of return values it cares about.
type fakes struct {
	gtd         *fakeGTDStore
	decision    *fakeDecisionStore
	knowledge   *fakeKnowledgeStore
	atom        *fakeAtomStore
	procedural  *fakeProceduralStore
	skill       *fakeSkillStore
	outcome     *fakeOutcomeStore
	reflection  *fakeReflectionStore
	rule        *fakeBehaviorRuleStore
	session     *fakeSessionStore
	workSession *fakeWorkSessionStore
}

func newFakes() *fakes {
	return &fakes{
		gtd:         &fakeGTDStore{workspaceID: uuid.New()},
		decision:    &fakeDecisionStore{},
		knowledge:   &fakeKnowledgeStore{},
		atom:        &fakeAtomStore{},
		procedural:  &fakeProceduralStore{},
		skill:       &fakeSkillStore{},
		outcome:     &fakeOutcomeStore{},
		reflection:  &fakeReflectionStore{},
		rule:        &fakeBehaviorRuleStore{},
		session:     &fakeSessionStore{},
		workSession: &fakeWorkSessionStore{},
	}
}

func (f *fakes) assembler() *Assembler {
	// newFakes() always allocates every field, so NewAssembler's nil-guard
	// can never fire here — panic (rather than silently swallowing) if that
	// assumption ever breaks.
	a, err := NewAssembler(f.gtd, f.decision, f.knowledge, f.atom, f.procedural, f.skill,
		f.outcome, f.reflection, f.rule, f.session, f.workSession)
	if err != nil {
		panic(err)
	}
	return a
}

func findItem(items []Item, typ string, id uuid.UUID) *Item {
	for i := range items {
		if items[i].Type == typ && items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

func TestRetrieveArchivedKnowledgeExcluded(t *testing.T) {
	f := newFakes()
	activeID, archivedID := uuid.New(), uuid.New()
	f.knowledge.searchResults = []db.KnowledgeItem{
		{ID: activeID, Title: "keep me", Content: "c", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
		{
			ID: archivedID, Title: "drop me", Content: "c",
			CreatedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
			ArchivedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{Objective: "test"})

	if findItem(items, TypeKnowledge, activeID) == nil {
		t.Errorf("expected active knowledge item %s to be present", activeID)
	}
	if findItem(items, TypeKnowledge, archivedID) != nil {
		t.Errorf("expected archived knowledge item %s to be excluded", archivedID)
	}
}

func TestRetrieveNonActiveBehaviorRuleExcluded(t *testing.T) {
	f := newFakes()
	activeID, proposedID, deprecatedID := uuid.New(), uuid.New(), uuid.New()
	f.rule.rules = []*behaviorrule.BehaviorRule{
		{ID: activeID, Condition: "c", Action: "a", Status: "active", CreatedAt: time.Now()},
		{ID: proposedID, Condition: "c", Action: "a", Status: "proposed", CreatedAt: time.Now()},
		{ID: deprecatedID, Condition: "c", Action: "a", Status: "deprecated", CreatedAt: time.Now()},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{Objective: "test"})

	if findItem(items, "rule", activeID) == nil {
		t.Errorf("expected active rule %s to be present", activeID)
	}
	if findItem(items, "rule", proposedID) != nil {
		t.Errorf("expected proposed rule %s to be excluded", proposedID)
	}
	if findItem(items, "rule", deprecatedID) != nil {
		t.Errorf("expected deprecated rule %s to be excluded", deprecatedID)
	}
}

func TestRetrieveCurrentTaskAlwaysIncluded(t *testing.T) {
	f := newFakes()
	taskID := uuid.New()
	f.gtd.taskByID = &db.Task{
		ID: taskID, Title: "unrelated to objective", Status: "in_progress",
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{
		Objective: "something completely different that would never keyword-match",
		TaskID:    &taskID,
	})

	if findItem(items, "task", taskID) == nil {
		t.Errorf("expected current task %s to always be included, regardless of objective relevance", taskID)
	}
}

func TestRetrieveCurrentProjectAlwaysIncluded(t *testing.T) {
	f := newFakes()
	projectID := uuid.New()
	f.gtd.projectByID = &db.Project{ID: projectID, Title: "p", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}

	items, _ := f.assembler().retrieve(context.Background(), Request{
		Objective: "irrelevant",
		ProjectID: &projectID,
	})

	if findItem(items, "project", projectID) == nil {
		t.Errorf("expected current project %s to always be included", projectID)
	}
}

func TestRetrieveSingleSourceErrorNonFatal(t *testing.T) {
	f := newFakes()
	f.atom.searchErr = errors.New("atom store unavailable")
	knowledgeID := uuid.New()
	f.knowledge.searchResults = []db.KnowledgeItem{
		{ID: knowledgeID, Title: "still here", Content: "c", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{Objective: "test"})

	if findItem(items, "atom", uuid.Nil) != nil {
		t.Errorf("did not expect any atom items when atom.Search errored")
	}
	for _, it := range items {
		if it.Type == "atom" {
			t.Errorf("did not expect any atom items when atom.Search errored, got %+v", it)
		}
	}
	if findItem(items, TypeKnowledge, knowledgeID) == nil {
		t.Errorf("expected knowledge source to still return results despite the atom source erroring")
	}
}

func TestRetrieveProvenanceSourceTableSetOnEveryItem(t *testing.T) {
	f := newFakes()
	taskID := uuid.New()
	f.gtd.taskByID = &db.Task{ID: taskID, Title: "t", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}
	f.knowledge.searchResults = []db.KnowledgeItem{
		{ID: uuid.New(), Title: "k", Content: "c", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	}
	f.decision.byRepo = []db.Decision{
		{ID: uuid.New(), Title: "d", Decision: "do it", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	}
	f.rule.rules = []*behaviorrule.BehaviorRule{
		{ID: uuid.New(), Condition: "c", Action: "a", Status: "active", CreatedAt: time.Now()},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{Objective: "test", RepoName: testRepoName, TaskID: &taskID})

	if len(items) == 0 {
		t.Fatal("expected at least one item")
	}
	for _, it := range items {
		if it.Provenance == nil || it.Provenance["source_table"] == "" {
			t.Errorf("item %s (%s) missing Provenance[source_table]", it.Type, it.ID)
		}
	}
}

func TestRetrieveRecentFlag(t *testing.T) {
	f := newFakes()
	recentID, staleID := uuid.New(), uuid.New()
	f.knowledge.searchResults = []db.KnowledgeItem{
		{ID: recentID, Title: "recent", Content: "c", CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-24 * time.Hour), Valid: true}},
		{ID: staleID, Title: "stale", Content: "c", CreatedAt: pgtype.Timestamptz{Time: time.Now().Add(-200 * 24 * time.Hour), Valid: true}},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{Objective: "test"})

	recent := findItem(items, TypeKnowledge, recentID)
	if recent == nil {
		t.Fatalf("expected recent knowledge item %s to be present", recentID)
	}
	if recent.Provenance["recent"] != provTrue {
		t.Errorf("recent item: Provenance[recent] = %q, want \"true\"", recent.Provenance["recent"])
	}

	stale := findItem(items, TypeKnowledge, staleID)
	if stale == nil {
		t.Fatalf("expected stale knowledge item %s to be present", staleID)
	}
	if stale.Provenance["recent"] != "false" {
		t.Errorf("stale item: Provenance[recent] = %q, want \"false\"", stale.Provenance["recent"])
	}
}

// ---------------------------------------------------------------------------
// warnings (P1 review finding D)
// ---------------------------------------------------------------------------

func TestRetrieveStoreErrorSurfacedAsWarning(t *testing.T) {
	f := newFakes()
	f.atom.searchErr = errors.New("atom store unavailable")

	_, warnings := f.assembler().retrieve(context.Background(), Request{Objective: "test"})

	found := false
	for _, w := range warnings {
		if w.Type == "store_error" && strings.Contains(w.Summary, "atom.Search") && strings.Contains(w.Summary, "atom store unavailable") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a Warning mentioning atom.Search, got %+v", warnings)
	}
}

func TestRetrieveNoStoreErrorsProducesNoWarnings(t *testing.T) {
	f := newFakes()
	_, warnings := f.assembler().retrieve(context.Background(), Request{Objective: "test"})
	if len(warnings) != 0 {
		t.Errorf("expected no warnings when every store succeeds, got %+v", warnings)
	}
}

func TestRetrieveNotFoundIsNotAWarning(t *testing.T) {
	// gtd.ErrNotFound (stale task_id) and session.ErrNotFound (no unresolved
	// handoff) are expected "nothing here" outcomes, not store failures —
	// they must not pollute Pack.Warnings.
	f := newFakes()
	staleTaskID := uuid.New()
	f.gtd.taskErr = gtd.ErrNotFound

	_, warnings := f.assembler().retrieve(context.Background(), Request{Objective: "test", TaskID: &staleTaskID})
	for _, w := range warnings {
		if strings.Contains(w.Summary, "GetTaskByID") || strings.Contains(w.Summary, "LatestHandoff") {
			t.Errorf("expected ErrNotFound sources to produce no Warning, got %+v", warnings)
		}
	}
}

// ---------------------------------------------------------------------------
// include_types filtering (P1 review finding C)
// ---------------------------------------------------------------------------

func TestRetrieveIncludeTypesNarrowsSources(t *testing.T) {
	f := newFakes()
	knowledgeID, ruleID := uuid.New(), uuid.New()
	f.knowledge.searchResults = []db.KnowledgeItem{
		{ID: knowledgeID, Title: "k", Content: "c", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	}
	f.rule.rules = []*behaviorrule.BehaviorRule{
		{ID: ruleID, Condition: "c", Action: "a", Status: "active", CreatedAt: time.Now()},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{
		Objective: "test", IncludeTypes: []string{"rules"},
	})

	if findItem(items, "rule", ruleID) == nil {
		t.Errorf("expected rule %s present when IncludeTypes=[rules]", ruleID)
	}
	if findItem(items, TypeKnowledge, knowledgeID) != nil {
		t.Errorf("expected knowledge %s excluded when IncludeTypes=[rules]", knowledgeID)
	}
}

func TestRetrieveIncludeTypesEmptyMeansNoRestriction(t *testing.T) {
	f := newFakes()
	knowledgeID := uuid.New()
	f.knowledge.searchResults = []db.KnowledgeItem{
		{ID: knowledgeID, Title: "k", Content: "c", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{Objective: "test"})

	if findItem(items, TypeKnowledge, knowledgeID) == nil {
		t.Errorf("expected knowledge %s present when IncludeTypes is unset", knowledgeID)
	}
}

func TestRetrieveIncludeTypesDoesNotFilterCurrentTaskProject(t *testing.T) {
	f := newFakes()
	taskID := uuid.New()
	f.gtd.taskByID = &db.Task{ID: taskID, Title: "t", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}

	// Current task must survive even though IncludeTypes narrows to a
	// category that doesn't include "task" — retrieveCurrentTaskProject is
	// exempt from include_types filtering.
	items, _ := f.assembler().retrieve(context.Background(), Request{
		Objective: "test", TaskID: &taskID, IncludeTypes: []string{"rules"},
	})

	if findItem(items, "task", taskID) == nil {
		t.Errorf("expected current task %s to survive IncludeTypes narrowing", taskID)
	}
}

// ---------------------------------------------------------------------------
// decision.ByProject / decision.ByTask branches + dedup collision
// (P1 review finding H)
// ---------------------------------------------------------------------------

func TestRetrieveDecisionsByProject(t *testing.T) {
	f := newFakes()
	projectID := uuid.New()
	decisionID := uuid.New()
	f.decision.byProject = []db.Decision{
		{ID: decisionID, Title: "d", Decision: "do it", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{Objective: "test", ProjectID: &projectID})

	if findItem(items, "decision", decisionID) == nil {
		t.Errorf("expected decision %s from decision.ByProject to be present", decisionID)
	}
}

func TestRetrieveDecisionsByTask(t *testing.T) {
	f := newFakes()
	taskID := uuid.New()
	decisionID := uuid.New()
	f.decision.byTask = []db.Decision{
		{ID: decisionID, Title: "d", Decision: "do it", CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{Objective: "test", TaskID: &taskID})

	if findItem(items, "decision", decisionID) == nil {
		t.Errorf("expected decision %s from decision.ByTask to be present", decisionID)
	}
}

func TestRetrieveDedupCollisionAcrossOverlappingQueries(t *testing.T) {
	// The same decision ID returned by both ByRepo and ByProject (a decision
	// tagged with both a repo and a project) must collapse to one Item.
	f := newFakes()
	projectID := uuid.New()
	decisionID := uuid.New()
	shared := db.Decision{
		ID: decisionID, Title: "d", Decision: "do it",
		RepoName:  pgtype.Text{String: testRepoName, Valid: true},
		ProjectID: pgtype.UUID{Bytes: projectID, Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	f.decision.byRepo = []db.Decision{shared}
	f.decision.byProject = []db.Decision{shared}

	items, _ := f.assembler().retrieve(context.Background(), Request{
		Objective: "test", RepoName: testRepoName, ProjectID: &projectID,
	})

	count := 0
	for _, it := range items {
		if it.Type == "decision" && it.ID == decisionID {
			count++
		}
	}
	if count != 1 {
		t.Errorf("decision %s appeared %d times in retrieve() output, want 1 (dedupeItems collapse)", decisionID, count)
	}
}

// ---------------------------------------------------------------------------
// previously-uncovered source converters (P1 review finding H)
// ---------------------------------------------------------------------------

func TestRetrieveAtomConverterEndToEnd(t *testing.T) {
	f := newFakes()
	atomID := uuid.New()
	f.atom.searchResults = []atom.Atom{
		{ID: atomID, Content: "remember this fact", CreatedAt: time.Now().Add(-1 * time.Hour)},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{Objective: "test"})

	it := findItem(items, "atom", atomID)
	if it == nil {
		t.Fatalf("expected atom item %s in retrieve() output", atomID)
	}
	if it.SourceTable != "memory_atoms" {
		t.Errorf("SourceTable = %q, want memory_atoms", it.SourceTable)
	}
	if it.Summary != "remember this fact" {
		t.Errorf("Summary = %q, want %q", it.Summary, "remember this fact")
	}
	if it.Provenance["recent"] != provTrue {
		t.Errorf("Provenance[recent] = %q, want %q (created 1h ago)", it.Provenance["recent"], provTrue)
	}
}

func TestRetrieveProceduralConverterEndToEnd(t *testing.T) {
	f := newFakes()
	procID := uuid.New()
	projectID := uuid.New()
	f.procedural.queryResults = []procedural.ProceduralMemory{
		{
			ID: procID, Title: "how to deploy", WhenToUse: "before a release",
			RepoName: testRepoName, ProjectID: &projectID,
			FilesTouched: []string{"build/Dockerfile", "railway.toml"},
			SuccessCount: 4,
			CreatedAt:    time.Now(),
		},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{Objective: "test"})

	it := findItem(items, "procedure", procID)
	if it == nil {
		t.Fatalf("expected procedure item %s in retrieve() output", procID)
	}
	if it.SourceTable != "procedural_memories" {
		t.Errorf("SourceTable = %q, want procedural_memories", it.SourceTable)
	}
	if it.Summary != "how to deploy — before a release" {
		t.Errorf("Summary = %q, want %q", it.Summary, "how to deploy — before a release")
	}
	if it.Provenance["repo_name"] != testRepoName {
		t.Errorf("Provenance[repo_name] = %q, want wayneblacktea", it.Provenance["repo_name"])
	}
	if it.Provenance["project_id"] != projectID.String() {
		t.Errorf("Provenance[project_id] = %q, want %s", it.Provenance["project_id"], projectID)
	}
	if it.Provenance["files"] != "build/Dockerfile,railway.toml" {
		t.Errorf("Provenance[files] = %q, want %q", it.Provenance["files"], "build/Dockerfile,railway.toml")
	}
	if it.Provenance["success_count"] != "4" {
		t.Errorf("Provenance[success_count] = %q, want 4", it.Provenance["success_count"])
	}
}

func TestRetrieveSkillConverterEndToEnd(t *testing.T) {
	f := newFakes()
	skillID := uuid.New()
	f.skill.searchResults = []*skill.Skill{
		{
			ID: skillID.String(), Name: "deploy-railway", Description: "ship to Railway",
			SuccessCount: 3, FailureCount: 1, CreatedAt: time.Now(),
		},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{Objective: "test"})

	it := findItem(items, "skill", skillID)
	if it == nil {
		t.Fatalf("expected skill item %s in retrieve() output", skillID)
	}
	if it.SourceTable != "skills" {
		t.Errorf("SourceTable = %q, want skills", it.SourceTable)
	}
	if it.Summary != "deploy-railway — ship to Railway" {
		t.Errorf("Summary = %q, want %q", it.Summary, "deploy-railway — ship to Railway")
	}
	if it.Provenance["confidence"] != "0.75" {
		t.Errorf("Provenance[confidence] = %q, want 0.75 (3 success / 4 total)", it.Provenance["confidence"])
	}
}

func TestRetrieveOutcomeConverterEndToEnd(t *testing.T) {
	f := newFakes()
	outcomeID := uuid.New()
	f.outcome.failedResults = []outcome.Outcome{
		{ID: outcomeID, EntityType: "task", Result: "regressed", Notes: "broke on retry", CreatedAt: time.Now()},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{Objective: "test"})

	it := findItem(items, "outcome", outcomeID)
	if it == nil {
		t.Fatalf("expected outcome item %s in retrieve() output", outcomeID)
	}
	if it.SourceTable != "outcomes" {
		t.Errorf("SourceTable = %q, want outcomes", it.SourceTable)
	}
	if it.Summary != "task regressed: broke on retry" {
		t.Errorf("Summary = %q, want %q", it.Summary, "task regressed: broke on retry")
	}
	if it.Provenance["result"] != "regressed" {
		t.Errorf("Provenance[result] = %q, want regressed", it.Provenance["result"])
	}
}

func TestRetrieveReflectionConverterEndToEnd(t *testing.T) {
	f := newFakes()
	reflectionID := uuid.New()
	relatedTaskID := uuid.New()
	taskType := "task"
	f.reflection.recent = []*reflection.Reflection{
		{
			ID: reflectionID, Summary: "retried the same fix twice", CreatedAt: time.Now(),
			RelatedEntityType: &taskType, RelatedEntityID: &relatedTaskID,
		},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{Objective: "test"})

	it := findItem(items, "reflection", reflectionID)
	if it == nil {
		t.Fatalf("expected reflection item %s in retrieve() output", reflectionID)
	}
	if it.SourceTable != "reflections" {
		t.Errorf("SourceTable = %q, want reflections", it.SourceTable)
	}
	if it.Summary != "retried the same fix twice" {
		t.Errorf("Summary = %q, want %q", it.Summary, "retried the same fix twice")
	}
	if it.Provenance["task_id"] != relatedTaskID.String() {
		t.Errorf("Provenance[task_id] = %q, want %s", it.Provenance["task_id"], relatedTaskID)
	}
}

func TestRetrieveWorkSessionConverterEndToEnd(t *testing.T) {
	f := newFakes()
	sessionID := uuid.New()
	projectID := uuid.New()
	taskID := uuid.New()
	f.workSession.active = &worksession.ActiveSessionResult{
		Active: true,
		Session: &worksession.Session{
			ID: sessionID, RepoName: testRepoName, Title: "shipping P1",
			Goal: "fix the review findings", ProjectID: &projectID, CurrentTaskID: &taskID,
			CreatedAt: time.Now().Format(time.RFC3339),
		},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{Objective: "test", RepoName: testRepoName})

	it := findItem(items, "session", sessionID)
	if it == nil {
		t.Fatalf("expected work-session item %s in retrieve() output", sessionID)
	}
	if it.SourceTable != "work_sessions" {
		t.Errorf("SourceTable = %q, want work_sessions", it.SourceTable)
	}
	if it.Summary != "shipping P1 — fix the review findings" {
		t.Errorf("Summary = %q, want %q", it.Summary, "shipping P1 — fix the review findings")
	}
	if it.Provenance["project_id"] != projectID.String() {
		t.Errorf("Provenance[project_id] = %q, want %s", it.Provenance["project_id"], projectID)
	}
	if it.Provenance["task_id"] != taskID.String() {
		t.Errorf("Provenance[task_id] = %q, want %s", it.Provenance["task_id"], taskID)
	}
}

func TestRetrieveSessionHandoffConverterEndToEnd(t *testing.T) {
	f := newFakes()
	handoffID := uuid.New()
	projectID := uuid.New()
	f.session.latestHandoff = &db.SessionHandoff{
		ID: handoffID, Intent: "ship P1 fixes", ContextSummary: pgtype.Text{String: "review found 9 issues", Valid: true},
		ProjectID: pgtype.UUID{Bytes: projectID, Valid: true},
		RepoName:  pgtype.Text{String: testRepoName, Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}

	items, _ := f.assembler().retrieve(context.Background(), Request{Objective: "test"})

	it := findItem(items, "session", handoffID)
	if it == nil {
		t.Fatalf("expected session-handoff item %s in retrieve() output", handoffID)
	}
	if it.SourceTable != "session_handoffs" {
		t.Errorf("SourceTable = %q, want session_handoffs", it.SourceTable)
	}
	if it.Summary != "ship P1 fixes — review found 9 issues" {
		t.Errorf("Summary = %q, want %q", it.Summary, "ship P1 fixes — review found 9 issues")
	}
	if it.Provenance["project_id"] != projectID.String() {
		t.Errorf("Provenance[project_id] = %q, want %s", it.Provenance["project_id"], projectID)
	}
	if it.Provenance["repo_name"] != testRepoName {
		t.Errorf("Provenance[repo_name] = %q, want wayneblacktea", it.Provenance["repo_name"])
	}
}
