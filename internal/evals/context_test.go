package evals

import "testing"

// TestContextRelevance runs ContextRelevanceGrader against every case in
// testdata/context_relevance_cases.json, proving contextpack's real
// Assemble() pipeline (score ordering, stale/deprecated exclusion, budget
// trimming) end-to-end through its public API — see
// context_relevance_grader.go's package doc for the full rationale.
func TestContextRelevance(t *testing.T) {
	cases := LoadFixtures[ContextRelevanceCase](t, "context_relevance_cases.json")
	if len(cases) == 0 {
		t.Fatal("expected at least one context_relevance case, got 0")
	}

	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			Run(t, []Grader{&ContextRelevanceGrader{Case: c}})
		})
	}
}

// TestConflict runs ConflictGrader against every case in
// testdata/conflict_cases.json. The grader is a placeholder (see
// conflict_grader.go) — this test proves the fixture loads and wires into
// the harness, not that conflicts are actually detected (TODO P3).
func TestConflict(t *testing.T) {
	cases := LoadFixtures[ConflictCase](t, "conflict_cases.json")
	if len(cases) == 0 {
		t.Fatal("expected at least one conflict case, got 0")
	}

	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			Run(t, []Grader{&ConflictGrader{Case: c}})
		})
	}
}
