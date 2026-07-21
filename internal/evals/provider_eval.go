// provider_eval.go wires the existing deterministic graders (context
// relevance, conflict placeholder, outcome learning, discipline, budget)
// into a single suite runnable from the standalone eval CLI
// (cmd/wbt-eval, Phase 6.5), independent of `go test` / `task check`.
//
// Fixtures are embedded via go:embed rather than read with LoadFixtures
// (fixture_loader.go): LoadFixtures takes a *testing.T and relies on `go
// test` setting the process CWD to the package directory, neither of which
// holds for a standalone CLI invoked from an arbitrary working directory.
//
// The client parameter threaded through ProviderEvalSuite.Run is accepted
// for future LLM-graded cases — TODO(Phase 6+): LLM-graded cases. Phase 6.5
// wires zero provider-backed cases; every case below is the same
// deterministic logic `go test ./internal/evals/...` already exercises via
// evals_test.go/context_test.go/discipline_test.go/budget_test.go/
// outcome_learning_test.go, never client.CompleteJSON.
package evals

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/llm"
	"github.com/Wayne997035/wayneblacktea/internal/outcome"
)

// Category name constants — the --category flag values cmd/wbt-eval
// accepts, plus the CategoryAll aggregate.
const (
	CategoryContextRelevance = "context_relevance"
	CategoryConflict         = "conflict"
	CategoryOutcomeLearning  = "outcome_learning"
	CategoryDiscipline       = "discipline"
	CategoryBudget           = "budget"
	CategoryAll              = "all"
)

// ProviderEvalCategories lists every individual (non-"all") category
// ProviderEvalSuite recognizes, in the fixed order CategoryAll iterates
// them. Exported so cmd/wbt-eval can validate --category exhaustively
// client-side (backend-security-design.md §5.2) before ever calling Run,
// rather than relying on Run's internal fallback as the validation gate.
var ProviderEvalCategories = []string{
	CategoryContextRelevance,
	CategoryConflict,
	CategoryOutcomeLearning,
	CategoryDiscipline,
	CategoryBudget,
}

// providerEvalBuilders maps each category name to its grader-construction
// function.
var providerEvalBuilders = map[string]func() ([]Grader, error){
	CategoryContextRelevance: contextRelevanceGraders,
	CategoryConflict:         conflictGraders,
	CategoryOutcomeLearning:  outcomeLearningGraders,
	CategoryDiscipline:       disciplineGraders,
	CategoryBudget:           budgetGraders,
}

//go:embed testdata/*.json
var providerEvalFixtures embed.FS

// loadEmbeddedFixture decodes an embedded testdata/*.json fixture into
// []T. name is still passed through validateFixtureName (fixture_loader.go)
// as defence in depth even though every call site below uses a hardcoded
// literal, not caller input (backend-security-design.md §2.2).
func loadEmbeddedFixture[T any](name string) ([]T, error) {
	if err := validateFixtureName(name); err != nil {
		return nil, fmt.Errorf("evals: %w", err)
	}
	raw, err := providerEvalFixtures.ReadFile("testdata/" + name)
	if err != nil {
		return nil, fmt.Errorf("evals: read embedded fixture %s: %w", name, err)
	}
	var out []T
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("evals: unmarshal embedded fixture %s: %w", name, err)
	}
	return out, nil
}

// singleCaseGraders loads fixtureName and converts each decoded case T into
// a Grader via build. It is shared by the two categories
// (context_relevance, conflict) whose fixture case already satisfies
// Grader's data needs 1:1 with no extra wiring; discipline/budget/
// outcome_learning need per-item collaborators (checker/enforcer/type
// conversion) and are built directly below instead.
func singleCaseGraders[T any](fixtureName string, build func(T) Grader) ([]Grader, error) {
	cases, err := loadEmbeddedFixture[T](fixtureName)
	if err != nil {
		return nil, err
	}
	graders := make([]Grader, 0, len(cases))
	for _, c := range cases {
		graders = append(graders, build(c))
	}
	return graders, nil
}

func contextRelevanceGraders() ([]Grader, error) {
	return singleCaseGraders("context_relevance_cases.json", func(c ContextRelevanceCase) Grader {
		return &ContextRelevanceGrader{Case: c}
	})
}

func conflictGraders() ([]Grader, error) {
	return singleCaseGraders("conflict_cases.json", func(c ConflictCase) Grader {
		return &ConflictGrader{Case: c}
	})
}

func disciplineGraders() ([]Grader, error) {
	cases, err := loadEmbeddedFixture[disciplineFixtureCase]("discipline_cases.json")
	if err != nil {
		return nil, err
	}
	checker := stubDisciplineChecker{}
	graders := make([]Grader, 0, len(cases))
	for _, c := range cases {
		graders = append(graders, disciplineDriftGrader{
			caseID:           c.ID,
			toolName:         c.ToolName,
			hasPriorDecision: c.HasPriorDecision,
			expectDrift:      c.ExpectDrift,
			checker:          checker,
		})
	}
	return graders, nil
}

func budgetGraders() ([]Grader, error) {
	cases, err := loadEmbeddedFixture[budgetFixtureCase]("budget_cases.json")
	if err != nil {
		return nil, err
	}
	enforcer := stubBudgetEnforcer{}
	graders := make([]Grader, 0, len(cases))
	for _, c := range cases {
		graders = append(graders, newBudgetGrader(c, enforcer))
	}
	return graders, nil
}

// providerEvalOutcomeEntry/providerEvalOutcomeCase mirror
// testdata/outcome_learning_cases.json's schema. Duplicated here rather than
// reused from outcome_learning_test.go's outcomeLearningFixtureEntry/Case
// because this file is production code compiled into the wbt-eval binary,
// and non-test files cannot reference types declared in _test.go files.
type providerEvalOutcomeEntry struct {
	Status    string   `json:"status"`
	Repo      string   `json:"repo"`
	Files     []string `json:"files"`
	Procedure string   `json:"procedure"`
}

type providerEvalOutcomeCase struct {
	ID              string                     `json:"id"`
	Outcomes        []providerEvalOutcomeEntry `json:"outcomes"`
	SkillName       string                     `json:"skill_name"`
	ExpectCandidate bool                       `json:"expect_candidate"`
}

// providerEvalToDomainOutcomes converts the fixture's JSON vocabulary into
// outcome.Outcome the same way outcome_learning_test.go's toDomainOutcomes
// does: Procedure becomes EntityType (the dimension repeated evidence is
// grouped by), Status becomes Result, Files are joined into Notes for
// traceability only.
func providerEvalToDomainOutcomes(entries []providerEvalOutcomeEntry) []outcome.Outcome {
	out := make([]outcome.Outcome, 0, len(entries))
	for _, e := range entries {
		out = append(out, outcome.Outcome{
			EntityType: e.Procedure,
			Result:     e.Status,
			Notes:      strings.Join(e.Files, ","),
		})
	}
	return out
}

func outcomeLearningGraders() ([]Grader, error) {
	fixtures, err := loadEmbeddedFixture[providerEvalOutcomeCase]("outcome_learning_cases.json")
	if err != nil {
		return nil, err
	}
	graders := make([]Grader, 0, len(fixtures))
	for _, fc := range fixtures {
		graders = append(graders, OutcomeLearningGrader{
			Cases: []OutcomeLearningCase{{
				CaseID:          fc.ID,
				SkillName:       fc.SkillName,
				Outcomes:        providerEvalToDomainOutcomes(fc.Outcomes),
				ExpectCandidate: fc.ExpectCandidate,
			}},
		})
	}
	return graders, nil
}

// ProviderEvalSuite runs the deterministic eval graders through the same
// Grader contract `go test` already exercises (evals.go), from outside a
// `go test` process — see package doc above. Category restricts which
// category's cases run; "" or CategoryAll runs every wired category.
type ProviderEvalSuite struct {
	Category string
}

// Run executes every grader selected by s.Category and returns one
// EvalResult per fixture case. client is unused today (see package doc's
// TODO(Phase 6+)) — it is threaded through now so cmd/wbt-eval's
// --provider/--model flags have a real parameter to eventually pass an
// llm.JSONClient into once LLM-graded cases exist, without a signature
// change at that point.
//
// A fixture-loading error (embed.FS is compiled in, so this should only
// happen if a category's testdata file is malformed) surfaces as a single
// failing EvalResult rather than a panic or silent empty result, so a
// broken fixture is visible in the CLI's JSON output and exit code.
func (s ProviderEvalSuite) Run(ctx context.Context, _ llm.JSONClient) []EvalResult {
	graders, err := s.graders()
	if err != nil {
		return []EvalResult{{
			CaseID: "provider_eval_suite",
			Passed: false,
			Reason: fmt.Sprintf("loading fixtures: %v", err),
		}}
	}

	results := make([]EvalResult, 0, len(graders))
	for _, g := range graders {
		results = append(results, g.Grade(ctx))
	}
	return results
}

// graders resolves s.Category to the concrete Grader set. An unrecognized
// category name fails closed to zero graders rather than silently falling
// back to "all" — cmd/wbt-eval validates --category exhaustively against
// ProviderEvalCategories before it ever reaches here
// (backend-security-design.md §5.2), so this branch is a defensive
// fallback, not the primary validation gate.
func (s ProviderEvalSuite) graders() ([]Grader, error) {
	category := s.Category
	if category == "" {
		category = CategoryAll
	}

	if category == CategoryAll {
		var all []Grader
		for _, name := range ProviderEvalCategories {
			built, err := providerEvalBuilders[name]()
			if err != nil {
				return nil, fmt.Errorf("category %s: %w", name, err)
			}
			all = append(all, built...)
		}
		return all, nil
	}

	build, ok := providerEvalBuilders[category]
	if !ok {
		return nil, nil
	}
	return build()
}
