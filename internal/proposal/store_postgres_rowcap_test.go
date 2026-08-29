package proposal_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// [F170-06] — Postgres half of list_pending_proposals' row cap, on
// testcontainers (backend-security-design.md §6.5).
//
// Beyond the LIMIT itself, this pins a real pre-existing divergence the change
// closed: the SQLite store already ordered by `created_at DESC, id DESC` while
// the sqlc query ordered by `created_at DESC` alone, so the two backends
// disagreed on tie order. Postgres timestamps in a tight insert loop tie
// readily, which is exactly the condition under which unstable OFFSET paging
// drops rows.

const pgProposalRowCapSeed = 60

func TestPGProposalStore_ListPendingPage_CapsAndPages(t *testing.T) {
	pool := openBatchTestPgPool(t)
	wsID := uuid.New() // fresh workspace: isolated from other tests' rows
	store := proposal.NewStore(pool, &wsID)
	ctx := context.Background()

	for i := range pgProposalRowCapSeed {
		if _, err := store.Create(ctx, proposal.CreateParams{
			WorkspaceID: &wsID,
			Type:        proposal.TypeGoal,
			Payload:     fmt.Appendf(nil, `{"title":"pg rowcap %03d","area":"career"}`, i),
			ProposedBy:  "rowcap-test",
		}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	page, err := store.ListPendingPage(ctx, 20, 0)
	if err != nil {
		t.Fatalf("ListPendingPage: %v", err)
	}
	if len(page) != 20 {
		t.Fatalf("limit 20 over %d proposals returned %d — the SQL LIMIT is not applied",
			pgProposalRowCapSeed, len(page))
	}

	seen := map[uuid.UUID]bool{}
	for offset := 0; offset < pgProposalRowCapSeed; offset += 20 {
		p, pErr := store.ListPendingPage(ctx, 20, int32(offset))
		if pErr != nil {
			t.Fatalf("ListPendingPage(offset=%d): %v", offset, pErr)
		}
		for _, row := range p {
			if seen[row.ID] {
				t.Errorf("proposal %s appeared on two pages — created_at ties are not broken by "+
					"the id tiebreaker", row.ID)
			}
			seen[row.ID] = true
		}
	}
	if len(seen) != pgProposalRowCapSeed {
		t.Errorf("paging enumerated %d of %d proposals — rows fell between pages",
			len(seen), pgProposalRowCapSeed)
	}

	all, err := store.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(all) != pgProposalRowCapSeed {
		t.Errorf("ListPending returned %d of %d — adding LIMIT to the shared query truncated the "+
			"HTTP/dashboard/scheduler callers whose contract is 'all pending rows'",
			len(all), pgProposalRowCapSeed)
	}
}

// TestPGProposalStore_ListPendingPage_NegativeOffsetIsClamped: Postgres
// rejects a negative OFFSET outright, so this passing is the proof that
// db.ClampRowOffset runs on this path — SQLite's leniency would hide it.
func TestPGProposalStore_ListPendingPage_NegativeOffsetIsClamped(t *testing.T) {
	pool := openBatchTestPgPool(t)
	wsID := uuid.New()
	store := proposal.NewStore(pool, &wsID)
	ctx := context.Background()

	if _, err := store.Create(ctx, proposal.CreateParams{
		WorkspaceID: &wsID,
		Type:        proposal.TypeGoal,
		Payload:     []byte(`{"title":"negative offset","area":"career"}`),
		ProposedBy:  "rowcap-test",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rows, err := store.ListPendingPage(ctx, 10, -5)
	if err != nil {
		t.Fatalf("ListPendingPage(offset=-5) errored — the negative offset reached Postgres "+
			"instead of being clamped: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("returned %d rows, want 1 (offset -5 must behave as 0)", len(rows))
	}
}

// TestPGProposalStore_ListPendingPage_EmptyStaysNonNil keeps the
// [] -not-null contract attached to the method the MCP handler now serialises
// (tools_proposal_pg_contract_test.go covers the same property one layer up).
func TestPGProposalStore_ListPendingPage_EmptyStaysNonNil(t *testing.T) {
	pool := openBatchTestPgPool(t)
	wsID := uuid.New() // guaranteed zero rows
	store := proposal.NewStore(pool, &wsID)

	rows, err := store.ListPendingPage(context.Background(), 50, 0)
	if err != nil {
		t.Fatalf("ListPendingPage: %v", err)
	}
	if rows == nil {
		t.Error("ListPendingPage returned a nil slice on an empty workspace; it marshals to " +
			"JSON null, which is the bug the empty-list contract exists to prevent")
	}
}
