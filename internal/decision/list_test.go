package decision_test

import (
	"context"
	"errors"
	"testing"
	"time"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestListParams_Validate is a pure unit test (no DB) for
// decision.ListParams.Validate (P3.0a Stage B Step 2).
func TestListParams_Validate(t *testing.T) {
	pid := uuid.New()
	cases := []struct {
		name    string
		p       decision.ListParams
		wantErr error
	}{
		{"valid neither filter", decision.ListParams{Limit: 20}, nil},
		{"valid project only", decision.ListParams{ProjectID: &pid, Limit: 20}, nil},
		{"valid repo only", decision.ListParams{RepoName: "repo", Limit: 20}, nil},
		{"conflicting project and repo", decision.ListParams{ProjectID: &pid, RepoName: "repo", Limit: 20}, decision.ErrConflictingListFilter},
		{"limit zero rejected", decision.ListParams{Limit: 0}, decision.ErrInvalidListLimit},
		{"limit negative rejected", decision.ListParams{Limit: -1}, decision.ErrInvalidListLimit},
		{"limit over 100 rejected", decision.ListParams{Limit: 101}, decision.ErrInvalidListLimit},
		{"limit exactly 1 accepted", decision.ListParams{Limit: 1}, nil},
		{"limit exactly 100 accepted", decision.ListParams{Limit: 100}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Validate()
			if tc.wantErr == nil {
				if err != nil {
					t.Errorf("Validate() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Validate() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// logTestDecision logs a decision with title/src plus any opts applied to
// LogParams before Log is called, failing the test on error.
func logTestDecision(
	t *testing.T, store *decision.Store, title string, src decision.Source, opts ...func(*decision.LogParams),
) *db.Decision {
	t.Helper()
	p := decision.LogParams{
		Title: title, Context: "ctx", Decision: "dec", Rationale: "rat", Source: src,
	}
	for _, opt := range opts {
		opt(&p)
	}
	d, err := store.Log(context.Background(), p)
	if err != nil {
		t.Fatalf("Log(%s): %v", title, err)
	}
	return d
}

// newIsolatedListStore returns a Store bound to a fresh random workspace so
// this test's rows can never collide with rows left over by sibling tests
// sharing testPgPool, and registers cleanup.
func newIsolatedListStore(t *testing.T, pool *pgxpool.Pool) (*decision.Store, uuid.UUID) {
	t.Helper()
	ws := uuid.New()
	store := decision.NewStore(pool, &ws)
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DELETE FROM decisions WHERE workspace_id = $1", ws); err != nil {
			t.Logf("cleanup workspace %s: %v", ws, err)
		}
	})
	return store, ws
}

func TestStore_List_ManualOnlyByDefault(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	store, _ := newIsolatedListStore(t, pool)

	manual := logTestDecision(t, store, "manual one", decision.SourceManual)
	logTestDecision(t, store, "auto one", decision.SourceAuto)

	rows, err := store.List(ctx, decision.ListParams{Limit: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != manual.ID {
		t.Fatalf("expected only the manual decision, got %+v", rows)
	}
}

func TestStore_List_IncludeAutoTrue(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	store, _ := newIsolatedListStore(t, pool)

	manual := logTestDecision(t, store, "manual two", decision.SourceManual)
	auto := logTestDecision(t, store, "auto two", decision.SourceAuto)

	rows, err := store.List(ctx, decision.ListParams{Limit: 20, IncludeAuto: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected manual+auto (2 rows), got %d: %+v", len(rows), rows)
	}
	gotManual, gotAuto := false, false
	for _, r := range rows {
		gotManual = gotManual || r.ID == manual.ID
		gotAuto = gotAuto || r.ID == auto.ID
	}
	if !gotManual || !gotAuto {
		t.Errorf("expected both manual and auto rows present, got %+v", rows)
	}
}

func TestStore_List_FilterByProject(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	store, _ := newIsolatedListStore(t, pool)

	projA, projB := uuid.New(), uuid.New()
	a := logTestDecision(t, store, "in project A", decision.SourceManual, func(p *decision.LogParams) { p.ProjectID = &projA })
	logTestDecision(t, store, "in project B", decision.SourceManual, func(p *decision.LogParams) { p.ProjectID = &projB })

	rows, err := store.List(ctx, decision.ListParams{ProjectID: &projA, Limit: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != a.ID {
		t.Fatalf("expected only project A's decision, got %+v", rows)
	}
}

func TestStore_List_FilterByRepo(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	store, _ := newIsolatedListStore(t, pool)

	a := logTestDecision(t, store, "in repo A", decision.SourceManual, func(p *decision.LogParams) { p.RepoName = "repo-a" })
	logTestDecision(t, store, "in repo B", decision.SourceManual, func(p *decision.LogParams) { p.RepoName = "repo-b" })

	rows, err := store.List(ctx, decision.ListParams{RepoName: "repo-a", Limit: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != a.ID {
		t.Fatalf("expected only repo-a's decision, got %+v", rows)
	}
}

// TestStore_List_NonexistentProjectReturnsEmpty covers the MCP truth table
// row "project not owned / nonexistent -> returns [] (not an error)" at the
// store layer: a well-formed project_id that matches nothing in this
// workspace must not error.
func TestStore_List_NonexistentProjectReturnsEmpty(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	store, _ := newIsolatedListStore(t, pool)
	logTestDecision(t, store, "some decision", decision.SourceManual)

	unknownProject := uuid.New()
	rows, err := store.List(ctx, decision.ListParams{ProjectID: &unknownProject, Limit: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected empty result for nonexistent project, got %d rows: %+v", len(rows), rows)
	}
}

func TestStore_List_RejectsConflictingFilter(t *testing.T) {
	pool := openTestPgPool(t)
	store := decision.NewStore(pool, nil)
	proj := uuid.New()
	_, err := store.List(context.Background(), decision.ListParams{ProjectID: &proj, RepoName: "x", Limit: 20})
	if !errors.Is(err, decision.ErrConflictingListFilter) {
		t.Errorf("List() error = %v, want ErrConflictingListFilter", err)
	}
}

func TestStore_List_RejectsLimitOutOfRange(t *testing.T) {
	pool := openTestPgPool(t)
	store := decision.NewStore(pool, nil)
	for _, limit := range []int32{0, -5, 101, 1000} {
		_, err := store.List(context.Background(), decision.ListParams{Limit: limit})
		if !errors.Is(err, decision.ErrInvalidListLimit) {
			t.Errorf("List(limit=%d) error = %v, want ErrInvalidListLimit", limit, err)
		}
	}
}

// TestStore_List_SourceFilterAppliedBeforeLimit proves the source filter
// runs inside the SQL WHERE clause rather than as a post-LIMIT slice: 3
// newer 'auto' rows are seeded above 2 older 'manual' rows. If the LIMIT 2
// were applied before excluding 'auto' rows, the top-2 by created_at DESC
// would both be 'auto' and get excluded entirely, leaving zero results. The
// correct (filter-in-WHERE) behaviour must still return the 2 manual rows.
func TestStore_List_SourceFilterAppliedBeforeLimit(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	store, _ := newIsolatedListStore(t, pool)

	base := time.Now().UTC().Truncate(time.Second)
	seeds := []struct {
		title string
		src   decision.Source
		at    time.Time
	}{
		{"oldest manual A", decision.SourceManual, base},
		{"oldest manual B", decision.SourceManual, base.Add(1 * time.Second)},
		{"newest auto A", decision.SourceAuto, base.Add(2 * time.Second)},
		{"newest auto B", decision.SourceAuto, base.Add(3 * time.Second)},
		{"newest auto C", decision.SourceAuto, base.Add(4 * time.Second)},
	}
	for _, s := range seeds {
		d := logTestDecision(t, store, s.title, s.src)
		if _, err := pool.Exec(ctx, "UPDATE decisions SET created_at = $1 WHERE id = $2", s.at, d.ID); err != nil {
			t.Fatalf("backdate %s: %v", s.title, err)
		}
	}

	rows, err := store.List(ctx, decision.ListParams{Limit: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 manual rows (filter-before-limit), got %d: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.Source != string(decision.SourceManual) {
			t.Errorf("expected only manual rows, got source=%q title=%q", r.Source, r.Title)
		}
	}
	if rows[0].Title != "oldest manual B" || rows[1].Title != "oldest manual A" {
		t.Errorf("unexpected created_at DESC order: got [%q, %q], want [%q, %q]",
			rows[0].Title, rows[1].Title, "oldest manual B", "oldest manual A")
	}
}

// TestStore_List_OrderingTiebreaksOnID verifies the "created_at DESC, id
// DESC" ordering contract (P3.0a Stage B Step 3) when two rows share the
// same created_at.
func TestStore_List_OrderingTiebreaksOnID(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	store, _ := newIsolatedListStore(t, pool)

	same := time.Now().UTC().Truncate(time.Millisecond)
	a := logTestDecision(t, store, "tie A", decision.SourceManual)
	b := logTestDecision(t, store, "tie B", decision.SourceManual)
	for _, d := range []*db.Decision{a, b} {
		if _, err := pool.Exec(ctx, "UPDATE decisions SET created_at = $1 WHERE id = $2", same, d.ID); err != nil {
			t.Fatalf("backdate: %v", err)
		}
	}

	rows, err := store.List(ctx, decision.ListParams{Limit: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	wantFirst, wantSecond := a.ID, b.ID
	if b.ID.String() > a.ID.String() {
		wantFirst, wantSecond = b.ID, a.ID
	}
	if rows[0].ID != wantFirst || rows[1].ID != wantSecond {
		t.Errorf("id DESC tiebreak on equal created_at failed: got [%s, %s], want [%s, %s]",
			rows[0].ID, rows[1].ID, wantFirst, wantSecond)
	}
}

// TestStore_List_WorkspaceIsolation verifies List never crosses the
// store-scoped workspace boundary, even though ListParams carries no
// workspace field (IDOR threat surface — dispatch prompt).
func TestStore_List_WorkspaceIsolation(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	storeA, _ := newIsolatedListStore(t, pool)
	storeB, _ := newIsolatedListStore(t, pool)

	logTestDecision(t, storeA, "only in workspace A", decision.SourceManual)

	rows, err := storeB.List(ctx, decision.ListParams{Limit: 20})
	if err != nil {
		t.Fatalf("List from workspace B: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("workspace B must not see workspace A's decisions via List, got %d rows: %+v", len(rows), rows)
	}
}

// TestStore_LegacyReaders_UnfilteredBySource is the P3.0a Stage B Step 5
// regression: All/ByRepo/ByProject/ByTask must keep returning both manual
// and auto decisions — Stage B only changes the NEW List path, not these
// pre-existing read paths.
func TestStore_LegacyReaders_UnfilteredBySource(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	store, _ := newIsolatedListStore(t, pool)

	proj := uuid.New()
	taskID := uuid.New()
	const repo = "legacy-unfiltered-repo"

	withCommon := func(p *decision.LogParams) {
		p.RepoName = repo
		p.ProjectID = &proj
		p.TaskID = &taskID
	}
	manual := logTestDecision(t, store, "legacy manual", decision.SourceManual, withCommon)
	auto := logTestDecision(t, store, "legacy auto", decision.SourceAuto, withCommon)

	assertContainsBoth := func(t *testing.T, rows []db.Decision, err error, label string) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if len(rows) != 2 {
			t.Fatalf("%s: expected 2 rows (unfiltered by source), got %d: %+v", label, len(rows), rows)
		}
		gotManual, gotAuto := false, false
		for _, r := range rows {
			gotManual = gotManual || r.ID == manual.ID
			gotAuto = gotAuto || r.ID == auto.ID
		}
		if !gotManual || !gotAuto {
			t.Errorf("%s: expected both manual and auto rows present, got %+v", label, rows)
		}
	}

	all, err := store.All(ctx, 20)
	assertContainsBoth(t, all, err, "All")

	byRepo, err := store.ByRepo(ctx, repo, 20)
	assertContainsBoth(t, byRepo, err, "ByRepo")

	byProject, err := store.ByProject(ctx, proj, 20)
	assertContainsBoth(t, byProject, err, "ByProject")

	byTask, err := store.ByTask(ctx, taskID, 20)
	assertContainsBoth(t, byTask, err, "ByTask")
}

// TestStore_SearchByCosine_UnfilteredBySource is the SearchByCosine half of
// the Step 5 regression — embeddings have no active writer yet (see
// store.go's SearchByCosine doc), so this test sets the embedding columns
// directly via SQL to exercise the read path.
func TestStore_SearchByCosine_UnfilteredBySource(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	store, _ := newIsolatedListStore(t, pool)

	hp := localai.HashedEmbeddingProvider{}
	vecManual, err := hp.Embed("legacy manual embedding text")
	if err != nil {
		t.Fatalf("Embed manual: %v", err)
	}
	vecAuto, err := hp.Embed("legacy auto embedding text")
	if err != nil {
		t.Fatalf("Embed auto: %v", err)
	}

	manual := logTestDecision(t, store, "cosine manual", decision.SourceManual)
	auto := logTestDecision(t, store, "cosine auto", decision.SourceAuto)

	const setEmbedding = `UPDATE decisions SET embedding = $1, embedding_provider = 'hashed' WHERE id = $2`
	if _, err := pool.Exec(ctx, setEmbedding, localai.SerializeEmbedding(vecManual), manual.ID); err != nil {
		t.Fatalf("set manual embedding: %v", err)
	}
	if _, err := pool.Exec(ctx, setEmbedding, localai.SerializeEmbedding(vecAuto), auto.ID); err != nil {
		t.Fatalf("set auto embedding: %v", err)
	}

	rows, err := store.SearchByCosine(ctx, vecManual, 10)
	if err != nil {
		t.Fatalf("SearchByCosine: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows (unfiltered by source), got %d: %+v", len(rows), rows)
	}
	gotManual, gotAuto := false, false
	for _, r := range rows {
		gotManual = gotManual || r.ID == manual.ID
		gotAuto = gotAuto || r.ID == auto.ID
	}
	if !gotManual || !gotAuto {
		t.Errorf("expected both manual and auto rows present, got %+v", rows)
	}
}
