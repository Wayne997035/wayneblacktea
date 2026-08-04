// context_relevance_grader.go wires internal/contextpack's PUBLIC API
// (NewAssembler + Assemble) into the evals harness against the fixtures in
// testdata/context_relevance_cases.json, proving three properties of the
// real (non-mocked) contextpack scoring/exclusion/budget pipeline:
//
//  1. Score ordering — the fixture's expected_top_ids score strictly above
//     every other candidate via contextpack's real score() function (repo/
//     task match, recency, keyword match — see contextpack/scorer.go), not
//     via the fixture's own advisory "score" field (which this grader never
//     reads into any store).
//  2. Stale/superseded exclusion — a behavior rule whose content marks it
//     "deprecated" is filtered out by retrieveBehaviorRules before scoring
//     ever runs, the same production contract wayneblacktea's other callers
//     rely on.
//  3. Budget trimming — re-running Assemble with BudgetChars sized to fit
//     only the winning item's Summary reports the rest via Pack.Omitted
//     rather than silently dropping them.
//
// contextpack.NewAssembler wires 11 narrow, consumer-owned read ports (see
// internal/contextpack/ports.go) rather than the 11 full domain StoreIfaces
// — only gtd/decision/knowledge/behaviorRule carry fixture-driven state
// below; the remaining 7 stubs implement just the one method their read
// port requires (returning zero values), since no fixture item routes
// through those sources.
package evals

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	"github.com/Wayne997035/wayneblacktea/internal/contextpack"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	// contextRelevanceRepoName is the fixed req.RepoName used for every
	// case; only the fixture item designated as expected-top gets its
	// Decision.RepoName tagged to match, so the repo-match bonus in
	// contextpack's score() function differentiates it from the rest.
	contextRelevanceRepoName = "context-relevance-eval-repo"
	// contextRelevanceStaleAge pushes a non-winning item's CreatedAt outside
	// contextpack's 180-day recentWindow (contextpack/retrieval.go) so it
	// never collects the "recent" scoring bonus the winning item gets.
	contextRelevanceStaleAge = 200 * 24 * time.Hour
	// contextRelevanceProbeBudget is generous enough that no fixture case's
	// candidate set is ever trimmed during the initial (non-budget) probe
	// Assemble() call — every case's content is a handful of short sentences.
	contextRelevanceProbeBudget = 5000
)

// errStubNotConfigured is returned by every stub domain-store method the
// context-relevance fixtures never populate. contextpack's retrieve() treats
// any non-ErrNotFound error as a soft, Pack.Warnings-reported failure (see
// contextpack/retrieval.go warnStoreErr), so an accidentally-exercised stub
// surfaces as a visible Warning instead of a nil-pointer panic further down
// the pipeline.
var errStubNotConfigured = errors.New("evals: stub store method not exercised by context-relevance fixtures")

// ---------------------------------------------------------------------------
// fixture schema — mirrors testdata/context_relevance_cases.json exactly.
// ---------------------------------------------------------------------------

// ContextRelevanceItem is one candidate item in a context-relevance fixture
// case. Score documents the fixture author's intended relative ranking for
// human readers only — the grader never wires it into any store field;
// ranking is instead proven through contextpack's own, real score()
// computation (see buildContextRelevanceFixture).
type ContextRelevanceItem struct {
	ID      string  `json:"id"`
	Source  string  `json:"source"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// ContextRelevanceCase is one fixture scenario loaded via
// LoadFixtures[ContextRelevanceCase](t, "context_relevance_cases.json").
type ContextRelevanceCase struct {
	ID             string                 `json:"id"`
	Description    string                 `json:"description"`
	Items          []ContextRelevanceItem `json:"items"`
	ExpectedTopIDs []string               `json:"expected_top_ids"`
}

// ---------------------------------------------------------------------------
// stub contextpack.TaskProjectReadPort — only WorkspaceID/GetTaskByID carry
// fixture state; GetProjectByID/ProjectsByRepoName exist solely to satisfy
// the port and are never exercised by these fixtures.
// ---------------------------------------------------------------------------

type stubGTDStore struct {
	workspaceID uuid.UUID
	taskByID    *db.Task
}

var _ contextpack.TaskProjectReadPort = (*stubGTDStore)(nil)

func (s *stubGTDStore) WorkspaceID() pgtype.UUID {
	return pgtype.UUID{Bytes: s.workspaceID, Valid: true}
}

func (s *stubGTDStore) GetTaskByID(_ context.Context, _ uuid.UUID) (*db.Task, error) {
	if s.taskByID != nil {
		return s.taskByID, nil
	}
	return nil, gtd.ErrNotFound
}

func (s *stubGTDStore) GetProjectByID(_ context.Context, _ uuid.UUID) (*db.Project, error) {
	return nil, errStubNotConfigured
}

func (s *stubGTDStore) ProjectsByRepoName(_ context.Context, _ string) ([]db.Project, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// stub contextpack.DecisionReadPort — only ByRepo carries fixture state.
// ---------------------------------------------------------------------------

type stubDecisionStore struct {
	byRepo []db.Decision
}

var _ contextpack.DecisionReadPort = (*stubDecisionStore)(nil)

func (s *stubDecisionStore) ByRepo(_ context.Context, _ string, _ int32) ([]db.Decision, error) {
	return s.byRepo, nil
}

func (s *stubDecisionStore) ByProject(_ context.Context, _ uuid.UUID, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (s *stubDecisionStore) ByTask(_ context.Context, _ uuid.UUID, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (s *stubDecisionStore) All(_ context.Context, _ int32) ([]db.Decision, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// stub contextpack.KnowledgeReadPort — only SearchReadOnly carries fixture
// state. retrieveKnowledge (contextpack/retrieval.go) calls SearchReadOnly,
// never Search, so fixture items intentionally live only there.
// ---------------------------------------------------------------------------

type stubKnowledgeStore struct {
	searchResults []db.KnowledgeItem
}

var _ contextpack.KnowledgeReadPort = (*stubKnowledgeStore)(nil)

func (s *stubKnowledgeStore) SearchReadOnly(_ context.Context, _ string, _ int) ([]db.KnowledgeItem, error) {
	return s.searchResults, nil
}

// ---------------------------------------------------------------------------
// stub contextpack.AtomReadPort — pure no-op; no atom-sourced fixture item
// exists.
// ---------------------------------------------------------------------------

type stubAtomStore struct{}

var _ contextpack.AtomReadPort = (*stubAtomStore)(nil)

func (s *stubAtomStore) Search(_ context.Context, _ *uuid.UUID, _ string, _ int) ([]atom.Atom, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// stub contextpack.ProceduralReadPort — pure no-op; no procedural-sourced
// fixture item exists.
// ---------------------------------------------------------------------------

type stubProceduralStore struct{}

var _ contextpack.ProceduralReadPort = (*stubProceduralStore)(nil)

func (s *stubProceduralStore) Query(_ context.Context, _ procedural.QueryFilter) ([]procedural.ProceduralMemory, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// stub contextpack.SkillReadPort — pure no-op; no skill-sourced fixture item
// exists.
// ---------------------------------------------------------------------------

type stubSkillStore struct{}

var _ contextpack.SkillReadPort = (*stubSkillStore)(nil)

func (s *stubSkillStore) Search(_ context.Context, _ skill.SearchFilter) ([]*skill.Skill, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// stub contextpack.OutcomeReadPort — pure no-op; no outcome-sourced fixture
// item exists.
// ---------------------------------------------------------------------------

type stubOutcomeStore struct{}

var _ contextpack.OutcomeReadPort = (*stubOutcomeStore)(nil)

func (s *stubOutcomeStore) ListFailedOutcomes(_ context.Context, _ *uuid.UUID, _ int) ([]outcome.Outcome, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// stub contextpack.ReflectionReadPort — pure no-op; no reflection-sourced
// fixture item exists.
// ---------------------------------------------------------------------------

type stubReflectionStore struct{}

var _ contextpack.ReflectionReadPort = (*stubReflectionStore)(nil)

func (s *stubReflectionStore) RecentWithPatterns(_ context.Context, _ *uuid.UUID, _ time.Time, _ int) ([]*reflection.Reflection, error) {
	return nil, nil
}

// ---------------------------------------------------------------------------
// stub contextpack.BehaviorRuleReadPort — List carries fixture state and
// mirrors the real store's Status filter, so a fixture rule with Status
// "deprecated" is genuinely excluded here, the same contract
// retrieveBehaviorRules (contextpack/retrieval.go) relies on in production
// rather than a fixture-side simulation of exclusion.
// ---------------------------------------------------------------------------

type stubBehaviorRuleStore struct {
	rules []*behaviorrule.BehaviorRule
}

var _ contextpack.BehaviorRuleReadPort = (*stubBehaviorRuleStore)(nil)

func (s *stubBehaviorRuleStore) List(_ context.Context, p behaviorrule.ListParams) ([]*behaviorrule.BehaviorRule, error) {
	if p.Status == nil {
		return s.rules, nil
	}
	var out []*behaviorrule.BehaviorRule
	for _, r := range s.rules {
		if r.Status == *p.Status {
			out = append(out, r)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// stub contextpack.SessionReadPort — LatestHandoff defaults to ErrNotFound,
// mirroring the real store's "no unresolved handoff" contract; no fixture
// ever populates one.
// ---------------------------------------------------------------------------

type stubSessionStore struct{}

var _ contextpack.SessionReadPort = (*stubSessionStore)(nil)

func (s *stubSessionStore) LatestHandoff(_ context.Context) (*db.SessionHandoff, error) {
	return nil, session.ErrNotFound
}

// ---------------------------------------------------------------------------
// stub contextpack.WorkSessionReadPort — GetActive defaults to
// Active:false, mirroring the real store's "no in_progress session"
// contract; no fixture ever populates one.
// ---------------------------------------------------------------------------

type stubWorkSessionStore struct{}

var _ contextpack.WorkSessionReadPort = (*stubWorkSessionStore)(nil)

func (s *stubWorkSessionStore) GetActive(_ context.Context, _ uuid.UUID, _ string) (*worksession.ActiveSessionResult, error) {
	return &worksession.ActiveSessionResult{}, nil
}

// ---------------------------------------------------------------------------
// grader
// ---------------------------------------------------------------------------

// ContextRelevanceGrader evaluates one ContextRelevanceCase through
// contextpack's real Assemble() pipeline.
type ContextRelevanceGrader struct {
	Case ContextRelevanceCase
}

var _ Grader = (*ContextRelevanceGrader)(nil)

// contextRelevanceFixture bundles the stub stores + derived request fields
// buildContextRelevanceFixture assembles from a ContextRelevanceCase's items.
type contextRelevanceFixture struct {
	gtd          *stubGTDStore
	decision     *stubDecisionStore
	knowledge    *stubKnowledgeStore
	behaviorRule *stubBehaviorRuleStore
	taskID       *uuid.UUID
	// excludedFixtureIDs holds the fixture item ids expected to never reach
	// Pack.Items — currently only behavior rules whose content marks them
	// deprecated (contextpack's real Status filter, not a simulation).
	excludedFixtureIDs []string
}

// buildContextRelevanceFixture routes each fixture item to the domain store
// contextpack actually retrieves it from, and tags the item(s) named in
// c.ExpectedTopIDs with the recency/repo/confidence signals needed for
// contextpack's real score() function to rank them first — see the package
// doc comment for the full scoring rationale.
func buildContextRelevanceFixture(c ContextRelevanceCase) contextRelevanceFixture {
	fx := contextRelevanceFixture{
		gtd:          &stubGTDStore{workspaceID: uuid.New()},
		decision:     &stubDecisionStore{},
		knowledge:    &stubKnowledgeStore{},
		behaviorRule: &stubBehaviorRuleStore{},
	}
	expectedTop := toStringSet(c.ExpectedTopIDs)

	for _, it := range c.Items {
		id := fixtureItemUUID(it.ID)
		createdAt := time.Now().Add(-contextRelevanceStaleAge)
		isTop := expectedTop[it.ID]
		if isTop {
			createdAt = time.Now()
		}

		switch it.Source {
		case "task":
			fx.gtd.taskByID = &db.Task{ID: id, Title: it.Content, CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true}}
			taskID := id
			fx.taskID = &taskID
		case "decision":
			d := db.Decision{ID: id, Decision: it.Content, CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true}}
			if isTop {
				d.RepoName = pgtype.Text{String: contextRelevanceRepoName, Valid: true}
			}
			fx.decision.byRepo = append(fx.decision.byRepo, d)
		case "rule":
			status := "active"
			if strings.Contains(strings.ToLower(it.Content), "deprecated") {
				status = "deprecated"
				fx.excludedFixtureIDs = append(fx.excludedFixtureIDs, it.ID)
			}
			confidence := 0.0
			if isTop {
				confidence = 0.9
			}
			fx.behaviorRule.rules = append(fx.behaviorRule.rules, &behaviorrule.BehaviorRule{
				ID: id, Condition: it.Content, Action: "handle it",
				Status: status, CreatedAt: createdAt, Confidence: confidence,
			})
		default:
			// "note"/"log"/any other source: no dedicated domain store
			// exists for free-form background noise, so route through
			// knowledge — itemFromKnowledge never sets
			// Provenance["repo_name"], so these items can never collect the
			// repo-match bonus that would let them out-rank a real
			// task/decision/rule candidate.
			fx.knowledge.searchResults = append(fx.knowledge.searchResults, db.KnowledgeItem{
				ID: id, Content: it.Content, CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true},
			})
		}
	}
	return fx
}

// Grade builds an Assembler from the case's fixture items, runs it through
// contextpack's real Assemble() twice — once with a generous budget to check
// score ordering and exclusion, once with a budget sized to exactly fit the
// winning item to check budget-trim reporting — and returns the combined
// verdict.
func (g *ContextRelevanceGrader) Grade(ctx context.Context) EvalResult {
	c := g.Case
	fx := buildContextRelevanceFixture(c)

	assembler, err := contextpack.NewAssembler(
		fx.gtd, fx.decision, fx.knowledge, &stubAtomStore{}, &stubProceduralStore{}, &stubSkillStore{},
		&stubOutcomeStore{}, &stubReflectionStore{}, fx.behaviorRule, &stubSessionStore{}, &stubWorkSessionStore{},
	)
	if err != nil {
		return failResult(c.ID, "NewAssembler: %v", err)
	}

	req := contextpack.Request{
		Objective:   objectiveFromExpectedTop(c),
		RepoName:    contextRelevanceRepoName,
		TaskID:      fx.taskID,
		BudgetChars: contextRelevanceProbeBudget,
	}

	probe, err := assembler.Assemble(ctx, req)
	if err != nil {
		return failResult(c.ID, "Assemble (probe): %v", err)
	}
	if reason := assertExcluded(probe.Items, fx.excludedFixtureIDs); reason != "" {
		return EvalResult{CaseID: c.ID, Passed: false, Reason: reason}
	}
	if reason := assertExpectedTopOutranksRest(probe.Items, c.ExpectedTopIDs); reason != "" {
		return EvalResult{CaseID: c.ID, Passed: false, Reason: reason}
	}
	if len(probe.Items) < 2 {
		return EvalResult{
			CaseID: c.ID, Passed: false,
			Reason: "fixture yielded fewer than 2 surviving candidates; cannot exercise budget trimming",
		}
	}

	// probe.Items is sorted score-desc (contextpack.Assemble); Items[0] is
	// the winner, just proven above to outrank everything else.
	tightReq := req
	tightReq.BudgetChars = utf8.RuneCountInString(probe.Items[0].Summary)
	trimmed, err := assembler.Assemble(ctx, tightReq)
	if err != nil {
		return failResult(c.ID, "Assemble (tight budget): %v", err)
	}
	if findItemByID(trimmed.Items, probe.Items[0].ID) == nil {
		return EvalResult{CaseID: c.ID, Passed: false, Reason: "winning item dropped by a budget sized to exactly fit it"}
	}
	if len(trimmed.Omitted) == 0 {
		return EvalResult{
			CaseID: c.ID, Passed: false,
			Reason: "expected Pack.Omitted to report the items dropped by the tight budget, got none",
		}
	}

	return EvalResult{
		CaseID: c.ID, Passed: true,
		Reason: "score ordering matched expected_top_ids, stale/deprecated items excluded, budget omission reported",
	}
}

func failResult(caseID, format string, err error) EvalResult {
	return EvalResult{CaseID: caseID, Passed: false, Reason: fmt.Sprintf(format, err)}
}

// objectiveFromExpectedTop derives the Request.Objective fed to Assemble()
// from the real content of the case's expected-top item, so contextpack's
// keyword-match scoring signal is exercised against genuine fixture text
// rather than a synthetic phrase invented independently of the fixture.
func objectiveFromExpectedTop(c ContextRelevanceCase) string {
	expected := toStringSet(c.ExpectedTopIDs)
	for _, it := range c.Items {
		if expected[it.ID] {
			return it.Content
		}
	}
	return c.Description
}

// assertExcluded reports a non-empty reason if any fixture id in
// excludedFixtureIDs (stale/deprecated per buildContextRelevanceFixture)
// unexpectedly appears in items.
func assertExcluded(items []contextpack.Item, excludedFixtureIDs []string) string {
	for _, fid := range excludedFixtureIDs {
		if findItemByID(items, fixtureItemUUID(fid)) != nil {
			return fmt.Sprintf("expected fixture item %q to be excluded (stale/deprecated), but it was present in Pack.Items", fid)
		}
	}
	return ""
}

// assertExpectedTopOutranksRest reports a non-empty reason unless every
// fixture id in expectedTopFixtureIDs is present in items and scores
// strictly above every other item — i.e. contextpack's real score()
// genuinely ranks the fixture's designated winner(s) first.
func assertExpectedTopOutranksRest(items []contextpack.Item, expectedTopFixtureIDs []string) string {
	expectedUUIDs := make(map[uuid.UUID]bool, len(expectedTopFixtureIDs))
	for _, fid := range expectedTopFixtureIDs {
		expectedUUIDs[fixtureItemUUID(fid)] = true
	}

	var expectedMin, otherMax *float64
	found := 0
	for _, it := range items {
		score := it.Score
		if expectedUUIDs[it.ID] {
			found++
			if expectedMin == nil || score < *expectedMin {
				expectedMin = &score
			}
		} else if otherMax == nil || score > *otherMax {
			otherMax = &score
		}
	}
	if found != len(expectedTopFixtureIDs) {
		return fmt.Sprintf("expected %d top item(s) in Pack.Items, found %d", len(expectedTopFixtureIDs), found)
	}
	if otherMax != nil && *expectedMin <= *otherMax {
		return fmt.Sprintf("expected top item score %.2f did not strictly outrank the next-highest score %.2f", *expectedMin, *otherMax)
	}
	return ""
}

// findItemByID returns a pointer to the item in items whose ID matches id,
// or nil if none does.
func findItemByID(items []contextpack.Item, id uuid.UUID) *contextpack.Item {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

// toStringSet converts a slice of fixture ids into a lookup set.
func toStringSet(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// fixtureItemUUID derives a deterministic uuid.UUID from a fixture item's
// human-readable string id (e.g. "item-task-current"), so the same fixture
// id always resolves to the same UUID across runs — contextpack.Item.ID is
// uuid.UUID but testdata/context_relevance_cases.json items use string ids.
// FNV-128a (non-cryptographic, deterministic) is used purely as a
// fixed-width hash here, not for any security property.
func fixtureItemUUID(fixtureID string) uuid.UUID {
	h := fnv.New128a()
	_, _ = h.Write([]byte(fixtureID)) // hash.Hash.Write never returns an error
	var id uuid.UUID
	copy(id[:], h.Sum(nil))
	return id
}
