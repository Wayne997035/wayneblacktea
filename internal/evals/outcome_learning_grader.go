package evals

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/outcome"
)

// successThreshold is the minimum count of result=="success" outcomes
// sharing the same EntityType before stubOutcomeLearner proposes a skill
// candidate. This is an invented threshold for this grader's own stub
// model, not the real recall_count > 3 threshold gating
// runKnowledgeToSkillCandidate (internal/scheduler/cognitive_jobs.go:457) —
// that function proposes candidates from knowledge_items.recall_count, an
// entirely different counter this harness never touches; the numeric
// coincidence (3) is not a claim of equivalence. See
// TestOutcomeLearning_SuccessThreshold for the sensitivity check that this
// stub's own value is actually load-bearing on the stub's own behavior.
const successThreshold = 3

// OutcomeLearner decides whether a set of recorded outcomes is strong enough
// evidence to propose promoting a procedure to a skill candidate, using an
// invented deterministic stub built for grading purposes. This interface —
// and stubOutcomeLearner below — is NOT wired to and does NOT validate
// internal/scheduler.runKnowledgeToSkillCandidate, the real job that
// creates pending_proposal rows. The real job selects knowledge_items where
// recall_count > 3 (cognitive_jobs.go:457); it has no notion of
// outcome.Outcome{Result:"success"} counts at all. This interface exists so
// the eval harness can exercise a *decision-logic shape* deterministically
// without pulling in the scheduler, a DB pool, or an LLM provider (package
// doc, evals.go).
type OutcomeLearner interface {
	// ShouldProposeSkill reports whether outcomes contain enough repeated
	// success evidence to warrant proposing a skill candidate.
	ShouldProposeSkill(outcomes []outcome.Outcome) bool
}

// stubOutcomeLearner is an invented deterministic model, built only for
// grading this harness — it is NOT a stand-in for the real scheduler
// decision and does NOT validate runKnowledgeToSkillCandidate (see
// OutcomeLearner and successThreshold doc comments above for the specific
// mismatch: recall_count vs. success-result counts). It counts outcomes
// with Result == "success" grouped by EntityType and proposes a candidate
// once any group reaches successThreshold. Non-success results — including
// "unknown" (the result complete_task auto-seeds via seedDraftOutcome,
// internal/mcp/tools_gtd.go) — are never counted, so auto-seeded rows can
// never push the threshold on their own.
type stubOutcomeLearner struct{}

var _ OutcomeLearner = stubOutcomeLearner{}

func (stubOutcomeLearner) ShouldProposeSkill(outcomes []outcome.Outcome) bool {
	successByEntityType := make(map[string]int, len(outcomes))
	for _, o := range outcomes {
		if o.Result != "success" {
			continue
		}
		successByEntityType[o.EntityType]++
		if successByEntityType[o.EntityType] >= successThreshold {
			return true
		}
	}
	return false
}

// FailedOutcomeSurfaced reports whether a failure recorded in outcomes would
// surface for a caller searching with query — i.e. at least one outcome with
// Result == "failure" whose Notes contains query as a substring. This models
// the retrospection path (find_failed_patterns / ListFailedOutcomes,
// internal/outcome/iface.go) that must never silently swallow a failure: if
// the failure's own notes mention the thing being searched for, the caller
// MUST be able to find it.
func FailedOutcomeSurfaced(outcomes []outcome.Outcome, query string) bool {
	if query == "" {
		return false
	}
	for _, o := range outcomes {
		if o.Result != "failure" {
			continue
		}
		if strings.Contains(o.Notes, query) {
			return true
		}
	}
	return false
}

// OutcomeLearningCase is a single grading scenario for the outcome-learning
// eval category: given outcomes, does the learner propose a skill candidate,
// and does that match ExpectCandidate?
type OutcomeLearningCase struct {
	CaseID          string
	SkillName       string
	Outcomes        []outcome.Outcome
	ExpectCandidate bool
}

// OutcomeLearningGrader adapts an OutcomeLearner plus a set of cases to the
// shared Grader contract (evals.go) so this category runs alongside every
// other one through Run. A nil Learner defaults to stubOutcomeLearner.
type OutcomeLearningGrader struct {
	Learner OutcomeLearner
	Cases   []OutcomeLearningCase
}

var _ Grader = OutcomeLearningGrader{}

// Grade implements the evals.Grader contract (Grade(ctx context.Context)
// EvalResult). It makes no network call, no LLM call, and touches no store —
// it only exercises Learner against in-memory Cases.
func (g OutcomeLearningGrader) Grade(_ context.Context) EvalResult {
	learner := g.Learner
	if learner == nil {
		learner = stubOutcomeLearner{}
	}
	for _, c := range g.Cases {
		got := learner.ShouldProposeSkill(c.Outcomes)
		if got != c.ExpectCandidate {
			return EvalResult{
				CaseID: c.CaseID,
				Passed: false,
				Reason: fmt.Sprintf("skill %q: ShouldProposeSkill returned %v, want %v", c.SkillName, got, c.ExpectCandidate),
			}
		}
	}
	return EvalResult{CaseID: "outcome_learning", Passed: true}
}
