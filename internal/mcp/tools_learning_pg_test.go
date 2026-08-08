package mcp

import (
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/google/uuid"
)

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
