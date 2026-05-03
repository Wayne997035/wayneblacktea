package guard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// migrationsDir resolves to the repo's migrations/ directory.
//
// The test binary's CWD is the package directory (internal/guard),
// so we walk up two levels.
func migrationsDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	dir := filepath.Join(root, "migrations")
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("migrations dir %q: %v (info=%v)", dir, err, info)
	}
	return dir
}

// readMigration reads a single .up.sql or .down.sql file relative to migrationsDir.
func readMigration(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(migrationsDir(t), name)
	data, err := os.ReadFile(path) //nolint:gosec // path built from test-controlled migrations dir + constant filename
	if err != nil {
		t.Fatalf("read migration %s: %v", path, err)
	}
	return string(data)
}

// startPostgresContainer launches a throwaway Postgres container with
// BasicWaitStrategies so pgx connects only after the server is ready.
// Returns the connection DSN and registers cleanup on t.
func startPostgresContainer(t *testing.T, dbName string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: requires Docker (skipping under -short)")
	}
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase(dbName),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if cleanupErr := container.Terminate(ctx); cleanupErr != nil {
			t.Logf("container cleanup error: %v", cleanupErr)
		}
	})
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return dsn
}

// applyUpMigrations runs the on-disk 000024 + 000025 up SQL files against pool.
// It also creates the uuid-ossp extension because in production this is created
// by 000001_gtd which is not in scope for the guard integration test container.
func applyUpMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`); err != nil {
		t.Fatalf("create uuid-ossp: %v", err)
	}
	for _, file := range []string{
		"000024_guard_events.up.sql",
		"000025_guard_bypasses.up.sql",
	} {
		sql := readMigration(t, file)
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("apply %s: %v", file, err)
		}
	}
}

// applyDownMigrations runs the on-disk down SQL files in reverse order.
func applyDownMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, file := range []string{
		"000025_guard_bypasses.down.sql",
		"000024_guard_events.down.sql",
	} {
		sql := readMigration(t, file)
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("apply %s: %v", file, err)
		}
	}
}

// setupTestDB starts a Postgres container, applies real migration SQL files,
// and returns a Store backed by the resulting pool.
func setupTestDB(t *testing.T) *Store {
	t.Helper()
	dsn := startPostgresContainer(t, "testguard")

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	applyUpMigrations(t, ctx, pool)
	return NewStore(pool)
}

// TestIntegration_MigrationsAndWriteEvent tests the full guard_events write path
// against the real 000024 up SQL.
func TestIntegration_MigrationsAndWriteEvent(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	ev := Event{
		SessionID:  "session-abc",
		ToolName:   "Bash",
		ToolInput:  json.RawMessage(`{"command":"git push --force"}`),
		CWD:        "/home/user/myrepo",
		RepoName:   "myrepo",
		RiskTier:   T6,
		RiskReason: "git push --force",
		WouldDeny:  true,
		Matcher:    "bash",
	}

	if err := store.WriteEvent(ctx, ev); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}

	var count int
	err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM guard_events WHERE session_id = $1 AND tool_name = $2`,
		ev.SessionID, ev.ToolName,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("guard_events count = %d, want 1", count)
	}
}

// TestIntegration_BypassAddListRevoke tests the full bypass lifecycle.
func TestIntegration_BypassAddListRevoke(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	id, err := store.AddBypass(ctx, "repo", "myrepo", nil, "testing bypass", "testuser", nil)
	if err != nil {
		t.Fatalf("AddBypass: %v", err)
	}
	if id == (uuid.UUID{}) {
		t.Fatal("AddBypass returned zero UUID")
	}

	bypasses, err := store.ListBypasses(ctx, "")
	if err != nil {
		t.Fatalf("ListBypasses: %v", err)
	}
	found := false
	for _, b := range bypasses {
		if b.ID == id {
			found = true
			if b.Scope != "repo" {
				t.Errorf("bypass scope = %q, want repo", b.Scope)
			}
			if b.Target != "myrepo" {
				t.Errorf("bypass target = %q, want myrepo", b.Target)
			}
		}
	}
	if !found {
		t.Errorf("bypass %s not found in ListBypasses", id)
	}

	if err := store.RevokeBypass(ctx, id); err != nil {
		t.Fatalf("RevokeBypass: %v", err)
	}

	bypasses, err = store.ListBypasses(ctx, "")
	if err != nil {
		t.Fatalf("ListBypasses after revoke: %v", err)
	}
	for _, b := range bypasses {
		if b.ID == id {
			t.Errorf("bypass %s still present after revoke", id)
		}
	}
}

// TestIntegration_BypassReasonEmpty verifies empty reason is rejected.
func TestIntegration_BypassReasonEmpty(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	_, err := store.AddBypass(ctx, "repo", "myrepo", nil, "", "testuser", nil)
	if err == nil {
		t.Fatal("AddBypass with empty reason: expected error, got nil")
	}
}

// TestIntegration_BypassResolutionOrder verifies narrowest scope wins across
// the full file > dir > repo > global precedence chain.
func TestIntegration_BypassResolutionOrder(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	cwd := "/home/user/myrepo"
	dirTarget := "/home/user/myrepo/internal"
	filePath := "/home/user/myrepo/internal/foo.go"

	if _, err := store.AddBypass(ctx, "global", "global", nil, "global bypass", "testuser", nil); err != nil {
		t.Fatalf("AddBypass global: %v", err)
	}
	if _, err := store.AddBypass(ctx, "repo", "myrepo", nil, "repo bypass", "testuser", nil); err != nil {
		t.Fatalf("AddBypass repo: %v", err)
	}
	dirID, err := store.AddBypass(ctx, "dir", dirTarget, nil, "dir bypass", "testuser", nil)
	if err != nil {
		t.Fatalf("AddBypass dir: %v", err)
	}

	// Without a file bypass, dir scope must win over repo + global.
	b := ResolveBypass(ctx, store, cwd, filePath, "Edit")
	if b == nil {
		t.Fatal("ResolveBypass returned nil, want dir-level bypass")
	}
	if b.ID != dirID {
		t.Errorf("ResolveBypass (no file scope) returned %s, want dir bypass %s", b.ID, dirID)
	}

	// Adding a file bypass must take precedence over dir.
	fileID, err := store.AddBypass(ctx, "file", filePath, nil, "file bypass", "testuser", nil)
	if err != nil {
		t.Fatalf("AddBypass file: %v", err)
	}
	b = ResolveBypass(ctx, store, cwd, filePath, "Edit")
	if b == nil {
		t.Fatal("ResolveBypass returned nil, want file-level bypass")
	}
	if b.ID != fileID {
		t.Errorf("ResolveBypass (with file scope) returned %s, want file bypass %s", b.ID, fileID)
	}
}

// TestIntegration_BypassExpiry verifies expired bypasses are not returned.
func TestIntegration_BypassExpiry(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	past := time.Now().UTC().Add(-1 * time.Hour)
	_, err := store.AddBypass(ctx, "repo", "expiredrepo", nil, "expired bypass", "testuser", &past)
	if err != nil {
		t.Fatalf("AddBypass: %v", err)
	}

	bypasses, err := store.ListBypasses(ctx, "repo")
	if err != nil {
		t.Fatalf("ListBypasses: %v", err)
	}
	for _, b := range bypasses {
		if b.Target == "expiredrepo" {
			t.Errorf("expired bypass should not appear in list, got %+v", b)
		}
	}
}

// TestIntegration_NestedTaskInvocations verifies each Task invocation produces
// a separate guard_events row — the sub-agent recursion threat surface case.
func TestIntegration_NestedTaskInvocations(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	inputs := []struct {
		sessionID string
		input     json.RawMessage
	}{
		{
			sessionID: "outer-session",
			input:     json.RawMessage(`{"subagent_type":"engineer","prompt":"fix the tests"}`),
		},
		{
			sessionID: "inner-session",
			input:     json.RawMessage(`{"subagent_type":"codex:rescue","prompt":"fix the tests nested"}`),
		},
	}

	for _, inp := range inputs {
		result := Match("Task", inp.input, "/repo")
		ev := Event{
			SessionID:  inp.sessionID,
			ToolName:   "Task",
			ToolInput:  inp.input,
			CWD:        "/repo",
			RepoName:   "repo",
			RiskTier:   result.Tier,
			RiskReason: result.Reason,
			WouldDeny:  false,
			Matcher:    result.MatcherName,
		}
		if err := store.WriteEvent(ctx, ev); err != nil {
			t.Fatalf("WriteEvent for session %s: %v", inp.sessionID, err)
		}
	}

	var count int
	err := store.pool.QueryRow(ctx,
		`SELECT count(*) FROM guard_events WHERE tool_name = 'Task'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 2 {
		t.Errorf("guard_events Task count = %d, want 2 (separate rows per nested invocation)", count)
	}
}

// TestIntegration_E2EPipeBashClassification simulates an end-to-end pipe
// classification: classifier -> matcher -> store, the path wbt-guard takes.
func TestIntegration_E2EPipeBashClassification(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	// Pipeline with a destructive segment in the middle — the highest-risk tier
	// MUST surface even though the first command is read-only.
	cmd := `git status && rm -rf ./tmp && go test ./...`
	tier, reason := ClassifyBash(cmd)
	if tier != T5 {
		t.Fatalf("ClassifyBash(%q) = T%d, want T5", cmd, tier)
	}

	result := Match("Bash", json.RawMessage(`{"command":"`+cmd+`"}`), "/repo")
	if result.Tier != T5 {
		t.Fatalf("Match(Bash) tier = T%d, want T5", result.Tier)
	}

	ev := Event{
		SessionID:  "e2e-session",
		ToolName:   "Bash",
		ToolInput:  json.RawMessage(`{"command":"` + cmd + `"}`),
		CWD:        "/repo",
		RepoName:   "repo",
		RiskTier:   result.Tier,
		RiskReason: result.Reason,
		WouldDeny:  result.Tier >= T5,
		Matcher:    result.MatcherName,
	}
	if err := store.WriteEvent(ctx, ev); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}

	// Verify the stored row reflects the highest-tier segment, not the first.
	var (
		gotTier   int16
		gotReason string
		gotDeny   bool
	)
	err := store.pool.QueryRow(ctx,
		`SELECT risk_tier, risk_reason, would_deny FROM guard_events WHERE session_id = $1`,
		ev.SessionID,
	).Scan(&gotTier, &gotReason, &gotDeny)
	if err != nil {
		t.Fatalf("read back guard_events: %v", err)
	}
	// risk_tier is SMALLINT (int16) in Postgres but the Go type is int8.
	// All defined tiers are 0–7 so the truncation is safe; tag the conversion.
	//nolint:gosec // G115: tier values are 0..7 by construction (T0..T7), int16→int8 is lossless.
	if RiskTier(int8(gotTier)) != T5 {
		t.Errorf("stored risk_tier = %d, want T5(%d)", gotTier, T5)
	}
	if !gotDeny {
		t.Error("stored would_deny = false, want true (T5 with no bypass)")
	}
	if !strings.Contains(gotReason, "deletion") && !strings.Contains(gotReason, "destructive") {
		t.Errorf("stored risk_reason = %q, want it to mention deletion/destructive cause", gotReason)
	}
	_ = reason
}

// TestIntegration_MigrationsDownClean verifies the down migration SQL files
// remove every artifact created by the up migrations.
func TestIntegration_MigrationsDownClean(t *testing.T) {
	dsn := startPostgresContainer(t, "testdown")

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	applyUpMigrations(t, ctx, pool)
	applyDownMigrations(t, ctx, pool)

	for _, table := range []string{"guard_events", "guard_bypasses"} {
		var exists bool
		err = pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
			table,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("table existence check %s: %v", table, err)
		}
		if exists {
			t.Errorf("table %s still exists after down migration", table)
		}
	}
}

// TestIntegration_BypassScopeCheckConstraint verifies the CHECK constraint on
// guard_bypasses.scope rejects unsupported scope values.
func TestIntegration_BypassScopeCheckConstraint(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	_, err := store.AddBypass(ctx, "team", "myteam", nil, "should fail", "testuser", nil)
	if err == nil {
		t.Fatal("AddBypass with scope=team: expected CHECK constraint violation, got nil")
	}
}

// TestIntegration_FailOpenOnPoolClosed verifies WriteEvent returns nil even
// when the underlying pool has been closed (DB lost mid-flight).
func TestIntegration_FailOpenOnPoolClosed(t *testing.T) {
	store := setupTestDB(t)
	store.pool.Close()

	err := store.WriteEvent(context.Background(), Event{
		SessionID: "after-close",
		ToolName:  "Bash",
		ToolInput: json.RawMessage(`{"command":"ls"}`),
		RiskTier:  T0,
		Matcher:   "bash",
	})
	if err != nil {
		t.Errorf("WriteEvent on closed pool = %v, want nil (fail-open)", err)
	}
}

// ensureMigrationFilesExist guards against the test running against stale fixtures.
// If the SQL files vanish, the integration tests must fail loudly, not silently.
func TestIntegration_MigrationFilesExist(t *testing.T) {
	for _, f := range []string{
		"000024_guard_events.up.sql",
		"000024_guard_events.down.sql",
		"000025_guard_bypasses.up.sql",
		"000025_guard_bypasses.down.sql",
	} {
		path := filepath.Join(migrationsDir(t), f)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("missing migration file %s: %v", path, err)
		}
	}
	// Sanity: each file is non-empty.
	for _, f := range []string{
		"000024_guard_events.up.sql",
		"000025_guard_bypasses.up.sql",
	} {
		if data := readMigration(t, f); !strings.Contains(data, "CREATE TABLE") {
			t.Errorf("migration %s does not contain CREATE TABLE", f)
		}
	}
}
