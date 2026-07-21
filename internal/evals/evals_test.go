package evals

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// validateFixtureName: pure guard, unit-tested directly (normal + error cases)
// ---------------------------------------------------------------------------

func TestValidateFixtureName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "valid bare filename", input: "context_relevance_cases.json", wantErr: false},
		{name: "empty name rejected", input: "", wantErr: true},
		{name: "parent directory traversal rejected", input: "../evals.go", wantErr: true},
		{name: "bare dotdot rejected", input: "..", wantErr: true},
		{name: "forward slash subpath rejected", input: "sub/dir.json", wantErr: true},
		{name: "backslash subpath rejected", input: "a\\b.json", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFixtureName(tc.input)
			if tc.wantErr && err == nil {
				t.Fatalf("validateFixtureName(%q): expected error, got nil", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateFixtureName(%q): unexpected error: %v", tc.input, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Run / Grader scaffold self-test (happy path, safe to run in-process)
// ---------------------------------------------------------------------------

type stubGrader struct {
	result EvalResult
}

func (s stubGrader) Grade(_ context.Context) EvalResult {
	return s.result
}

func TestRun_PassingGraderDoesNotFail(t *testing.T) {
	Run(t, []Grader{stubGrader{result: EvalResult{CaseID: "ok-1", Passed: true}}})
}

// ---------------------------------------------------------------------------
// Subprocess-based tests for code paths that call t.Fatalf/t.Errorf.
//
// A failing t.Run subtest always marks its parent (and the whole package
// run) as failed, regardless of what assertions run after it — there is no
// in-process way to "expect a subtest to fail". So the two scenarios that
// must exercise the real t.Fatalf/t.Errorf path (a failing grader through
// Run, and LoadFixtures hitting a missing file) are executed in a
// re-invoked test binary and asserted on exit status + output instead.
// ---------------------------------------------------------------------------

const helperProcessEnvKey = "EVALS_TEST_HELPER_CASE"

// TestHelperProcess is not a real test on its own; it is invoked via
// exec.Command by the tests below to exercise t.Fatalf/t.Errorf paths in
// isolation. It no-ops (skips) when run as part of the normal test suite.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv(helperProcessEnvKey) {
	case "":
		t.Skip("TestHelperProcess is only meant to be invoked as a subprocess helper")
	case "run_failing_grader":
		Run(t, []Grader{stubGrader{result: EvalResult{CaseID: "bad-1", Passed: false, Reason: "expected failure for scaffold self-test"}}})
	case "load_missing_fixture":
		LoadFixtures[map[string]any](t, "does-not-exist.json")
	}
}

// runHelperProcess re-invokes the current test binary with only
// TestHelperProcess selected, so t.Fatalf/t.Errorf inside it cannot fail this
// process's own test run.
func runHelperProcess(t *testing.T, helperCase string) (output string, failed bool) {
	t.Helper()

	//nolint:gosec // G204: os.Args[0] is this test binary's own path; arg is a hardcoded literal
	cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), helperProcessEnvKey+"="+helperCase)
	raw, err := cmd.CombinedOutput()
	return string(raw), err != nil
}

func TestRun_FailingGraderReportsFailure(t *testing.T) {
	out, failed := runHelperProcess(t, "run_failing_grader")
	if !failed {
		t.Fatalf("expected Run to fail the test for a failing grader, but the helper process succeeded. output:\n%s", out)
	}
	if !strings.Contains(out, "bad-1") {
		t.Errorf("expected failure output to mention case ID bad-1, got:\n%s", out)
	}
}

func TestLoadFixtures_MissingFile(t *testing.T) {
	out, failed := runHelperProcess(t, "load_missing_fixture")
	if !failed {
		t.Fatalf("expected LoadFixtures to fail for a missing fixture file, but the helper process succeeded. output:\n%s", out)
	}
	if !strings.Contains(out, "does-not-exist.json") {
		t.Errorf("expected failure output to mention the missing fixture name, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Fixture-loading self-test: every testdata/*.json fixture must load and be
// non-empty. Field shapes here are scaffold-local sanity checks only — the
// authoritative typed fixtures belong to each grader's own implementation.
// ---------------------------------------------------------------------------

type contextRelevanceItemFixture struct {
	ID      string  `json:"id"`
	Source  string  `json:"source"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

type contextRelevanceCaseFixture struct {
	ID             string                        `json:"id"`
	Description    string                        `json:"description"`
	Items          []contextRelevanceItemFixture `json:"items"`
	ExpectedTopIDs []string                      `json:"expected_top_ids"`
}

func TestLoadFixtures_ContextRelevance(t *testing.T) {
	cases := LoadFixtures[contextRelevanceCaseFixture](t, "context_relevance_cases.json")
	if len(cases) == 0 {
		t.Fatal("expected at least one context relevance case, got none")
	}
	for _, c := range cases {
		if c.ID == "" {
			t.Errorf("case has empty ID")
		}
		if len(c.Items) == 0 {
			t.Errorf("case %s has no items", c.ID)
		}
		if len(c.ExpectedTopIDs) == 0 {
			t.Errorf("case %s has no expected_top_ids", c.ID)
		}
	}
}

type conflictItemFixture struct {
	ID           string  `json:"id"`
	Content      string  `json:"content"`
	SupersededBy *string `json:"superseded_by"`
}

type conflictCaseFixture struct {
	ID                  string                `json:"id"`
	Items               []conflictItemFixture `json:"items"`
	ExpectedConflictIDs []string              `json:"expected_conflict_ids"`
}

func TestLoadFixtures_Conflict(t *testing.T) {
	cases := LoadFixtures[conflictCaseFixture](t, "conflict_cases.json")
	if len(cases) == 0 {
		t.Fatal("expected at least one conflict case, got none")
	}
	for _, c := range cases {
		if c.ID == "" {
			t.Errorf("case has empty ID")
		}
		if len(c.Items) == 0 {
			t.Errorf("case %s has no items", c.ID)
		}
		// expected_conflict_ids may legitimately be empty (the "no false
		// positive" case), so it is not asserted non-empty here.
	}
}

type outcomeFixtureEntry struct {
	Status    string   `json:"status"`
	Repo      string   `json:"repo"`
	Files     []string `json:"files"`
	Procedure string   `json:"procedure"`
}

type outcomeLearningCaseFixture struct {
	ID              string                `json:"id"`
	Outcomes        []outcomeFixtureEntry `json:"outcomes"`
	SkillName       string                `json:"skill_name"`
	ExpectCandidate bool                  `json:"expect_candidate"`
}

func TestLoadFixtures_OutcomeLearning(t *testing.T) {
	cases := LoadFixtures[outcomeLearningCaseFixture](t, "outcome_learning_cases.json")
	if len(cases) == 0 {
		t.Fatal("expected at least one outcome learning case, got none")
	}
	for _, c := range cases {
		if c.ID == "" {
			t.Errorf("case has empty ID")
		}
		if len(c.Outcomes) == 0 {
			t.Errorf("case %s has no outcomes", c.ID)
		}
		if c.SkillName == "" {
			t.Errorf("case %s has no skill_name", c.ID)
		}
	}
}

type disciplineCaseFixture struct {
	ID               string `json:"id"`
	ToolName         string `json:"tool_name"`
	HasPriorDecision bool   `json:"has_prior_decision"`
	ExpectDrift      bool   `json:"expect_drift"`
}

func TestLoadFixtures_Discipline(t *testing.T) {
	cases := LoadFixtures[disciplineCaseFixture](t, "discipline_cases.json")
	if len(cases) == 0 {
		t.Fatal("expected at least one discipline case, got none")
	}
	for _, c := range cases {
		if c.ID == "" {
			t.Errorf("case has empty ID")
		}
		if c.ToolName == "" {
			t.Errorf("case %s has no tool_name", c.ID)
		}
	}
}

type budgetItemFixture struct {
	ID        string `json:"id"`
	SizeBytes int    `json:"size_bytes"`
	Priority  int    `json:"priority"`
}

type budgetCaseFixture struct {
	ID                 string              `json:"id"`
	Items              []budgetItemFixture `json:"items"`
	BudgetBytes        int                 `json:"budget_bytes"`
	ExpectIncluded     []string            `json:"expect_included"`
	ExpectOmittedCount int                 `json:"expect_omitted_count"`
}

func TestLoadFixtures_Budget(t *testing.T) {
	cases := LoadFixtures[budgetCaseFixture](t, "budget_cases.json")
	if len(cases) == 0 {
		t.Fatal("expected at least one budget case, got none")
	}
	for _, c := range cases {
		if c.ID == "" {
			t.Errorf("case has empty ID")
		}
		if len(c.Items) == 0 {
			t.Errorf("case %s has no items", c.ID)
		}
		if c.BudgetBytes <= 0 {
			t.Errorf("case %s has non-positive budget_bytes", c.ID)
		}
	}
}
