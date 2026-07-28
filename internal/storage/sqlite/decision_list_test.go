package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// sqliteBackdateLayout mirrors the unexported sqliteMillisLayout used by
// internal/storage/sqlite/decision.go so tests can write comparable
// created_at values via the exported DB.ExecContext.
const sqliteBackdateLayout = "2006-01-02T15:04:05.000Z"

// logSQLiteDecision logs a decision with title/src plus any opts applied to
// LogParams before Log is called, failing the test on error.
func logSQLiteDecision(
	t *testing.T, s *sqlite.DecisionStore, title string, src decision.Source, opts ...func(*decision.LogParams),
) *db.Decision {
	t.Helper()
	p := decision.LogParams{
		Title: title, Context: "ctx", Decision: "dec", Rationale: "rat", Source: src,
	}
	for _, opt := range opts {
		opt(&p)
	}
	d, err := s.Log(context.Background(), p)
	if err != nil {
		t.Fatalf("Log(%s): %v", title, err)
	}
	return d
}

func TestDecisionStore_List_ManualOnlyByDefault(t *testing.T) {
	_, s := openDecisionDB(t, ":memory:", "")
	manual := logSQLiteDecision(t, s, "manual one", decision.SourceManual)
	logSQLiteDecision(t, s, "auto one", decision.SourceAuto)

	rows, err := s.List(context.Background(), decision.ListParams{Limit: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != manual.ID {
		t.Fatalf("expected only the manual decision, got %+v", rows)
	}
}

func TestDecisionStore_List_IncludeAutoTrue(t *testing.T) {
	_, s := openDecisionDB(t, ":memory:", "")
	manual := logSQLiteDecision(t, s, "manual two", decision.SourceManual)
	auto := logSQLiteDecision(t, s, "auto two", decision.SourceAuto)

	rows, err := s.List(context.Background(), decision.ListParams{Limit: 20, IncludeAuto: true})
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

func TestDecisionStore_List_FilterByProject(t *testing.T) {
	_, s := openDecisionDB(t, ":memory:", "")
	projA, projB := uuid.New(), uuid.New()
	a := logSQLiteDecision(t, s, "in project A", decision.SourceManual, func(p *decision.LogParams) { p.ProjectID = &projA })
	logSQLiteDecision(t, s, "in project B", decision.SourceManual, func(p *decision.LogParams) { p.ProjectID = &projB })

	rows, err := s.List(context.Background(), decision.ListParams{ProjectID: &projA, Limit: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != a.ID {
		t.Fatalf("expected only project A's decision, got %+v", rows)
	}
}

func TestDecisionStore_List_FilterByRepo(t *testing.T) {
	_, s := openDecisionDB(t, ":memory:", "")
	a := logSQLiteDecision(t, s, "in repo A", decision.SourceManual, func(p *decision.LogParams) { p.RepoName = "repo-a" })
	logSQLiteDecision(t, s, "in repo B", decision.SourceManual, func(p *decision.LogParams) { p.RepoName = "repo-b" })

	rows, err := s.List(context.Background(), decision.ListParams{RepoName: "repo-a", Limit: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != a.ID {
		t.Fatalf("expected only repo-a's decision, got %+v", rows)
	}
}

// TestDecisionStore_List_NonexistentProjectReturnsEmpty covers the MCP truth
// table row "project not owned / nonexistent -> returns [] (not an error)"
// at the store layer.
func TestDecisionStore_List_NonexistentProjectReturnsEmpty(t *testing.T) {
	_, s := openDecisionDB(t, ":memory:", "")
	logSQLiteDecision(t, s, "some decision", decision.SourceManual)

	unknownProject := uuid.New()
	rows, err := s.List(context.Background(), decision.ListParams{ProjectID: &unknownProject, Limit: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected empty result for nonexistent project, got %d rows: %+v", len(rows), rows)
	}
}

func TestDecisionStore_List_RejectsConflictingFilter(t *testing.T) {
	_, s := openDecisionDB(t, ":memory:", "")
	proj := uuid.New()
	_, err := s.List(context.Background(), decision.ListParams{ProjectID: &proj, RepoName: "x", Limit: 20})
	if !errors.Is(err, decision.ErrConflictingListFilter) {
		t.Errorf("List() error = %v, want ErrConflictingListFilter", err)
	}
}

func TestDecisionStore_List_RejectsLimitOutOfRange(t *testing.T) {
	_, s := openDecisionDB(t, ":memory:", "")
	for _, limit := range []int32{0, -5, 101, 1000} {
		_, err := s.List(context.Background(), decision.ListParams{Limit: limit})
		if !errors.Is(err, decision.ErrInvalidListLimit) {
			t.Errorf("List(limit=%d) error = %v, want ErrInvalidListLimit", limit, err)
		}
	}
}

// TestDecisionStore_List_SourceFilterAppliedBeforeLimit mirrors the PG test
// of the same name (internal/decision/list_test.go) — dual-backend parity
// (backend-security-design.md §6.5). 3 newer 'auto' rows are seeded above 2
// older 'manual' rows; if LIMIT 2 were applied before excluding 'auto' rows,
// zero manual rows would ever surface.
func TestDecisionStore_List_SourceFilterAppliedBeforeLimit(t *testing.T) {
	d, s := openDecisionDB(t, ":memory:", "")
	ctx := context.Background()

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
	for _, seed := range seeds {
		row := logSQLiteDecision(t, s, seed.title, seed.src)
		err := d.ExecContext(ctx, "UPDATE decisions SET created_at = ?1 WHERE id = ?2",
			seed.at.Format(sqliteBackdateLayout), row.ID.String())
		if err != nil {
			t.Fatalf("backdate %s: %v", seed.title, err)
		}
	}

	rows, err := s.List(ctx, decision.ListParams{Limit: 2})
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

// TestDecisionStore_List_OrderingTiebreaksOnID verifies the "created_at
// DESC, id DESC" ordering contract when two rows share the same created_at.
func TestDecisionStore_List_OrderingTiebreaksOnID(t *testing.T) {
	d, s := openDecisionDB(t, ":memory:", "")
	ctx := context.Background()

	same := time.Now().UTC().Truncate(time.Millisecond).Format(sqliteBackdateLayout)
	a := logSQLiteDecision(t, s, "tie A", decision.SourceManual)
	b := logSQLiteDecision(t, s, "tie B", decision.SourceManual)
	for _, row := range []*db.Decision{a, b} {
		if err := d.ExecContext(ctx, "UPDATE decisions SET created_at = ?1 WHERE id = ?2", same, row.ID.String()); err != nil {
			t.Fatalf("backdate: %v", err)
		}
	}

	rows, err := s.List(ctx, decision.ListParams{Limit: 20})
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

// TestDecisionStore_List_WorkspaceIsolation verifies List never crosses the
// store-scoped workspace boundary, even though ListParams carries no
// workspace field (IDOR threat surface — dispatch prompt).
func TestDecisionStore_List_WorkspaceIsolation(t *testing.T) {
	wsA, wsB := uuid.New().String(), uuid.New().String()
	dsn := "file:decision-list-" + uuid.New().String() + "?mode=memory&cache=shared"
	_, storeA := openDecisionDB(t, dsn, wsA)
	_, storeB := openDecisionDB(t, dsn, wsB)

	logSQLiteDecision(t, storeA, "only in workspace A", decision.SourceManual)

	rows, err := storeB.List(context.Background(), decision.ListParams{Limit: 20})
	if err != nil {
		t.Fatalf("List from workspace B: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("workspace B must not see workspace A's decisions via List, got %d rows: %+v", len(rows), rows)
	}
}

// TestDecisionStore_LegacyReaders_UnfilteredBySource is the P3.0a Stage B
// Step 5 regression: All/ByRepo/ByProject/ByTask must keep returning both
// manual and auto decisions — Stage B only changes the NEW List path.
func TestDecisionStore_LegacyReaders_UnfilteredBySource(t *testing.T) {
	_, s := openDecisionDB(t, ":memory:", "")
	ctx := context.Background()

	proj := uuid.New()
	taskID := uuid.New()
	const repo = "legacy-unfiltered-repo"

	withCommon := func(p *decision.LogParams) {
		p.RepoName = repo
		p.ProjectID = &proj
		p.TaskID = &taskID
	}
	manual := logSQLiteDecision(t, s, "legacy manual", decision.SourceManual, withCommon)
	auto := logSQLiteDecision(t, s, "legacy auto", decision.SourceAuto, withCommon)

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

	all, err := s.All(ctx, 20)
	assertContainsBoth(t, all, err, "All")

	byRepo, err := s.ByRepo(ctx, repo, 20)
	assertContainsBoth(t, byRepo, err, "ByRepo")

	byProject, err := s.ByProject(ctx, proj, 20)
	assertContainsBoth(t, byProject, err, "ByProject")

	byTask, err := s.ByTask(ctx, taskID, 20)
	assertContainsBoth(t, byTask, err, "ByTask")
}

// NOTE: no TestDecisionStore_SearchByCosine_UnfilteredBySource here (unlike
// the PG twin in internal/decision/list_test.go). SQLite's decisions table
// lost its `embedding` column in migration 000026's FK-drop table rebuild
// and never got it back (documented in migrations/HISTORICAL_EXCEPTIONS.md
// and migrations/sqlite/000064_embedding_provider_marker.up.sql's comment).
// DecisionStore.SearchByCosine on SQLite now reports this deliberately via
// decision.ErrCosineUnsupported instead of erroring "no such column:
// embedding" on invocation — see decision_cosine_test.go for the capability
// error's coverage (errors.Is, no-SQL-issued, and no-internal-leak cases).
