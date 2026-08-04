//go:build integration

// Cross-backend golden-parity tests for the A5a SessionStart hook rewrite.
// Uses testcontainers Postgres (backend-security-design.md §6.5 — real
// container, never mocked/shared) plus a real file-backed SQLite DB, seeded
// with byte-identical fixture content through the SAME domain Store methods
// production code calls (decision.Store.Log / learning.Store.CreateConcept /
// session.Store.SetHandoff and their SQLite twins), then compares this
// package's own unexported rendering functions — hence package cli, not
// cli_test.
package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// skipCliPgMigrations mirrors the skip list every other package's
// testcontainers-based PG test helper uses (e.g. internal/atom/store_test.go)
// — these .up.sql files use psql `\set`/metacommand syntax pgx cannot parse.
var skipCliPgMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true,
}

// newTestPgPoolAndDSN starts a fresh pgvector/pgvector:pg16 testcontainer,
// applies every embedded migration, and returns both the pool (for direct
// seeding via domain Store methods) and the raw DSN (for
// runSessionStartPostgres, which builds its own hook-tuned pool internally —
// see newHookPgxPool). Both are torn down via t.Cleanup.
func newTestPgPoolAndDSN(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(
		ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_test"),
		tcpostgres.WithUsername("wbt"),
		tcpostgres.WithPassword("wbt"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	applyAllCliPgMigrations(ctx, t, pool)
	return pool, dsn
}

// applyAllCliPgMigrations replays every *.up.sql file in the embedded
// migrations FS in filename order, matching internal/atom/store_test.go's
// applyAllUpMigrationsOnce exactly (kept as a separate unexported copy here
// rather than a shared helper package, matching this repo's existing
// per-package convention — every other testcontainers PG test file defines
// its own copy too).
func applyAllCliPgMigrations(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	entries, err := migrationfs.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded migrations dir: %v", err)
	}
	var ups []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		ups = append(ups, name)
	}
	sort.Strings(ups)

	for _, name := range ups {
		if skipCliPgMigrations[name] {
			continue
		}
		body, err := migrationfs.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}

// ---------------------------------------------------------------------------
// TestFetchDueReviews_PostgresSQLiteGoldenParity — Acceptance: "due-reviews
// PG vs SQLite 逐位元組相同的 golden test".
// ---------------------------------------------------------------------------

func TestFetchDueReviews_PostgresSQLiteGoldenParity(t *testing.T) {
	ctx := context.Background()
	pgPool, _ := newTestPgPoolAndDSN(t)

	sqlitePath := filepath.Join(t.TempDir(), "due-reviews-fixture.db")
	sdb, err := wbtsqlite.Open(ctx, sqlitePath, "")
	if err != nil {
		t.Fatalf("sqlite Open: %v", err)
	}

	// CreateConcept auto-creates a review_schedule row whose due_date
	// defaults to "now" at insert time (both backends), so each freshly
	// created concept is immediately overdue — no manual due_date wrangling
	// needed. A short sleep between the two inserts guarantees a
	// distinguishable due_date (ORDER BY due_date ASC) on both backends.
	wantTitles := []string{"first overdue concept", "second overdue concept"}
	pgLearning := learning.NewStore(pgPool, nil)
	sqliteLearning := wbtsqlite.NewLearningStore(sdb)
	for _, title := range wantTitles {
		if _, err := pgLearning.CreateConcept(ctx, title, "fixture content", nil); err != nil {
			t.Fatalf("pg CreateConcept(%q): %v", title, err)
		}
		if _, err := sqliteLearning.CreateConcept(ctx, title, "fixture content", nil); err != nil {
			t.Fatalf("sqlite CreateConcept(%q): %v", title, err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	pgTitles := fetchDueReviewsPostgres(ctx, pgPool, nil, dueReviewsLimit)
	sqliteTitles := fetchDueReviewsSQLite(ctx, sdb, dueReviewsLimit)
	if err := sdb.Close(); err != nil {
		t.Fatalf("sqlite Close: %v", err)
	}

	if !reflect.DeepEqual(pgTitles, sqliteTitles) {
		t.Fatalf("due review titles differ:\n  postgres = %#v\n  sqlite   = %#v", pgTitles, sqliteTitles)
	}
	if !reflect.DeepEqual(pgTitles, wantTitles) {
		t.Fatalf("due review titles = %#v, want %#v (ORDER BY due_date ASC = insertion order)", pgTitles, wantTitles)
	}
}

// ---------------------------------------------------------------------------
// TestRenderSessionContext_PostgresSQLiteGoldenParity — Acceptance: "跨後端
// golden test：相同 fixture 下 PG 與 SQLite 的 section 內容與順序一致".
// Seeds one unresolved handoff + two decisions (identical content, and
// identical *relative* insertion order) into both backends, drives the real
// runSessionStartPostgres/runSessionStartSQLite + renderSessionContext code
// paths, and asserts the two rendered systemMessage strings are identical.
// ---------------------------------------------------------------------------

// boundaryTokenPattern matches wrapUntrustedLocalDB's per-call random
// boundary-token line (context_render.go's C-1 fix, PR #151 second-army
// review) — boundaryTokenLabel followed by exactly boundaryNonceBytes*2 hex
// chars. Tied to those two constants (not hardcoded) so a future change to
// the token's label or length keeps this pattern correct instead of
// silently under/over-matching.
var boundaryTokenPattern = regexp.MustCompile(
	regexp.QuoteMeta(boundaryTokenLabel) + fmt.Sprintf("[0-9a-f]{%d}", boundaryNonceBytes*2),
)

// normalizeBoundaryToken replaces wrapUntrustedLocalDB's per-call random
// boundary-token lines with a fixed placeholder, used ONLY for the
// cross-backend equality check in TestRenderSessionContext_
// PostgresSQLiteGoldenParity below. The token is crypto-random per call by
// design (C-1: it must not be derivable from DB content, or an attacker who
// controls that content could pre-compute a matching value to forge the
// boundary) — it always differs between the two renderSessionContext calls
// below even for byte-identical fixture data. This normalization is what
// lets the test keep verifying "backend rendering logic doesn't diverge"
// without asserting the two random tokens are equal, which would be both
// the wrong thing to assert and structurally impossible to satisfy.
func normalizeBoundaryToken(s string) string {
	return boundaryTokenPattern.ReplaceAllString(s, boundaryTokenLabel+"<normalized>")
}

func TestRenderSessionContext_PostgresSQLiteGoldenParity(t *testing.T) {
	ctx := context.Background()
	pgPool, pgDSN := newTestPgPoolAndDSN(t)

	sqlitePath := filepath.Join(t.TempDir(), "render-parity-fixture.db")
	sdb, err := wbtsqlite.Open(ctx, sqlitePath, "")
	if err != nil {
		t.Fatalf("sqlite Open: %v", err)
	}

	// Intent/ContextSummary are crafted to keyword-match ONLY "beta decision"
	// below (via the word "beta") — see the decisions comment for why this
	// matters: contextpack.capPerType (scorer.go) tie-breaks EQUAL-score
	// items within a type by Item.ID.String() (ascending), and Item.ID is a
	// backend-generated random UUID that differs between the Postgres and
	// SQLite rows for "the same" logical decision. Two decisions that tied
	// in score would therefore render in a relative order that is NOT
	// guaranteed to agree between backends — a real trap this test fell into
	// during development (see git history) before switching to a
	// keyword-differentiated score. "the"/"decision" are deliberately
	// avoided here so they don't accidentally keyword-match BOTH decisions'
	// titles and re-collapse the intended score gap back to a tie.
	handoffParams := session.HandoffParams{
		Intent:         "investigate the beta issue",
		ContextSummary: "focus on beta findings",
	}
	if _, err := session.NewStore(pgPool, nil).SetHandoff(ctx, handoffParams); err != nil {
		t.Fatalf("pg SetHandoff: %v", err)
	}
	if _, err := wbtsqlite.NewSessionStore(sdb).SetHandoff(ctx, handoffParams); err != nil {
		t.Fatalf("sqlite SetHandoff: %v", err)
	}

	// "beta decision" keyword-matches the objective above (+0.20, scorer.go)
	// on top of the +0.10 recent / +0.15 logged-decision every decision here
	// gets, so it deterministically outscores "alpha decision" (0.45 vs
	// 0.25) regardless of either backend's randomly-generated Item.ID —
	// removing the tie-break hazard described above. Decision/Context/
	// Rationale text is deliberately free of every objective keyword ("the",
	// "beta", "issue", "focus", "findings") for "alpha decision" so it stays
	// at exactly 0.25.
	decisions := []decision.LogParams{
		{Title: "alpha decision", Context: "c1", Decision: "unrelated filler content", Rationale: "r1", Source: decision.SourceManual},
		{Title: "beta decision", Context: "c2", Decision: "directly relevant beta content", Rationale: "r2", Source: decision.SourceManual},
	}
	pgDecisions := decision.NewStore(pgPool, nil)
	sqliteDecisions := wbtsqlite.NewDecisionStore(sdb)
	for _, p := range decisions {
		if _, err := pgDecisions.Log(ctx, p); err != nil {
			t.Fatalf("pg Log(%q): %v", p.Title, err)
		}
		if _, err := sqliteDecisions.Log(ctx, p); err != nil {
			t.Fatalf("sqlite Log(%q): %v", p.Title, err)
		}
	}

	if err := sdb.Close(); err != nil {
		t.Fatalf("sqlite Close: %v", err)
	}
	t.Setenv("SQLITE_PATH", sqlitePath)

	pgPack, pgHandoff, pgDueReviews := runSessionStartPostgres(ctx, nil, pgDSN)
	sqlitePack, sqliteHandoff, sqliteDueReviews := runSessionStartSQLite(ctx, nil)

	pgRendered := renderSessionContext(pgPack, pgHandoff, pgDueReviews)
	sqliteRendered := renderSessionContext(sqlitePack, sqliteHandoff, sqliteDueReviews)

	if pgRendered == "" || sqliteRendered == "" {
		t.Fatalf("expected non-empty rendered output on both backends; postgres=%q sqlite=%q", pgRendered, sqliteRendered)
	}
	// Compare with boundary tokens normalized (see normalizeBoundaryToken's
	// doc comment) — the two calls above legitimately get different
	// crypto-random tokens by design, that is not a backend divergence.
	if normalizeBoundaryToken(pgRendered) != normalizeBoundaryToken(sqliteRendered) {
		t.Fatalf("rendered systemMessage differs between backends (after normalizing per-call boundary tokens):\n--- postgres ---\n%s\n--- sqlite ---\n%s", pgRendered, sqliteRendered)
	}

	betaIdx := strings.Index(pgRendered, "beta decision")
	alphaIdx := strings.Index(pgRendered, "alpha decision")
	if betaIdx < 0 || alphaIdx < 0 || betaIdx > alphaIdx {
		t.Errorf("expected \"beta decision\" (higher score via keyword match) to render before \"alpha decision\" on both backends; postgres output:\n%s", pgRendered)
	}
}
