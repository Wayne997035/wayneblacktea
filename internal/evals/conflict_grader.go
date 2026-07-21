// conflict_grader.go is a minimal type/interface placeholder for the
// conflict-detection eval category (superseded-chain resolution across
// memory items). Real detection logic is explicitly out of scope for
// Phase 6.2 — 2026-07-20 Lead re-scope: "conflict:只做最小 interface stub
// 佔位,真邏輯歸 P3 記憶治理".
//
// TODO(P3): 真實 conflict 偵測邏輯歸 P3 記憶治理 — implement Grade() to walk
// each item's SupersededBy chain and assert it matches ExpectedConflictIDs,
// the same way ContextRelevanceGrader.Grade() (context_relevance_grader.go)
// exercises contextpack's real Assemble() pipeline.
package evals

import "context"

// ConflictItem is one candidate memory item in a conflict fixture case.
type ConflictItem struct {
	ID           string  `json:"id"`
	Content      string  `json:"content"`
	SupersededBy *string `json:"superseded_by"`
}

// ConflictCase mirrors testdata/conflict_cases.json's shape so
// LoadFixtures[ConflictCase] can decode it today, ahead of P3's real
// detector.
type ConflictCase struct {
	ID                  string         `json:"id"`
	Items               []ConflictItem `json:"items"`
	ExpectedConflictIDs []string       `json:"expected_conflict_ids"`
}

// ConflictGrader wraps a ConflictCase so it satisfies Grader for wiring
// purposes only. Grade below intentionally performs no conflict detection.
type ConflictGrader struct {
	Case ConflictCase
}

var _ Grader = (*ConflictGrader)(nil)

// Grade is a placeholder: it does not walk SupersededBy chains or compare
// against ExpectedConflictIDs — that is P3's responsibility (see TODO
// above). It always reports Passed: true so wiring this grader into the
// harness today never fails task check; the Reason makes the placeholder
// nature explicit so a passing result here is never mistaken for a verified
// conflict-detection pass.
func (g *ConflictGrader) Grade(_ context.Context) EvalResult {
	return EvalResult{
		CaseID: g.Case.ID,
		Passed: true,
		Reason: "placeholder: conflict detection not implemented (TODO P3), fixture loaded but not evaluated",
	}
}
