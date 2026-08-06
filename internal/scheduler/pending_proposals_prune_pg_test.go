package scheduler

import (
	"bytes"
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// schedulerSkipMigrations are .up.sql files the prune-test runner MUST skip;
// matches the proposal package's batchSkipMigrations.
var schedulerSkipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true, // psql `\set` metacommand
}

var testPgPool *pgxpool.Pool

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if testing.Short() {
		return m.Run()
	}
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_sched_test"),
		tcpostgres.WithUsername("wbt"),
		tcpostgres.WithPassword("wbt"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Fatalf("start postgres container: %v", err)
	}
	defer func() { _ = c.Terminate(ctx) }()

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Printf("get connection string: %v", err)
		return 1
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Printf("pgxpool.New: %v", err)
		return 1
	}
	defer pool.Close()

	applyAllUpMigrationsOnce(ctx, pool)

	testPgPool = pool
	return m.Run()
}

func applyAllUpMigrationsOnce(ctx context.Context, pool *pgxpool.Pool) {
	entries, err := migrationfs.FS.ReadDir(".")
	if err != nil {
		log.Fatalf("read embedded migrations dir: %v", err)
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
		if schedulerSkipMigrations[name] {
			continue
		}
		body, err := migrationfs.FS.ReadFile(name)
		if err != nil {
			log.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			log.Fatalf("apply %s: %v", name, err)
		}
	}
}

// openSchedulerTestPgPool returns the package-level singleton pool initialised in TestMain.
// Skips with -short.
func openSchedulerTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return testPgPool
}

// TestScheduler_DailyPendingProposalsPrune_DeletesOnlyExpiredRows seeds the
// pending_proposals table with a mix of in-window + out-of-window rows then
// runs the prune. Asserts:
//   - resolved (accepted/rejected) rows older than 90 days are deleted
//   - pending decision rows older than 180 days are deleted
//   - other pending types older than 180 days are KEPT (user intent)
//   - in-window rows are KEPT
func TestScheduler_DailyPendingProposalsPrune_DeletesOnlyExpiredRows(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()

	// Seed rows with explicit created_at / resolved_at so we can assert
	// deterministic prune behaviour. Helper builds INSERT for clarity.
	type seed struct {
		id         uuid.UUID
		typ        string
		status     string
		createdAt  time.Time
		resolvedAt *time.Time
		shouldKeep bool
		label      string
	}
	now := time.Now().UTC()
	resolved100Days := now.AddDate(0, 0, -100)    // outside 90d
	resolved10Days := now.AddDate(0, 0, -10)      // inside 90d
	pendingDecision200 := now.AddDate(0, 0, -200) // outside 180d
	pendingDecision30 := now.AddDate(0, 0, -30)   // inside 180d
	pendingGoal400 := now.AddDate(0, 0, -400)     // outside 180d but type=goal MUST be kept

	rt100 := resolved100Days
	rt10 := resolved10Days

	seeds := []seed{
		{uuid.New(), "decision", "accepted", resolved100Days, &rt100, false, "accepted decision >90d"},
		{uuid.New(), "concept", rejectedStatus, resolved100Days, &rt100, false, "rejected concept >90d"},
		{uuid.New(), "decision", "accepted", resolved10Days, &rt10, true, "accepted decision <90d"},
		{uuid.New(), "decision", pendingStatus, pendingDecision200, nil, false, "pending decision >180d"},
		{uuid.New(), "decision", pendingStatus, pendingDecision30, nil, true, "pending decision <180d"},
		{uuid.New(), "goal", pendingStatus, pendingGoal400, nil, true, "pending goal >180d (user intent)"},
		{uuid.New(), "concept", pendingStatus, pendingGoal400, nil, true, "pending concept >180d (user intent)"},
	}

	for _, s := range seeds {
		var resolvedArg any
		if s.resolvedAt != nil {
			resolvedArg = *s.resolvedAt
		}
		_, err := pool.Exec(ctx, `INSERT INTO pending_proposals
			(id, type, payload, status, created_at, resolved_at)
			VALUES ($1, $2, '{}'::jsonb, $3, $4, $5)`,
			s.id, s.typ, s.status, s.createdAt, resolvedArg)
		if err != nil {
			t.Fatalf("seed %s: %v", s.label, err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", s.id)
		})
	}

	sc := &Scheduler{disciplinePool: pool}
	sc.runDailyPendingProposalsPrune()

	// Assert each seed: kept rows still exist, dropped rows are gone.
	for _, s := range seeds {
		var count int
		err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM pending_proposals WHERE id = $1", s.id).Scan(&count)
		if err != nil {
			t.Fatalf("count %s: %v", s.label, err)
		}
		if s.shouldKeep && count != 1 {
			t.Errorf("%s: count = %d, want 1 (should be kept)", s.label, count)
		}
		if !s.shouldKeep && count != 0 {
			t.Errorf("%s: count = %d, want 0 (should be deleted)", s.label, count)
		}
	}
}

// TestRunDailyPendingProposalsPrune_TypeTaskTTL exercises the round-1
// follow-up GTD 947f3f12: pending TypeTask proposals older than 30 days are
// marked status='rejected', reason='ttl-expired-30d', resolved_at=NOW() —
// they are NOT deleted (they age out through the 90d resolved retention so
// the audit trail survives). Fresh TypeTask + already-resolved TypeTask
// rows are left untouched.
func TestRunDailyPendingProposalsPrune_TypeTaskTTL(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()

	now := time.Now().UTC()
	freshCreated := now.AddDate(0, 0, -1)      // 1 day old, inside 30d → keep pending
	staleCreated := now.AddDate(0, 0, -31)     // 31 days old → mark rejected
	staleAccepted := now.AddDate(0, 0, -31)    // already accepted, shouldn't be re-touched
	staleAcceptedRes := now.AddDate(0, 0, -29) // accepted within last 30d (inside 90d resolved retention)

	freshID := uuid.New()
	staleID := uuid.New()
	staleAcceptedID := uuid.New()

	insert := func(id uuid.UUID, status string, createdAt time.Time, resolvedAt *time.Time) {
		t.Helper()
		var resolvedArg any
		if resolvedAt != nil {
			resolvedArg = *resolvedAt
		}
		if _, err := pool.Exec(ctx, `INSERT INTO pending_proposals
			(id, type, payload, status, created_at, resolved_at)
			VALUES ($1, 'task', '{}'::jsonb, $2, $3, $4)`,
			id, status, createdAt, resolvedArg); err != nil {
			t.Fatalf("seed %s status=%s: %v", id, status, err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", id)
		})
	}

	insert(freshID, pendingStatus, freshCreated, nil)
	insert(staleID, pendingStatus, staleCreated, nil)
	rt := staleAcceptedRes
	insert(staleAcceptedID, "accepted", staleAccepted, &rt)

	sc := &Scheduler{disciplinePool: pool}
	sc.runDailyPendingProposalsPrune()

	// staleID: should now be rejected with reason='ttl-expired-30d'.
	var (
		gotStatus string
		gotReason *string
		gotRes    *time.Time
	)
	err := pool.QueryRow(ctx,
		"SELECT status, reason, resolved_at FROM pending_proposals WHERE id = $1",
		staleID).Scan(&gotStatus, &gotReason, &gotRes)
	if err != nil {
		t.Fatalf("query staleID: %v", err)
	}
	if gotStatus != rejectedStatus {
		t.Errorf("stale TypeTask: status = %q, want %q", gotStatus, rejectedStatus)
	}
	if gotReason == nil || *gotReason != "ttl-expired-30d" {
		t.Errorf("stale TypeTask: reason = %v, want %q", gotReason, "ttl-expired-30d")
	}
	if gotRes == nil {
		t.Errorf("stale TypeTask: resolved_at = nil, want NOW()-ish")
	}

	// freshID: still pending, reason nil.
	var freshStatus string
	var freshReason *string
	if err := pool.QueryRow(ctx,
		"SELECT status, reason FROM pending_proposals WHERE id = $1",
		freshID).Scan(&freshStatus, &freshReason); err != nil {
		t.Fatalf("query freshID: %v", err)
	}
	if freshStatus != pendingStatus {
		t.Errorf("fresh TypeTask: status = %q, want %q", freshStatus, pendingStatus)
	}
	if freshReason != nil {
		t.Errorf("fresh TypeTask: reason = %q, want nil", *freshReason)
	}

	// staleAcceptedID: untouched (was already accepted, still inside 90d).
	var accStatus string
	var accReason *string
	if err := pool.QueryRow(ctx,
		"SELECT status, reason FROM pending_proposals WHERE id = $1",
		staleAcceptedID).Scan(&accStatus, &accReason); err != nil {
		t.Fatalf("query staleAcceptedID: %v", err)
	}
	if accStatus != "accepted" {
		t.Errorf("stale-accepted TypeTask: status = %q, want %q", accStatus, "accepted")
	}
	if accReason != nil {
		t.Errorf("stale-accepted TypeTask: reason = %q, want nil (not re-touched)", *accReason)
	}
}

// TestScheduler_PendingProposalsPrune_EmptyTableNoPanic verifies the prune
// query is safe to run against an empty table — production may go days with
// no rows to drop.
func TestScheduler_PendingProposalsPrune_EmptyTableNoPanic(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	sc := &Scheduler{disciplinePool: pool}
	// Must not panic on zero-row delete.
	sc.runDailyPendingProposalsPrune()
}

func TestScheduler_DailyGuardPrune_DeletesExpiredRows(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()
	old := time.Now().UTC().AddDate(0, 0, -45)
	recent := time.Now().UTC().AddDate(0, 0, -5)

	oldEventID := uuid.New()
	recentEventID := uuid.New()
	oldBypassID := uuid.New()
	recentBypassID := uuid.New()

	seedGuardEvent := func(id uuid.UUID, createdAt time.Time) {
		t.Helper()
		_, err := pool.Exec(ctx, `INSERT INTO guard_events
			(id, session_id, cwd, repo_name, tool_name, tool_input, risk_tier, risk_reason, would_deny, matcher, created_at)
			VALUES ($1, 's', '/repo', 'repo', 'Bash', '{}', 1, 'test', false, 'test', $2)`,
			id, createdAt)
		if err != nil {
			t.Fatalf("seed guard_event %s: %v", id, err)
		}
	}
	seedBypass := func(id uuid.UUID, createdAt time.Time) {
		t.Helper()
		_, err := pool.Exec(ctx, `INSERT INTO guard_bypasses
			(id, created_at, scope, target, reason, created_by)
			VALUES ($1, $2, 'repo', 'repo', 'test', 'test')`,
			id, createdAt)
		if err != nil {
			t.Fatalf("seed guard_bypass %s: %v", id, err)
		}
	}

	seedGuardEvent(oldEventID, old)
	seedGuardEvent(recentEventID, recent)
	seedBypass(oldBypassID, old)
	seedBypass(recentBypassID, recent)

	sc := &Scheduler{disciplinePool: pool}
	sc.runDailyGuardPrune()

	assertGuardEventsCount := func(id uuid.UUID, want int) {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM guard_events WHERE id = $1", id).Scan(&count); err != nil {
			t.Fatalf("count guard_events %s: %v", id, err)
		}
		if count != want {
			t.Fatalf("guard_events id %s count = %d, want %d", id, count, want)
		}
	}
	assertGuardBypassesCount := func(id uuid.UUID, want int) {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM guard_bypasses WHERE id = $1", id).Scan(&count); err != nil {
			t.Fatalf("count guard_bypasses %s: %v", id, err)
		}
		if count != want {
			t.Fatalf("guard_bypasses id %s count = %d, want %d", id, count, want)
		}
	}
	assertGuardEventsCount(oldEventID, 0)
	assertGuardEventsCount(recentEventID, 1)
	assertGuardBypassesCount(oldBypassID, 0)
	assertGuardBypassesCount(recentBypassID, 1)
}

func TestScheduler_DailyGuardPrune_EmptyTableNoPanic(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	sc := &Scheduler{disciplinePool: pool}
	sc.runDailyGuardPrune()
}

// TestRunDailyPendingProposalsPrune_PartialSuccess_LogsWarn exercises the
// partial-success telemetry path (GTD 0ba91291): when the mark step
// successfully flags ≥1 stale TypeTask row, but the shared ctx deadline
// trips DURING the DELETE step, the function MUST emit a distinct
// warn-level log naming the situation ("mark succeeded but delete timed
// out") so operators have actionable signal — distinct from a generic
// "DELETE failed" warn.
//
// Strategy (chosen): shrink pendingProposalsPruneTimeout to a small window
// + install a between-steps hook that sleeps past the remaining deadline.
// This is deterministic on real Postgres (mark completes in <50 ms on
// near-empty test table; hook sleeps long enough that DELETE always sees
// DeadlineExceeded). The mock-pool alt was not adopted because the
// scheduler holds *pgxpool.Pool directly, and refactoring to an interface
// solely for this test would be a much larger blast radius.
func TestRunDailyPendingProposalsPrune_PartialSuccess_LogsWarn(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()

	// Seed 1 stale (>30d) pending TypeTask so the mark step has a row to
	// affect → markedRows > 0 → partial-success branch is reachable.
	staleID := uuid.New()
	staleCreated := time.Now().UTC().AddDate(0, 0, -31)
	if _, err := pool.Exec(ctx, `INSERT INTO pending_proposals
		(id, type, payload, status, created_at, resolved_at)
		VALUES ($1, 'task', '{}'::jsonb, 'pending', $2, NULL)`,
		staleID, staleCreated); err != nil {
		t.Fatalf("seed stale TypeTask: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", staleID)
	})

	// Override the timeout so mark fits comfortably (200 ms is >>> mark
	// latency on a near-empty pg16 testcontainer) but the between-steps
	// sleep (300 ms) blows past the remaining deadline before DELETE runs.
	prevTimeout := pendingProposalsPruneTimeout
	pendingProposalsPruneTimeout = 200 * time.Millisecond
	t.Cleanup(func() { pendingProposalsPruneTimeout = prevTimeout })

	prevHook := pendingProposalsPruneBetweenSteps
	pendingProposalsPruneBetweenSteps = func(c context.Context) {
		// Sleep past the remaining deadline. We use the parent test
		// context's done channel as a safety bound so a future timeout
		// tweak can't hang the test forever.
		select {
		case <-time.After(300 * time.Millisecond):
		case <-c.Done():
		}
	}
	t.Cleanup(func() { pendingProposalsPruneBetweenSteps = prevHook })

	// Capture slog at warn level so we can assert the partial-success line.
	prevLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevLogger) })
	buf := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})))

	sc := &Scheduler{disciplinePool: pool}
	sc.runDailyPendingProposalsPrune()

	logOut := buf.String()
	if !strings.Contains(logOut, "mark succeeded but delete timed out") {
		t.Errorf("expected partial-success warn, got log:\n%s", logOut)
	}
	if !strings.Contains(logOut, "marked_rows=") {
		t.Errorf("expected marked_rows attribute in warn, got log:\n%s", logOut)
	}
	// Sanity-check we did NOT fall through to the generic "DELETE failed"
	// branch — that would indicate the partial-success condition wasn't
	// recognised (mark failed, markedRows=0, or err wasn't DeadlineExceeded).
	if strings.Contains(logOut, "DELETE failed") {
		t.Errorf("partial-success path should NOT emit generic DELETE-failed warn, got log:\n%s", logOut)
	}

	// And: the stale row MUST actually be marked rejected (proves the mark
	// step really did succeed before the ctx tripped — otherwise the warn
	// would be misleading).
	var status string
	var reason *string
	if err := pool.QueryRow(ctx,
		"SELECT status, reason FROM pending_proposals WHERE id = $1",
		staleID).Scan(&status, &reason); err != nil {
		t.Fatalf("query staleID: %v", err)
	}
	if status != rejectedStatus {
		t.Errorf("stale row: status = %q, want %q (mark step must have succeeded)", status, rejectedStatus)
	}
	if reason == nil || *reason != "ttl-expired-30d" {
		t.Errorf("stale row: reason = %v, want %q", reason, "ttl-expired-30d")
	}
}
