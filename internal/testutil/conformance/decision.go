package conformance

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/google/uuid"
)

// Backdate sets the created_at column of the decision row identified by id
// to at, bypassing Log's own clock. There is no decision.StoreIface method
// for this — each backend's conformance call site (parity_smoke_test.go)
// supplies it via its own raw SQL handle, the same way the pre-existing
// offset/tiebreaker unit tests in internal/decision/list_test.go and
// internal/storage/sqlite/decision_list_test.go backdate directly through
// their own pool/db handles.
type Backdate func(t *testing.T, id uuid.UUID, at time.Time)

// RunDecisionSmoke runs the shared decision behaviours against store, as
// t.Run subtests named backend+"/"+<op>: Log+ByRepo happy path, invalid-
// Source rejection, SearchByCosine capability divergence [F0929-73], and
// (added [F1003-12]) ByRepo/ByProject offset pagination, the created_at-tie
// id DESC tiebreaker, and repo-filtered offset isolation. Running the same
// assertions against both postgres and sqlite (TestDecisionConformance_*,
// parity_smoke_test.go) is this suite's cross-backend parity guarantee: both
// backends are held to the identical "exact ID sequence" assertion computed
// from their own real generated IDs, not a looser approximate check. Each
// subtest's body is its own named function below (gocyclo) — this function
// itself is just a sequence of t.Run calls, no branching.
func RunDecisionSmoke(t *testing.T, store decision.StoreIface, backend string, backdate Backdate) {
	ctx := context.Background()

	t.Run(backend+"/Log_HappyPath_ThenByRepo", func(t *testing.T) {
		runLogHappyPathThenByRepo(t, ctx, store)
	})
	t.Run(backend+"/Log_InvalidSource_Rejected", func(t *testing.T) {
		runLogInvalidSourceRejected(t, ctx, store)
	})
	t.Run(backend+"/SearchByCosine_CapabilityDivergence", func(t *testing.T) {
		runSearchByCosineCapabilityDivergence(t, ctx, store, backend)
	})
	// [F1003-12] ByRepo/ByProject offset pagination, mirroring the existing
	// List offset tests (internal/decision/list_test.go,
	// internal/storage/sqlite/decision_list_test.go) but against the newly
	// offset-aware ByRepo/ByProject.
	t.Run(backend+"/ByRepo_OffsetPaginatesResults", func(t *testing.T) {
		runByRepoOffsetPaginatesResults(t, ctx, store, backdate)
	})
	t.Run(backend+"/ByProject_OffsetPaginatesResults", func(t *testing.T) {
		runByProjectOffsetPaginatesResults(t, ctx, store, backdate)
	})
	// [F1003-12] id DESC tiebreaker: ≥3 rows sharing the exact same
	// created_at must come back in id-descending order, computed from the
	// real generated IDs (not a guessed arrangement) — see store.go's
	// ByRepo/ByProject doc comments for the ordering contract this pins.
	t.Run(backend+"/ByRepo_TiebreaksOnIdDescForEqualCreatedAt", func(t *testing.T) {
		runByRepoTiebreaksOnIdDescForEqualCreatedAt(t, ctx, store, backdate)
	})
	t.Run(backend+"/ByProject_TiebreaksOnIdDescForEqualCreatedAt", func(t *testing.T) {
		runByProjectTiebreaksOnIdDescForEqualCreatedAt(t, ctx, store, backdate)
	})
	// [F1003-12] repo-filtered offset must never leak rows from another
	// repo — the WHERE repo_name filter has to apply before OFFSET/LIMIT,
	// not after.
	t.Run(backend+"/ByRepo_FilteredOffsetDoesNotLeakOtherRepos", func(t *testing.T) {
		runByRepoFilteredOffsetDoesNotLeakOtherRepos(t, ctx, store, backdate)
	})
}

func runLogHappyPathThenByRepo(t *testing.T, ctx context.Context, store decision.StoreIface) {
	if _, err := store.Log(ctx, decision.LogParams{
		RepoName: "smoke-repo",
		Title:    "t",
		Decision: "d",
		Source:   decision.SourceManual,
	}); err != nil {
		t.Fatalf("Log: %v", err)
	}
	rows, err := store.ByRepo(ctx, "smoke-repo", 10, 0)
	if err != nil {
		t.Fatalf("ByRepo: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ByRepo returned %d rows, want exactly 1", len(rows))
	}
	if rows[0].Title != "t" {
		t.Errorf("Title = %q, want %q", rows[0].Title, "t")
	}
}

func runLogInvalidSourceRejected(t *testing.T, ctx context.Context, store decision.StoreIface) {
	if _, err := store.Log(ctx, decision.LogParams{
		RepoName: "smoke-repo-invalid-source",
		Title:    "t",
		Decision: "d",
		// Source left at its zero value — neither SourceManual nor
		// SourceAuto — must be rejected, not silently accepted.
	}); !errors.Is(err, decision.ErrInvalidSource) {
		t.Fatalf("Log with zero-value Source: err = %v, want errors.Is(err, decision.ErrInvalidSource)", err)
	}
}

func runSearchByCosineCapabilityDivergence(t *testing.T, ctx context.Context, store decision.StoreIface, backend string) {
	rows, err := store.SearchByCosine(ctx, []float32{0.1, 0.2, 0.3}, 5)
	switch backend {
	case "postgres":
		if err != nil {
			t.Fatalf("SearchByCosine (postgres, no embedded decisions): err = %v, want nil", err)
		}
		if len(rows) != 0 {
			t.Errorf("SearchByCosine (postgres, no embedded decisions) = %v, want empty", rows)
		}
	case "sqlite":
		if !errors.Is(err, decision.ErrCosineUnsupported) {
			t.Fatalf("SearchByCosine (sqlite): err = %v, want errors.Is(err, decision.ErrCosineUnsupported)", err)
		}
	default:
		t.Fatalf("unknown backend %q — conformance.RunDecisionSmoke only knows postgres/sqlite", backend)
	}
}

func runByRepoOffsetPaginatesResults(t *testing.T, ctx context.Context, store decision.StoreIface, backdate Backdate) {
	seeded := seedStaggered(t, ctx, store, backdate, "smoke-offset-repo", "", 5)
	fetch := func(limit, offset int32) ([]db.Decision, error) {
		return store.ByRepo(ctx, "smoke-offset-repo", limit, offset)
	}
	assertDecisionOffsetPage(t, fetch, 2, 0, seeded[4], seeded[3])
	assertDecisionOffsetPage(t, fetch, 2, 2, seeded[2], seeded[1])
	assertDecisionOffsetPage(t, fetch, 2, 4, seeded[0])
}

func runByProjectOffsetPaginatesResults(t *testing.T, ctx context.Context, store decision.StoreIface, backdate Backdate) {
	projectID := uuid.New()
	seeded := seedStaggered(t, ctx, store, backdate, "", projectID.String(), 5)
	fetch := func(limit, offset int32) ([]db.Decision, error) {
		return store.ByProject(ctx, projectID, limit, offset)
	}
	assertDecisionOffsetPage(t, fetch, 2, 0, seeded[4], seeded[3])
	assertDecisionOffsetPage(t, fetch, 2, 2, seeded[2], seeded[1])
	assertDecisionOffsetPage(t, fetch, 2, 4, seeded[0])
}

func runByRepoTiebreaksOnIdDescForEqualCreatedAt(t *testing.T, ctx context.Context, store decision.StoreIface, backdate Backdate) {
	ids := seedSameTimestamp(t, ctx, store, backdate, "smoke-tiebreak-repo", "", 4)
	rows, err := store.ByRepo(ctx, "smoke-tiebreak-repo", 10, 0)
	if err != nil {
		t.Fatalf("ByRepo: %v", err)
	}
	assertIDDescOrder(t, rows, ids)
}

func runByProjectTiebreaksOnIdDescForEqualCreatedAt(t *testing.T, ctx context.Context, store decision.StoreIface, backdate Backdate) {
	projectID := uuid.New()
	ids := seedSameTimestamp(t, ctx, store, backdate, "", projectID.String(), 4)
	rows, err := store.ByProject(ctx, projectID, 10, 0)
	if err != nil {
		t.Fatalf("ByProject: %v", err)
	}
	assertIDDescOrder(t, rows, ids)
}

func runByRepoFilteredOffsetDoesNotLeakOtherRepos(t *testing.T, ctx context.Context, store decision.StoreIface, backdate Backdate) {
	const targetRepo = "smoke-filtered-target-repo"
	const noiseRepo = "smoke-filtered-noise-repo"
	target := seedStaggered(t, ctx, store, backdate, targetRepo, "", 3)
	for i := range 2 {
		if _, err := store.Log(ctx, decision.LogParams{
			RepoName: noiseRepo, Title: fmt.Sprintf("noise-%d", i), Decision: "d", Source: decision.SourceManual,
		}); err != nil {
			t.Fatalf("Log noise %d: %v", i, err)
		}
	}
	// target[2] is newest, target[0] oldest; offset=1 limit=2 -> [1, 0].
	rows, err := store.ByRepo(ctx, targetRepo, 2, 1)
	if err != nil {
		t.Fatalf("ByRepo: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != target[1] || rows[1].ID != target[0] {
		t.Fatalf("ByRepo(targetRepo, limit=2, offset=1) = %+v, want [%s, %s]", rows, target[1], target[0])
	}
	for _, r := range rows {
		if !r.RepoName.Valid || r.RepoName.String != targetRepo {
			t.Errorf("leaked a decision from repo %+v, want only %q", r.RepoName, targetRepo)
		}
	}
}

// seedStaggered logs n decisions (either scoped to repoName or projectID —
// exactly one must be non-empty) with created_at staggered 1 second apart
// via backdate, and returns their IDs in insertion order (seeded[n-1] is
// newest, seeded[0] oldest — i.e. created_at DESC order is the reverse of
// this slice).
func seedStaggered(
	t *testing.T, ctx context.Context, store decision.StoreIface, backdate Backdate,
	repoName, projectIDStr string, n int,
) []uuid.UUID {
	t.Helper()
	base := time.Now().UTC().Truncate(time.Second)
	ids := make([]uuid.UUID, 0, n)
	for i := range n {
		p := decision.LogParams{Title: fmt.Sprintf("staggered-%d", i), Decision: "d", Source: decision.SourceManual}
		if repoName != "" {
			p.RepoName = repoName
		}
		if projectIDStr != "" {
			id, err := uuid.Parse(projectIDStr)
			if err != nil {
				t.Fatalf("parse projectIDStr: %v", err)
			}
			p.ProjectID = &id
		}
		d, err := store.Log(ctx, p)
		if err != nil {
			t.Fatalf("Log %d: %v", i, err)
		}
		backdate(t, d.ID, base.Add(time.Duration(i)*time.Second))
		ids = append(ids, d.ID)
	}
	return ids
}

// seedSameTimestamp logs n decisions (either scoped to repoName or
// projectID — exactly one must be non-empty), backdates all of them to the
// exact same created_at via backdate, and returns their generated IDs in
// insertion order (not sorted — assertIDDescOrder computes the expected
// order from these real IDs).
func seedSameTimestamp(
	t *testing.T, ctx context.Context, store decision.StoreIface, backdate Backdate,
	repoName, projectIDStr string, n int,
) []uuid.UUID {
	t.Helper()
	same := time.Now().UTC().Truncate(time.Millisecond)
	ids := make([]uuid.UUID, 0, n)
	for i := range n {
		p := decision.LogParams{Title: fmt.Sprintf("tie-%d", i), Decision: "d", Source: decision.SourceManual}
		if repoName != "" {
			p.RepoName = repoName
		}
		if projectIDStr != "" {
			id, err := uuid.Parse(projectIDStr)
			if err != nil {
				t.Fatalf("parse projectIDStr: %v", err)
			}
			p.ProjectID = &id
		}
		d, err := store.Log(ctx, p)
		if err != nil {
			t.Fatalf("Log %d: %v", i, err)
		}
		backdate(t, d.ID, same)
		ids = append(ids, d.ID)
	}
	return ids
}

// assertDecisionOffsetPage calls fetch(limit, offset) and fails the test
// unless it returns exactly wantIDs, in order.
func assertDecisionOffsetPage(
	t *testing.T, fetch func(limit, offset int32) ([]db.Decision, error), limit, offset int32, wantIDs ...uuid.UUID,
) {
	t.Helper()
	page, err := fetch(limit, offset)
	if err != nil {
		t.Fatalf("fetch offset=%d: %v", offset, err)
	}
	if len(page) != len(wantIDs) {
		t.Fatalf("offset=%d page has %d rows, want %d: %+v", offset, len(page), len(wantIDs), page)
	}
	for i, want := range wantIDs {
		if page[i].ID != want {
			t.Fatalf("offset=%d page[%d] = %s, want %s (full page: %+v)", offset, i, page[i].ID, want, page)
		}
	}
}

// assertIDDescOrder fails the test unless rows' IDs, in order, equal
// seededIDs sorted descending by their string form — the same comparison
// internal/decision/list_test.go's TestStore_List_OrderingTiebreaksOnID
// already uses for 2 rows, generalized to n>=3 here to make a coincidental
// pass far less likely if the id DESC tiebreaker were missing (1/n! chance
// instead of 1/2).
func assertIDDescOrder(t *testing.T, rows []db.Decision, seededIDs []uuid.UUID) {
	t.Helper()
	if len(rows) != len(seededIDs) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(seededIDs), rows)
	}
	want := append([]uuid.UUID{}, seededIDs...)
	sort.Slice(want, func(i, j int) bool { return want[i].String() > want[j].String() })
	for i, w := range want {
		if rows[i].ID != w {
			t.Fatalf("id DESC tiebreak order[%d] = %s, want %s (full rows: %+v, want order: %+v)", i, rows[i].ID, w, rows, want)
		}
	}
}
