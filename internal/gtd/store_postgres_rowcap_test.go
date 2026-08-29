package gtd_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// [F170-04]/[F170-05] — Postgres half of the row-cap contract, on
// testcontainers (backend-security-design.md §6.5: a dual-backend project
// needs BOTH backends tested for the same logic; "identical logic" is the
// claim the second test exists to check, not a reason to skip it).
//
// The dialects genuinely differ here: SQLite tolerates a negative OFFSET by
// treating it as 0, while Postgres rejects it outright. db.ClampRowOffset is
// what makes the two agree, and only a real Postgres proves it.

// pgRowCapSeed is larger than any page requested below, so "returned == limit"
// cannot be an artefact of the fixture running out of rows.
const pgRowCapSeed = 60

func TestPGStore_ActiveProjectsPage_CapsAndPages(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New() // fresh workspace: no rows from other tests
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	for i := range pgRowCapSeed {
		if _, err := store.CreateProject(ctx, gtd.CreateProjectParams{
			Name:  fmt.Sprintf("pg-rowcap-%s-%03d", uuid.NewString()[:6], i),
			Title: fmt.Sprintf("PG row cap %03d", i),
			Area:  "eng",
		}); err != nil {
			t.Fatalf("CreateProject %d: %v", i, err)
		}
	}

	page, err := store.ActiveProjectsPage(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ActiveProjectsPage: %v", err)
	}
	if len(page) != 10 {
		t.Fatalf("limit 10 over %d rows returned %d — the SQL LIMIT is not applied on Postgres",
			pgRowCapSeed, len(page))
	}

	seen := map[uuid.UUID]bool{}
	for offset := 0; offset < pgRowCapSeed; offset += 10 {
		p, pErr := store.ActiveProjectsPage(ctx, 10, int32(offset))
		if pErr != nil {
			t.Fatalf("ActiveProjectsPage(offset=%d): %v", offset, pErr)
		}
		for _, row := range p {
			if seen[row.ID] {
				t.Errorf("project %s appeared on two pages — (priority, updated_at) ties are not "+
					"broken, so OFFSET paging repeats and drops rows", row.ID)
			}
			seen[row.ID] = true
		}
	}
	if len(seen) != pgRowCapSeed {
		t.Errorf("paging enumerated %d of %d projects", len(seen), pgRowCapSeed)
	}

	all, err := store.ListActiveProjects(ctx)
	if err != nil {
		t.Fatalf("ListActiveProjects: %v", err)
	}
	if len(all) != pgRowCapSeed {
		t.Errorf("ListActiveProjects returned %d of %d — adding LIMIT to the shared query "+
			"truncated the callers whose contract is 'all rows'", len(all), pgRowCapSeed)
	}
}

// TestPGStore_ActiveProjectsPage_NegativeOffsetIsClamped is the dialect
// difference in its own test: an unclamped negative OFFSET is a hard Postgres
// error ("OFFSET must not be negative"), so this passing proves
// db.ClampRowOffset actually runs on this path rather than being dead code that
// SQLite's leniency hid.
func TestPGStore_ActiveProjectsPage_NegativeOffsetIsClamped(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	if _, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name:  "pg-rowcap-neg-" + uuid.NewString()[:8],
		Title: "negative offset",
		Area:  "eng",
	}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	rows, err := store.ActiveProjectsPage(ctx, 10, -5)
	if err != nil {
		t.Fatalf("ActiveProjectsPage(offset=-5) returned an error — the negative offset reached "+
			"Postgres instead of being clamped: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("returned %d rows, want 1 (offset -5 must behave as 0)", len(rows))
	}
}

// TestPGStore_ActiveProjectsPage_ZeroLimitDoesNotDisableTheCap mirrors the
// SQLite twin: a non-positive limit clamps to the smallest page, never to
// "everything".
func TestPGStore_ActiveProjectsPage_ZeroLimitDoesNotDisableTheCap(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	for i := range 3 {
		if _, err := store.CreateProject(ctx, gtd.CreateProjectParams{
			Name:  fmt.Sprintf("pg-rowcap-zero-%s-%d", uuid.NewString()[:6], i),
			Title: "zero limit",
			Area:  "eng",
		}); err != nil {
			t.Fatalf("CreateProject %d: %v", i, err)
		}
	}

	rows, err := store.ActiveProjectsPage(ctx, 0, 0)
	if err != nil {
		t.Fatalf("ActiveProjectsPage(limit=0): %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("limit=0 returned %d rows, want 1", len(rows))
	}
}

func TestPGStore_ActiveGoalsPage_CapsAndPages(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	for i := range pgRowCapSeed {
		if _, err := store.CreateGoal(ctx, gtd.CreateGoalParams{
			Title: fmt.Sprintf("PG row cap goal %03d", i),
			Area:  "career",
		}); err != nil {
			t.Fatalf("CreateGoal %d: %v", i, err)
		}
	}

	page, err := store.ActiveGoalsPage(ctx, 15, 0)
	if err != nil {
		t.Fatalf("ActiveGoalsPage: %v", err)
	}
	if len(page) != 15 {
		t.Fatalf("limit 15 over %d goals returned %d", pgRowCapSeed, len(page))
	}

	// Every fixture goal has a NULL due_date, so all of them tie under
	// `ORDER BY due_date ASC NULLS LAST` — the id tiebreaker is the only thing
	// keeping these pages disjoint.
	seen := map[uuid.UUID]bool{}
	for offset := 0; offset < pgRowCapSeed; offset += 15 {
		p, pErr := store.ActiveGoalsPage(ctx, 15, int32(offset))
		if pErr != nil {
			t.Fatalf("ActiveGoalsPage(offset=%d): %v", offset, pErr)
		}
		for _, g := range p {
			if seen[g.ID] {
				t.Errorf("goal %s appeared on two pages — NULL due_date ties are not broken", g.ID)
			}
			seen[g.ID] = true
		}
	}
	if len(seen) != pgRowCapSeed {
		t.Errorf("paging enumerated %d of %d goals", len(seen), pgRowCapSeed)
	}

	all, err := store.ActiveGoals(ctx)
	if err != nil {
		t.Fatalf("ActiveGoals: %v", err)
	}
	if len(all) != pgRowCapSeed {
		t.Errorf("ActiveGoals returned %d of %d — the unbounded variant was truncated",
			len(all), pgRowCapSeed)
	}
}
