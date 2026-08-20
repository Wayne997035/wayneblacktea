package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// callSubmitReview invokes handleSubmitReview directly, mirroring
// callGetDueReviews' pattern in empty_list_contract_test.go.
func callSubmitReview(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleSubmitReview(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSubmitReview error: %v", err)
	}
	return result
}

// TestHandleGetDueReviews_Postgres_EmptyReturnsEmptyArrayNotNull is the
// Postgres half of the get_due_reviews dual-backend contract (SQLite half:
// TestEmptyListContract_MCP_SQLite's "get_due_reviews" row in
// empty_list_contract_test.go). internal/learning/store.go's DueReviews
// already builds reviews := make([]DueReview, 0, len(rows)), so this is a
// regression lock, not a bug-reproduction test — it protects the MCP entry
// point (handleGetDueReviews has no nil-guard of its own; it serializes
// whatever the store returns) against a future edit to the Postgres store
// that swaps make() for a bare `var reviews []DueReview` declaration, which
// is exactly the bug this PR fixes on the SQLite side.
//
// Reuses mcpPlanTestPgPool (tools_plan_pg_test.go's TestMain) rather than
// starting a second Postgres container — internal/mcp already has exactly one
// TestMain per package, and Go permits at most one per package.
func TestHandleGetDueReviews_Postgres_EmptyReturnsEmptyArrayNotNull(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	wsID := uuid.New() // fresh, isolated workspace — guaranteed zero rows
	s := &Server{learning: learning.NewStore(mcpPlanTestPgPool, &wsID)}

	r := callGetDueReviews(t, s)
	if r.IsError {
		t.Fatalf("unexpected tool error: %s", resultText(r))
	}
	if got := strings.TrimSpace(resultText(r)); got != "[]" {
		t.Errorf("raw body = %q, want exactly %q (nil slice must not serialize to JSON null)", got, "[]")
	}
}

// TestHandleSubmitReview_MatureScheduleNotResetByOmittedState is Ω7's
// bad-case red test: submit_review's stability/difficulty/review_count
// params were removed (the MCP handler now reads current state itself via
// learning.StoreIface.GetScheduleState), so there is no longer any way for
// a caller to supply them at all — this proves the fix by exercising the
// only call shape now possible (schedule_id + rating) against a schedule
// that has already been reviewed several times, and asserting the mature
// state continues advancing instead of being reset to the initial-review
// FSRS path (which would show up as review_count resetting or stability
// collapsing back toward its tiny w[0..3] initial value).
func TestHandleSubmitReview_MatureScheduleNotResetByOmittedState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	wsID := uuid.New()
	store := learning.NewStore(mcpPlanTestPgPool, &wsID)
	s := &Server{learning: store}

	concept, err := store.CreateConcept(context.Background(), "mature schedule", "Ω7 regression", []string{"srs"})
	if err != nil {
		t.Fatalf("CreateConcept: %v", err)
	}
	due, err := store.DueReviews(context.Background(), 10)
	if err != nil {
		t.Fatalf("DueReviews: %v", err)
	}
	var scheduleID uuid.UUID
	for _, d := range due {
		if d.ConceptID == concept.ID {
			scheduleID = d.ScheduleID
		}
	}
	if scheduleID == uuid.Nil {
		t.Fatalf("expected a due review schedule for concept %s", concept.ID)
	}

	// Build up a mature schedule via the MCP handler itself (3 reviews).
	for range 3 {
		r := callSubmitReview(t, s, map[string]any{
			"schedule_id": scheduleID.String(),
			"rating":      float64(3), // Good
		})
		if r.IsError {
			t.Fatalf("submit_review failed: %s", resultText(r))
		}
	}

	before, err := store.GetScheduleState(context.Background(), scheduleID)
	if err != nil {
		t.Fatalf("GetScheduleState before final review: %v", err)
	}
	if before.ReviewCount != 3 {
		t.Fatalf("precondition failed: ReviewCount = %d, want 3", before.ReviewCount)
	}

	// One more review — the bad case would reset ReviewCount to 1 and
	// collapse Stability back to a small initial-review value.
	r := callSubmitReview(t, s, map[string]any{
		"schedule_id": scheduleID.String(),
		"rating":      float64(3),
	})
	if r.IsError {
		t.Fatalf("submit_review (4th) failed: %s", resultText(r))
	}

	after, err := store.GetScheduleState(context.Background(), scheduleID)
	if err != nil {
		t.Fatalf("GetScheduleState after final review: %v", err)
	}
	if after.ReviewCount != 4 {
		t.Errorf("ReviewCount: got %d, want 4 (bad case: mature schedule was reset)", after.ReviewCount)
	}
	if after.Stability < before.Stability {
		t.Errorf("Stability regressed from %v to %v (bad case: mature schedule was reset to initial-review path)",
			before.Stability, after.Stability)
	}
}
