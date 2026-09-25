//go:build integration

package storage

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/validator"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // Postgres pgx/v5 driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// [F0925-33][F0925-34] Postgres coverage for migration 000084 — see
// migrations/000084_repo_name_cleanup.up.sql for the predicate and table
// list, and internal/storage/sqlite/repo_name_cleanup_migration_test.go for
// the SQLite twin these tests mirror. package storage (not storage_test):
// migrateToVersion below calls the unexported toPgx5DSN (migrate.go:126),
// same reason internal/storage/migrate_pem_test.go is package storage.

// testAdminDSN connects to the one Postgres database testcontainers creates
// at container start (used only to CREATE/DROP per-scenario databases via
// newIsolatedTestDB — never directly seeded or migrated).
var testAdminDSN string

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if testing.Short() {
		return m.Run()
	}
	ctx := context.Background()
	// pgvector/pgvector:pg16, not vanilla postgres:16-alpine: migrating to
	// version 83 replays migrations/000005_knowledge.up.sql's
	// `CREATE EXTENSION vector`, same reason internal/workspace/store_postgres_test.go uses this image.
	c, err := tcpostgres.Run(
		ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_test"),
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
	testAdminDSN = dsn
	return m.Run()
}

// isolatedDBCounter makes every newIsolatedTestDB database name unique even
// if two tests happen to run in the same nanosecond.
var isolatedDBCounter int64

// newIsolatedTestDB creates a fresh, empty database inside the shared
// container and returns its DSN. Each of the three test functions below
// gets its own database (dispatch record F0925-34: "每個情境各用一個獨立
// database") so one test's schema_migrations version / dirty flag can never
// leak into another's assertions.
func newIsolatedTestDB(t *testing.T, adminDSN string) string {
	t.Helper()
	ctx := context.Background()

	adminPool, err := pgxpool.New(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect admin pool: %v", err)
	}
	defer adminPool.Close()

	name := fmt.Sprintf("mig084_%d", atomic.AddInt64(&isolatedDBCounter, 1))
	if _, err := adminPool.Exec(ctx, `CREATE DATABASE `+name); err != nil { //nolint:gosec // name is program-generated (counter-based), never caller input
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		dropPool, dropErr := pgxpool.New(cleanupCtx, adminDSN)
		if dropErr != nil {
			t.Logf("cleanup: connect admin pool: %v", dropErr)
			return
		}
		defer dropPool.Close()
		if _, dropErr := dropPool.Exec(cleanupCtx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); dropErr != nil { //nolint:gosec // name is program-generated
			t.Logf("cleanup: drop database %s: %v", name, dropErr)
		}
	})

	u, err := url.Parse(adminDSN)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// migrateToVersion steps dsn to version directly via golang-migrate's iofs
// source, NOT via RunMigrations (which only ever calls m.Up() once and
// cannot stop short of the latest embedded migration — dispatch record
// F0925-34's "遷到 83" precondition). This is the ONLY place in this file
// that builds its own *migrate.Migrate; the "遷到 84" step in every test
// below calls RunMigrations itself instead — see that call site's comment.
func migrateToVersion(t *testing.T, dsn string, version uint) {
	t.Helper()
	src, err := iofs.New(migrationfs.FS, ".")
	if err != nil {
		t.Fatalf("load embedded migrations: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, toPgx5DSN(dsn))
	if err != nil {
		t.Fatalf("init migrate instance: %v", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			t.Logf("close migrate source: %v", srcErr)
		}
		if dbErr != nil {
			t.Logf("close migrate db: %v", dbErr)
		}
	}()
	if err := m.Migrate(version); err != nil {
		t.Fatalf("migrate to version %d: %v", version, err)
	}
}

// pgCleanupSpec describes how to seed one of the nine repo_name-carrying PG
// tables with a minimal valid row so only repo_name varies. Every insert
// binds exactly two params: $1=repo_name (a string sample, "" or nil for
// the NULL case), $2=marker (a uuid.New().String() written into markerCol —
// a free-text column with no uniqueness constraint of its own on every
// table except projects.name and completion_candidates.reason, which
// already need a unique value and so double as the marker). Any other
// NOT NULL / UNIQUE column the CREATE TABLE requires is filled server-side
// (gen_random_uuid()) or with a fixed literal.
type pgCleanupSpec struct {
	name      string
	notNull   bool // true: cleanup SETs '' (work_sessions, procedural_memories); false: SETs NULL
	markerCol string
	insert    string
}

var pgCleanupSpecs = []pgCleanupSpec{
	{
		name: "decisions", notNull: false, markerCol: "title",
		insert: `INSERT INTO decisions (repo_name, title, context, decision, rationale) VALUES ($1, $2, 'c', 'd', 'r')`,
	},
	{
		name: "session_handoffs", notNull: false, markerCol: "intent",
		insert: `INSERT INTO session_handoffs (repo_name, intent) VALUES ($1, $2)`,
	},
	{
		name: "guard_events", notNull: false, markerCol: "session_id",
		insert: `INSERT INTO guard_events (repo_name, session_id, tool_name, tool_input, risk_tier, would_deny, matcher) ` +
			`VALUES ($1, $2, 'tool', '{}'::jsonb, 0, false, 'm')`,
	},
	{
		name: "vision_items", notNull: false, markerCol: "title",
		insert: `INSERT INTO vision_items (repo_name, title, why_blocked) VALUES ($1, $2, 'w')`,
	},
	{
		name: "procedural_memories", notNull: true, markerCol: "title",
		insert: `INSERT INTO procedural_memories (repo_name, title) VALUES ($1, $2)`,
	},
	{
		name: "discipline_events", notNull: false, markerCol: "session_id",
		insert: `INSERT INTO discipline_events (repo_name, session_id, tool_name) VALUES ($1, $2, 'tool')`,
	},
	{
		name: "completion_candidates", notNull: false, markerCol: "reason",
		insert: `INSERT INTO completion_candidates (repo_name, task_id, reason, confidence) VALUES ($1, gen_random_uuid(), $2, 'high')`,
	},
	{
		name: "projects", notNull: false, markerCol: "name",
		insert: `INSERT INTO projects (repo_name, name, title) VALUES ($1, $2, 't')`,
	},
	{
		name: "work_sessions", notNull: true, markerCol: "title",
		insert: `INSERT INTO work_sessions (repo_name, workspace_id, title, goal, status, source) ` +
			`VALUES ($1, gen_random_uuid(), $2, 'g', 'planned', 'manual')`,
	},
}

// repoNameCleanupSamples mirrors internal/storage/sqlite/repo_name_cleanup_migration_test.go's
// list of the same name (a different package, so a separate copy — both
// ultimately trace to internal/validator/repo_name_test.go's unexported
// accept/reject fixtures). "repo\x00" is excluded for the same
// undocumented-for-Postgres reason as the SQLite twin's
// TestMigration000084_EmbeddedNULByteResidualRisk comment: not verified
// here, treated as the same accepted "sample doesn't cover a byte type"
// residual (dispatch record's acceptance self-check).
var repoNameCleanupSamples = []string{
	// accept
	"_project", "owner/repo", "a.b/c_d-e", "a", "A1", "1234567890", "my_repo.name", "trailing.dot.",
	"chatbot", "chatbot-go", "chat-gateway", "chat-web", "cmoney-backend", "demo",
	"example-app-go-pgx", "Flare-Go/auth", "food-photo-log", "go_web", "intelligence-flow",
	"kdc-p2p-report/kdc-p2p-server-status-check", "neomart-api",
	"skcloud-admin-portal/skcloud-advertisement-image", "skcloud-uid-udid-server/skcloud-uid-udid",
	"wayneblacktea",
	// reject
	"../etc/passwd", "a/../b", "./a", ".x", "_project/.claude", "-x", "a/-b", "--help",
	"a//b", "/a", "a/", "a b", "a;b", "a\nb", "repo\n", "repo\t",
	"repo$(cmd)", "repo`cmd`", "中文", "bad repo name!",
	// length boundary
	strings.Repeat("a", 100), strings.Repeat("a", 101),
}

// TestMigration000084_PG_Consistency seeds every sample above — plus an
// empty string and, for nullable columns, NULL — into all nine Postgres
// tables at schema version 83, calls RunMigrations (the production
// entrypoint, not a self-built *migrate.Migrate — grep -c 'RunMigrations(ctx'
// on this file is the dispatch record's positive control for that), and
// asserts the cleared/unchanged outcome for every row matches
// validator.IsValidRepoName.
func TestMigration000084_PG_Consistency(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := context.Background()
	dsn := newIsolatedTestDB(t, testAdminDSN)
	migrateToVersion(t, dsn, 83)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	type seeded struct {
		spec        pgCleanupSpec
		marker      string
		input       string
		isNullInput bool
	}
	var rows []seeded

	for _, spec := range pgCleanupSpecs {
		for _, sample := range repoNameCleanupSamples {
			marker := uuid.New().String()
			if _, execErr := pool.Exec(ctx, spec.insert, sample, marker); execErr != nil {
				t.Fatalf("seed %s marker=%s sample=%q: %v", spec.name, marker, sample, execErr)
			}
			rows = append(rows, seeded{spec: spec, marker: marker, input: sample})
		}

		emptyMarker := uuid.New().String()
		if _, execErr := pool.Exec(ctx, spec.insert, "", emptyMarker); execErr != nil {
			t.Fatalf("seed %s empty string: %v", spec.name, execErr)
		}
		rows = append(rows, seeded{spec: spec, marker: emptyMarker, input: ""})

		if !spec.notNull {
			nullMarker := uuid.New().String()
			if _, execErr := pool.Exec(ctx, spec.insert, nil, nullMarker); execErr != nil {
				t.Fatalf("seed %s NULL: %v", spec.name, execErr)
			}
			rows = append(rows, seeded{spec: spec, marker: nullMarker, isNullInput: true})
		}
	}

	// "遷到 84" MUST go through the production entrypoint itself, not a
	// second self-built *migrate.Migrate on the same driver (dispatch record
	// F0925-34). WBT_AUTO_MIGRATE must not be "false" or this becomes a
	// silent no-op.
	t.Setenv("WBT_AUTO_MIGRATE", "")
	if err := RunMigrations(ctx, dsn); err != nil {
		t.Fatalf("RunMigrations(ctx, dsn) to 84: %v", err)
	}

	for _, r := range rows {
		var got *string
		query := fmt.Sprintf(`SELECT repo_name FROM %s WHERE %s = $1`, r.spec.name, r.spec.markerCol) //nolint:gosec // table/column are hardcoded literals from pgCleanupSpecs, never caller input
		if err := pool.QueryRow(ctx, query, r.marker).Scan(&got); err != nil {
			t.Fatalf("read back %s marker=%s: %v", r.spec.name, r.marker, err)
		}

		if r.isNullInput {
			if got != nil {
				t.Errorf("%s marker=%s: NULL input became %q, want still NULL", r.spec.name, r.marker, *got)
			}
			continue
		}

		if validator.IsValidRepoName(r.input) {
			if got == nil || *got != r.input {
				t.Errorf("%s marker=%s: valid input %q was altered, got %v", r.spec.name, r.marker, r.input, got)
			}
			continue
		}

		// invalid and non-empty -> must be cleared.
		if r.spec.notNull {
			if got == nil || *got != "" {
				t.Errorf("%s marker=%s: invalid input %q not cleared to '', got %v", r.spec.name, r.marker, r.input, got)
			}
		} else if got != nil {
			t.Errorf("%s marker=%s: invalid input %q not cleared to NULL, got %q", r.spec.name, r.marker, r.input, *got)
		}
	}
}

// TestMigration000084_PG_TransactionalRollback pins the accepted failure
// mode (decision 3be90339): two in_progress work_sessions rows in the same
// workspace that both have an invalid repo_name collide on
// idx_work_sessions_one_active once both are cleared to ”, and
// RunMigrations MUST fail — not silently de-duplicate or leave one row
// unclean. work_sessions is deliberately the last UPDATE in
// migrations/000084_repo_name_cleanup.up.sql, so this also proves the
// whole-file BEGIN/COMMIT rolled back: an earlier UPDATE (decisions) in the
// same run must be undone too. Unlike the SQLite twin
// (TestMigration000084_WorkSessionsUniqueCollision), no explicit ROLLBACK
// is needed here to observe that: Postgres aborts the whole transaction on
// a mid-statement constraint violation (verified separately with a raw
// psql -f run against migrations/000084_repo_name_cleanup.up.sql — the
// earlier UPDATE was already undone with no explicit ROLLBACK issued), so a
// plain read after the failed RunMigrations call is sufficient.
func TestMigration000084_PG_TransactionalRollback(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := context.Background()
	dsn := newIsolatedTestDB(t, testAdminDSN)
	migrateToVersion(t, dsn, 83)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	decisionMarker := uuid.New().String()
	if _, execErr := pool.Exec(ctx,
		`INSERT INTO decisions (repo_name, title, context, decision, rationale) VALUES ($1, $2, 'c', 'd', 'r')`,
		"../bad", decisionMarker); execErr != nil {
		t.Fatalf("seed decisions: %v", execErr)
	}

	workspaceID := uuid.New()
	for i, badRepo := range []string{"../bad1", "../bad2"} {
		marker := uuid.New().String()
		if _, execErr := pool.Exec(ctx,
			`INSERT INTO work_sessions (repo_name, workspace_id, title, goal, status, source) VALUES ($1, $2, $3, 'g', 'in_progress', 'manual')`,
			badRepo, workspaceID, marker); execErr != nil {
			t.Fatalf("seed work_sessions row %d: %v", i, execErr)
		}
	}

	t.Setenv("WBT_AUTO_MIGRATE", "")
	err = RunMigrations(ctx, dsn)
	if err == nil {
		t.Fatal("expected RunMigrations to fail on the work_sessions UNIQUE collision, got nil error")
	}
	t.Logf("RunMigrations failed as expected: %v", err)

	var repoName *string
	if err := pool.QueryRow(ctx, `SELECT repo_name FROM decisions WHERE title = $1`, decisionMarker).Scan(&repoName); err != nil {
		t.Fatalf("read back decisions: %v", err)
	}
	if repoName == nil || *repoName != "../bad" {
		t.Errorf("decisions row was not rolled back: got %v, want unchanged \"../bad\"", repoName)
	}

	var version int
	var dirty bool
	if err := pool.QueryRow(ctx, `SELECT version, dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	t.Logf("post-failure schema_migrations = (version=%d, dirty=%v)", version, dirty)
	if version != 84 || !dirty {
		t.Errorf("schema_migrations = (version=%d, dirty=%v), want (84, true)", version, dirty)
	}
}

// TestMigration000084_PG_UpDownUp proves down (a documented no-op) and a
// second RunMigrations call don't error. down does NOT attempt to restore
// cleared values (decision 3be90339: no backup, not recoverable) — it only
// lets golang-migrate step schema_migrations back to 83 so RunMigrations
// can re-run 000084 against already-cleaned data (every UPDATE then
// matches zero rows).
func TestMigration000084_PG_UpDownUp(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := context.Background()
	dsn := newIsolatedTestDB(t, testAdminDSN)
	migrateToVersion(t, dsn, 83)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	marker := uuid.New().String()
	if _, execErr := pool.Exec(ctx,
		`INSERT INTO decisions (repo_name, title, context, decision, rationale) VALUES ($1, $2, 'c', 'd', 'r')`,
		"../bad", marker); execErr != nil {
		t.Fatalf("seed decisions: %v", execErr)
	}

	t.Setenv("WBT_AUTO_MIGRATE", "")
	if err := RunMigrations(ctx, dsn); err != nil {
		t.Fatalf("RunMigrations to 84 (first up): %v", err)
	}

	var repoName *string
	readBack := func() *string {
		var v *string
		if err := pool.QueryRow(ctx, `SELECT repo_name FROM decisions WHERE title = $1`, marker).Scan(&v); err != nil {
			t.Fatalf("read back decisions: %v", err)
		}
		return v
	}
	if repoName = readBack(); repoName != nil {
		t.Fatalf("precondition: expected repo_name cleared to NULL after first up, got %q", *repoName)
	}

	migrateToVersion(t, dsn, 83) // down: documented no-op
	if repoName = readBack(); repoName != nil {
		t.Errorf("down migration unexpectedly restored a value: %q (down.sql is a documented no-op)", *repoName)
	}

	if err := RunMigrations(ctx, dsn); err != nil {
		t.Fatalf("RunMigrations to 84 (second up): %v", err)
	}
}
