package conformance

import (
	"context"
	"slices"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

const (
	gtdAreaChangye = "changye"
	gtdAreaMisc    = "misc"
)

// RunGTDAreaSeedSmoke runs the shared checks that migration 000088's
// 'changye' task_areas row is visible through the real store on both
// backends: TaskAreaExists accepts it (and refuses near-miss spellings), and
// TaskAreaCounts lists it once, in sort_order position, counting a task
// created in it. It is a separate function, not part of RunGTDSmoke, so that
// function's "6 shared behaviours" contract stays true.
//
// [F1011-01] The Postgres half only runs where openTestPgPool does (not under
// -short), so a green -short run proves the SQLite half alone. store's
// workspace must be one the caller owns: the counts check uses before/after
// deltas rather than absolutes because the Postgres pool is shared with
// other tests in the same package.
func RunGTDAreaSeedSmoke(t *testing.T, store gtd.StoreIface, backend string) {
	ctx := context.Background()

	t.Run(backend+"/TaskAreaExists_Changye", func(t *testing.T) { gtdAreaSeedExists(t, ctx, store) })
	t.Run(backend+"/TaskAreaCounts_Changye", func(t *testing.T) { gtdAreaSeedCounts(t, ctx, store) })
}

// gtdAreaSeedExists pins the write-boundary check: 'changye' passes, and the
// near-miss spellings do not (matching is case-sensitive on both backends).
func gtdAreaSeedExists(t *testing.T, ctx context.Context, store gtd.StoreIface) {
	tests := []struct {
		area string
		want bool
	}{
		{area: gtdAreaChangye, want: true},
		{area: "changey", want: false},
		{area: "Changye", want: false},
		{area: "", want: false},
	}
	for _, tt := range tests {
		got, err := store.TaskAreaExists(ctx, tt.area)
		if err != nil {
			t.Fatalf("TaskAreaExists(%q): %v", tt.area, err)
		}
		if got != tt.want {
			t.Errorf("TaskAreaExists(%q) = %v, want %v", tt.area, got, tt.want)
		}
	}
}

// gtdAreaSeedCounts pins the read side: changye is listed once with its label
// between intel-flow and misc, and a pending task created in it moves only
// changye's counts.
func gtdAreaSeedCounts(t *testing.T, ctx context.Context, store gtd.StoreIface) {
	before, err := store.TaskAreaCounts(ctx)
	if err != nil {
		t.Fatalf("TaskAreaCounts (before): %v", err)
	}
	gtdAreaSeedAssertListed(t, before)

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "smoke-area-changye", Area: gtdAreaChangye})
	if err != nil {
		t.Fatalf("CreateTask(Area:changye): %v", err)
	}
	t.Cleanup(func() {
		if err := store.DeleteTask(ctx, task.ID, "smoke-test"); err != nil {
			t.Logf("cleanup: DeleteTask %s: %v", task.ID, err)
		}
	})

	after, err := store.TaskAreaCounts(ctx)
	if err != nil {
		t.Fatalf("TaskAreaCounts (after): %v", err)
	}
	gtdAreaSeedAssertCreateDelta(t, before, after)
}

// gtdAreaSeedAssertListed checks changye appears exactly once, with its label,
// strictly between intel-flow and misc.
func gtdAreaSeedAssertListed(t *testing.T, counts []gtd.AreaCount) {
	t.Helper()
	indexOf := func(area string) int {
		return slices.IndexFunc(counts, func(c gtd.AreaCount) bool { return c.Area == area })
	}

	seen := 0
	for _, c := range counts {
		if c.Area != gtdAreaChangye {
			continue
		}
		seen++
		if c.Label != "Changye Nightmart" {
			t.Errorf("changye Label = %q, want %q", c.Label, "Changye Nightmart")
		}
	}
	if seen != 1 {
		t.Fatalf("TaskAreaCounts lists changye %d times, want exactly once: %+v", seen, counts)
	}

	intel, changye, misc := indexOf("intel-flow"), indexOf(gtdAreaChangye), indexOf(gtdAreaMisc)
	if intel < 0 || misc < 0 {
		t.Fatalf("TaskAreaCounts is missing intel-flow (%d) or misc (%d): %+v", intel, misc, counts)
	}
	if intel >= changye || changye >= misc {
		t.Errorf("order: intel-flow=%d changye=%d misc=%d, want intel-flow < changye < misc", intel, changye, misc)
	}
}

// gtdAreaSeedAssertCreateDelta checks one pending changye task moved changye's
// Pending and Open by exactly +1 and left three other areas' Open unchanged.
func gtdAreaSeedAssertCreateDelta(t *testing.T, before, after []gtd.AreaCount) {
	t.Helper()
	find := func(counts []gtd.AreaCount, area string) gtd.AreaCount {
		i := slices.IndexFunc(counts, func(c gtd.AreaCount) bool { return c.Area == area })
		if i < 0 {
			t.Fatalf("TaskAreaCounts has no %q entry: %+v", area, counts)
		}
		return counts[i]
	}

	b, a := find(before, gtdAreaChangye), find(after, gtdAreaChangye)
	if a.Pending-b.Pending != 1 || a.Open-b.Open != 1 {
		t.Errorf("changye after CreateTask: Pending %d -> %d, Open %d -> %d, want +1 each",
			b.Pending, a.Pending, b.Open, a.Open)
	}
	for _, other := range []string{gtdAreaMisc, "unsorted", "wbt"} {
		bo, ao := find(before, other), find(after, other)
		if ao.Open != bo.Open {
			t.Errorf("%s Open %d -> %d after a changye CreateTask, want unchanged", other, bo.Open, ao.Open)
		}
	}
}
