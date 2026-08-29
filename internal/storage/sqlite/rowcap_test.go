package sqlite_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// [F170-04]/[F170-05]/[F170-06] — SQLite half of the row-cap contract.
//
// These exist because the MCP handler tests could NOT catch a missing store
// cap: the handler slices to `limit` after the store returns, so replacing
// ActiveProjectsPage with the uncapped ListActiveProjects there leaves every
// handler assertion green (measured — that exact mutation passed
// TestF170_04_ListProjectsRowCap). The SQL LIMIT therefore needs its own
// probe, at the layer that actually issues it.
//
// The Postgres twins live in internal/gtd/store_postgres_rowcap_test.go and
// internal/proposal/store_postgres_rowcap_test.go
// (backend-security-design.md §6.5: a dual-backend project needs both, and
// "the logic is identical" is the claim testcontainers exists to check).

// rowCapStoreSeed is deliberately larger than any page these tests request, so
// "returned == limit" cannot be satisfied by the fixture simply running out.
const rowCapStoreSeed = 120

func seedRowCapProjects(t *testing.T, s *sqlite.GTDStore, n int) {
	t.Helper()
	ctx := context.Background()
	for i := range n {
		if _, err := s.CreateProject(ctx, gtd.CreateProjectParams{
			Name:  fmt.Sprintf("rowcap-%04d", i),
			Title: fmt.Sprintf("Row cap %04d", i),
			Area:  "eng",
		}); err != nil {
			t.Fatalf("CreateProject %d: %v", i, err)
		}
	}
}

// TestSQLiteStore_ActiveProjectsPage_CapsAndPages pins the three properties
// the SQL clause exists for: the LIMIT bounds the result, OFFSET advances it,
// and the two together enumerate every row exactly once.
func TestSQLiteStore_ActiveProjectsPage_CapsAndPages(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()
	seedRowCapProjects(t, s, rowCapStoreSeed)

	first, err := s.ActiveProjectsPage(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ActiveProjectsPage: %v", err)
	}
	if len(first) != 10 {
		t.Fatalf("limit 10 over %d rows returned %d — the SQL LIMIT is not being applied",
			rowCapStoreSeed, len(first))
	}

	seen := map[uuid.UUID]bool{}
	for offset := 0; offset < rowCapStoreSeed; offset += 10 {
		page, pErr := s.ActiveProjectsPage(ctx, 10, int32(offset))
		if pErr != nil {
			t.Fatalf("ActiveProjectsPage(offset=%d): %v", offset, pErr)
		}
		for _, p := range page {
			if seen[p.ID] {
				t.Errorf("project %s appeared on two pages — ORDER BY is not a total order, so "+
					"OFFSET paging repeats and drops rows", p.ID)
			}
			seen[p.ID] = true
		}
	}
	if len(seen) != rowCapStoreSeed {
		t.Errorf("paging enumerated %d of %d projects — rows fell between pages",
			len(seen), rowCapStoreSeed)
	}

	past, err := s.ActiveProjectsPage(ctx, 10, rowCapStoreSeed+50)
	if err != nil {
		t.Fatalf("ActiveProjectsPage past the end: %v", err)
	}
	if len(past) != 0 {
		t.Errorf("offset past the end returned %d rows, want 0", len(past))
	}
}

// TestSQLiteStore_ActiveProjectsPage_ZeroLimitDoesNotDisableTheCap pins
// db.ClampRowLimit's deliberate choice: a zero limit is almost always an unset
// field, and resolving it to "no cap" is how a pagination guard gets switched
// off by accident.
func TestSQLiteStore_ActiveProjectsPage_ZeroLimitDoesNotDisableTheCap(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()
	seedRowCapProjects(t, s, rowCapStoreSeed)

	for _, limit := range []int32{0, -1} {
		rows, err := s.ActiveProjectsPage(ctx, limit, 0)
		if err != nil {
			t.Fatalf("ActiveProjectsPage(limit=%d): %v", limit, err)
		}
		if len(rows) != 1 {
			t.Errorf("limit=%d returned %d rows, want 1 — a non-positive limit must clamp to the "+
				"smallest page, never to 'everything'", limit, len(rows))
		}
	}
}

// TestSQLiteStore_ListActiveProjects_StaysUnbounded is the other half of the
// contract, and the reason the legacy method was kept: its callers (HTTP
// dashboard, context handler, qa-seed) asked for every row before this change
// and must still get every row.
func TestSQLiteStore_ListActiveProjects_StaysUnbounded(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()
	seedRowCapProjects(t, s, rowCapStoreSeed)

	all, err := s.ListActiveProjects(ctx)
	if err != nil {
		t.Fatalf("ListActiveProjects: %v", err)
	}
	if len(all) != rowCapStoreSeed {
		t.Errorf("ListActiveProjects returned %d of %d rows — adding the LIMIT clause silently "+
			"truncated the callers whose contract is 'all rows'", len(all), rowCapStoreSeed)
	}
}

// TestSQLiteStore_ActiveGoalsPage_CapsAndPages is [F170-05]'s store-level
// probe. Goals are the harder case: none of these fixtures has a due_date, so
// every row ties under `ORDER BY due_date ASC NULLS LAST` and only the id
// tiebreaker makes paging deterministic.
func TestSQLiteStore_ActiveGoalsPage_CapsAndPages(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()
	for i := range rowCapStoreSeed {
		if _, err := s.CreateGoal(ctx, gtd.CreateGoalParams{
			Title: fmt.Sprintf("Row cap goal %04d", i),
			Area:  "career",
		}); err != nil {
			t.Fatalf("CreateGoal %d: %v", i, err)
		}
	}

	page, err := s.ActiveGoalsPage(ctx, 25, 0)
	if err != nil {
		t.Fatalf("ActiveGoalsPage: %v", err)
	}
	if len(page) != 25 {
		t.Fatalf("limit 25 over %d goals returned %d", rowCapStoreSeed, len(page))
	}

	seen := map[uuid.UUID]bool{}
	for offset := 0; offset < rowCapStoreSeed; offset += 25 {
		p, pErr := s.ActiveGoalsPage(ctx, 25, int32(offset))
		if pErr != nil {
			t.Fatalf("ActiveGoalsPage(offset=%d): %v", offset, pErr)
		}
		for _, g := range p {
			if seen[g.ID] {
				t.Errorf("goal %s appeared on two pages — due_date ties are not broken", g.ID)
			}
			seen[g.ID] = true
		}
	}
	if len(seen) != rowCapStoreSeed {
		t.Errorf("paging enumerated %d of %d goals", len(seen), rowCapStoreSeed)
	}

	all, err := s.ActiveGoals(ctx)
	if err != nil {
		t.Fatalf("ActiveGoals: %v", err)
	}
	if len(all) != rowCapStoreSeed {
		t.Errorf("ActiveGoals returned %d of %d — the unbounded variant was truncated",
			len(all), rowCapStoreSeed)
	}
}

// TestSQLiteProposalStore_ListPendingPage_CapsAndPages is [F170-06]'s
// store-level probe.
func TestSQLiteProposalStore_ListPendingPage_CapsAndPages(t *testing.T) {
	s := openProposalStore(t, ":memory:", "")
	ctx := context.Background()
	for i := range rowCapStoreSeed {
		if _, err := s.Create(ctx, proposal.CreateParams{
			Type:       proposal.TypeGoal,
			Payload:    fmt.Appendf(nil, `{"title":"rowcap %04d","area":"career"}`, i),
			ProposedBy: "rowcap-test",
		}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}

	page, err := s.ListPendingPage(ctx, 20, 0)
	if err != nil {
		t.Fatalf("ListPendingPage: %v", err)
	}
	if len(page) != 20 {
		t.Fatalf("limit 20 over %d proposals returned %d", rowCapStoreSeed, len(page))
	}

	seen := map[uuid.UUID]bool{}
	for offset := 0; offset < rowCapStoreSeed; offset += 20 {
		p, pErr := s.ListPendingPage(ctx, 20, int32(offset))
		if pErr != nil {
			t.Fatalf("ListPendingPage(offset=%d): %v", offset, pErr)
		}
		for _, row := range p {
			if seen[row.ID] {
				t.Errorf("proposal %s appeared on two pages — created_at ties are not broken", row.ID)
			}
			seen[row.ID] = true
		}
	}
	if len(seen) != rowCapStoreSeed {
		t.Errorf("paging enumerated %d of %d proposals", len(seen), rowCapStoreSeed)
	}

	all, err := s.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(all) != rowCapStoreSeed {
		t.Errorf("ListPending returned %d of %d — the unbounded variant was truncated",
			len(all), rowCapStoreSeed)
	}
}

// TestSQLiteProposalStore_ListPendingPage_EmptyStaysNonNil keeps the
// [] -not-null contract (empty_list_contract_test.go's SQLite half) attached to
// the paged variant too — the MCP handler now serialises THIS method's result.
func TestSQLiteProposalStore_ListPendingPage_EmptyStaysNonNil(t *testing.T) {
	s := openProposalStore(t, ":memory:", "")

	rows, err := s.ListPendingPage(context.Background(), 50, 0)
	if err != nil {
		t.Fatalf("ListPendingPage: %v", err)
	}
	if rows == nil {
		t.Error("ListPendingPage returned a nil slice on an empty table; it marshals to JSON null")
	}
}
