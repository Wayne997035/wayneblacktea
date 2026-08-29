package mcp

import (
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// TestHandleListPendingProposals_Postgres_EmptyReturnsEmptyArrayNotNull is the
// Postgres half of the list_pending_proposals dual-backend contract (SQLite
// half: TestEmptyListContract_MCP_SQLite's "list_pending_proposals" row in
// empty_list_contract_test.go). proposal.Store.ListPending previously
// returned the sqlc-generated Queries.ListPendingProposals result (a bare
// `var items []PendingProposal`, never re-initialised when empty) straight
// through with no guard — the same nil-slice-to-JSON-null bug as DueReviews,
// discovered while building the SQLite contract table and fixed in the same
// PR (internal/proposal/store.go, internal/storage/sqlite/proposal.go).
//
// Reuses mcpPlanTestPgPool (tools_plan_pg_test.go's TestMain) rather than
// starting a second Postgres container.
func TestHandleListPendingProposals_Postgres_EmptyReturnsEmptyArrayNotNull(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	wsID := uuid.New() // fresh, isolated workspace — guaranteed zero rows
	s := &Server{proposal: proposal.NewStore(mcpPlanTestPgPool, &wsID)}

	r := callListPendingProposalsContract(t, s)
	if r.IsError {
		t.Fatalf("unexpected tool error: %s", resultText(r))
	}
	// [F170-06] The response is a page object now; the nil-vs-[] property
	// being pinned is unchanged, only its position in the body.
	got := strings.TrimSpace(resultText(r))
	if !strings.Contains(got, `"proposals":[]`) {
		t.Errorf("raw body = %q, want it to contain %q (nil slice must not serialize to JSON null)",
			got, `"proposals":[]`)
	}
	if strings.Contains(got, `"proposals":null`) {
		t.Errorf("raw body = %q — the PG store's nil slice reached the wire as JSON null", got)
	}
}
