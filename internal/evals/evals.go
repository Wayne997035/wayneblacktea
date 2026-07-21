// Package evals is a deterministic, network-free evaluation harness for
// wayneblacktea's memory/learning behavior. Graders assert behavior against
// fixed fixtures under testdata/ and MUST NOT call an LLM provider or make
// any network request — see Phase 6 of docs/internal/wayneblacktea-2.0-development-prompt.md.
package evals

import (
	"context"
	"testing"
)

// EvalCase identifies a single evaluation scenario. Category groups cases for
// reporting (e.g. "context_relevance", "conflict", "outcome_learning").
type EvalCase struct {
	ID          string
	Description string
	Category    string
}

// EvalResult is the outcome of grading one EvalCase.
type EvalResult struct {
	CaseID string
	Passed bool
	Reason string
}

// Grader evaluates a fixture-backed scenario deterministically. Grade takes a
// context.Context — not *testing.T — so the same grader can run both inside
// `go test` and from the standalone eval CLI (cmd/wbt-eval, Phase 6.5) where
// no testing.T is available. Grade MUST NOT make network calls or invoke an
// LLM provider.
type Grader interface {
	Grade(ctx context.Context) EvalResult
}

// Run executes every grader and reports failures through t. It is the
// standard entrypoint for wiring graders into `go test` / `task check`.
func Run(t *testing.T, graders []Grader) {
	t.Helper()

	ctx := context.Background()
	for _, g := range graders {
		result := g.Grade(ctx)
		if !result.Passed {
			t.Errorf("eval case %s failed: %s", result.CaseID, result.Reason)
		}
	}
}
