//go:build integration

package discipline_test

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// skipMigrations are migration files that cannot be run by pgx because they
// contain psql metacommands. Mirrors the atom-store integration test pattern.
var skipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true,
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
		if skipMigrations[name] {
			log.Printf("skipping %s (psql metacommands)", name)
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

func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return testPgPool
}

// disciplineOutcomeRow is the raw ok/error_class/response_bytes/duration_ms
// projection queried directly from discipline_events, bypassing the Store
// interface (which does not surface these four columns on Event — see
// spec 1f4c7b7f's Out-of-scope note). Used to prove the columns landed
// exactly as passed to Insert.
type disciplineOutcomeRow struct {
	Ok            bool
	ErrorClass    sql.NullString
	ResponseBytes sql.NullInt64
	DurationMs    sql.NullInt64
}

// queryDisciplineOutcome reads back the four F184-04 columns for the most
// recently inserted row matching (sessionID, toolName). Reverting
// PgStore.Insert's column list back to the pre-ticket 6 columns
// (internal/discipline/store_pg.go) makes this Scan fail outright (column
// count / unknown column).
func queryDisciplineOutcome(t *testing.T, pool *pgxpool.Pool, sessionID, toolName string) disciplineOutcomeRow {
	t.Helper()
	row := pool.QueryRow(context.Background(),
		`SELECT ok, error_class, response_bytes, duration_ms FROM discipline_events
		 WHERE session_id = $1 AND tool_name = $2 ORDER BY id DESC LIMIT 1`,
		sessionID, toolName)
	var out disciplineOutcomeRow
	if err := row.Scan(&out.Ok, &out.ErrorClass, &out.ResponseBytes, &out.DurationMs); err != nil {
		t.Fatalf("query discipline outcome columns for %s/%s: %v", sessionID, toolName, err)
	}
	return out
}

func TestPgStore_InsertAndRecentMutating(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := discipline.NewPgStore(pool, &wsID)
	ctx := context.Background()
	someBytes := 512

	tests := []struct {
		name   string
		params discipline.InsertParams
	}{
		{
			name: "mutating with all fields",
			params: discipline.InsertParams{
				SessionID:     "mcp-1-1000",
				RepoName:      "wayneblacktea",
				ToolName:      "log_decision",
				IsMutating:    true,
				Ok:            true,
				ResponseBytes: &someBytes,
				DurationMs:    17,
			},
		},
		{
			name: "read-only minimal",
			params: discipline.InsertParams{
				SessionID:  "mcp-1-1001",
				ToolName:   "list_tasks",
				IsMutating: false,
				Ok:         true,
			},
		},
		{
			name: "mutating with linked decision",
			params: discipline.InsertParams{
				SessionID:        "mcp-1-1002",
				ToolName:         "complete_task",
				IsMutating:       true,
				Ok:               true,
				LinkedDecisionID: ptrUUID(uuid.New()),
			},
		},
		{
			// [F184-05] failed call: ok=false, error_class="internal".
			name: "failed mutating call records ok=false",
			params: discipline.InsertParams{
				SessionID:     "mcp-1-1003",
				ToolName:      "create_project",
				IsMutating:    true,
				Ok:            false,
				ErrorClass:    discipline.ErrorClassInternal,
				ResponseBytes: &someBytes,
				DurationMs:    9,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.Insert(ctx, tc.params); err != nil {
				t.Fatalf("Insert: %v", err)
			}

			// [F184-04] Verify the four new columns landed exactly as
			// passed. See queryDisciplineOutcome's doc for the mutation
			// this guards against.
			got := queryDisciplineOutcome(t, pool, tc.params.SessionID, tc.params.ToolName)
			if got.Ok != tc.params.Ok {
				t.Errorf("ok: want %v, got %v", tc.params.Ok, got.Ok)
			}
			wantErrClass := string(tc.params.ErrorClass)
			gotErrClass := ""
			if got.ErrorClass.Valid {
				gotErrClass = got.ErrorClass.String
			}
			if gotErrClass != wantErrClass {
				t.Errorf("error_class: want %q, got %q", wantErrClass, gotErrClass)
			}
			if tc.params.ResponseBytes == nil {
				if got.ResponseBytes.Valid {
					t.Errorf("response_bytes: want NULL, got %d", got.ResponseBytes.Int64)
				}
			} else if !got.ResponseBytes.Valid || got.ResponseBytes.Int64 != int64(*tc.params.ResponseBytes) {
				t.Errorf("response_bytes: want %d, got %+v", *tc.params.ResponseBytes, got.ResponseBytes)
			}
			if !got.DurationMs.Valid || got.DurationMs.Int64 != int64(tc.params.DurationMs) {
				t.Errorf("duration_ms: want %d, got %+v", tc.params.DurationMs, got.DurationMs)
			}
		})
	}

	// Four rows persisted; mutating+ok filter pulls only the two
	// successful mutating ones, scoped to the test workspace (the failed
	// mutating call is excluded).
	got, err := store.RecentMutating(ctx, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("RecentMutating: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 mutating events, got %d (%+v)", len(got), got)
	}
	for _, ev := range got {
		if !ev.IsMutating {
			t.Errorf("non-mutating leaked: %+v", ev)
		}
		if ev.WorkspaceID == nil || *ev.WorkspaceID != wsID {
			t.Errorf("workspace_id mismatch: got %v want %v", ev.WorkspaceID, wsID)
		}
		if ev.SessionID == "mcp-1-1003" {
			t.Errorf("failed mutating call leaked into RecentMutating: %+v", ev)
		}
	}
}

func TestPgStore_RecentDecisionTimes(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := discipline.NewPgStore(pool, &wsID)
	ctx := context.Background()

	for _, p := range []discipline.InsertParams{
		{SessionID: "alpha", ToolName: "log_decision", IsMutating: true, Ok: true},
		{SessionID: "alpha", ToolName: "confirm_plan", IsMutating: true, Ok: true},
		{SessionID: "alpha", ToolName: "add_task", IsMutating: true, Ok: true},
		{SessionID: "beta", ToolName: "log_decision", IsMutating: true, Ok: true},
		// [F184-05] a failed log_decision must not suppress a real drift
		// signal — Acceptance criteria row 6.
		// Verified: dropping `AND ok = TRUE` from RecentDecisionTimes'
		// WHERE clause (internal/discipline/store_pg.go) makes both
		// subtests below fail (3 events instead of 2) — see the implement
		// record's "突變證明" section for the actual FAIL output.
		{SessionID: "alpha", ToolName: "log_decision", IsMutating: true, Ok: false, ErrorClass: discipline.ErrorClassInternal},
	} {
		if err := store.Insert(ctx, p); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	t.Run("alpha gets two decision rows", func(t *testing.T) {
		got, err := store.RecentDecisionTimes(ctx, "alpha", time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("RecentDecisionTimes: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("alpha decisions: want 2, got %d", len(got))
		}
	})

	t.Run("alpha excludes failed decision calls", func(t *testing.T) {
		got, err := store.RecentDecisionTimes(ctx, "alpha", time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("RecentDecisionTimes: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("failed log_decision leaked into RecentDecisionTimes: want 2, got %d", len(got))
		}
	})

	t.Run("beta gets one", func(t *testing.T) {
		got, err := store.RecentDecisionTimes(ctx, "beta", time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("RecentDecisionTimes: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("beta decisions: want 1, got %d", len(got))
		}
	})

	t.Run("non-existent session returns zero", func(t *testing.T) {
		got, err := store.RecentDecisionTimes(ctx, "gamma", time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("RecentDecisionTimes: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("gamma decisions: want 0, got %d", len(got))
		}
	})
}

func TestPgStore_WorkspaceScoping(t *testing.T) {
	pool := openTestPgPool(t)
	wsA := uuid.New()
	wsB := uuid.New()
	storeA := discipline.NewPgStore(pool, &wsA)
	storeB := discipline.NewPgStore(pool, &wsB)
	ctx := context.Background()

	// One mutating event into each workspace.
	if err := storeA.Insert(ctx, discipline.InsertParams{
		SessionID:  "sA",
		ToolName:   "add_task",
		IsMutating: true,
		Ok:         true,
	}); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	if err := storeB.Insert(ctx, discipline.InsertParams{
		SessionID:  "sB",
		ToolName:   "add_task",
		IsMutating: true,
		Ok:         true,
	}); err != nil {
		t.Fatalf("insert B: %v", err)
	}

	gotA, err := storeA.RecentMutating(ctx, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("storeA RecentMutating: %v", err)
	}
	if len(gotA) != 1 {
		t.Fatalf("storeA: want 1 event, got %d", len(gotA))
	}
	if gotA[0].SessionID != "sA" {
		t.Errorf("storeA leaked from B: %s", gotA[0].SessionID)
	}

	gotB, err := storeB.RecentMutating(ctx, time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("storeB RecentMutating: %v", err)
	}
	if len(gotB) != 1 {
		t.Fatalf("storeB: want 1 event, got %d", len(gotB))
	}
	if gotB[0].SessionID != "sB" {
		t.Errorf("storeB leaked from A: %s", gotB[0].SessionID)
	}
}

// TestPgStore_StrictWorkspaceScoping verifies the disjoint scoping
// introduced in PR #87 R2 (M2): a scoped store sees only its own
// workspace_id rows and an unscoped store sees only NULL workspace_id rows.
// Prevents legacy NULL-workspace data or other-workspace data leaking into
// a workspace-scoped read in PG mode (where DATABASE_URL + missing
// WORKSPACE_ID would otherwise be a cross-tenant leak).
func TestPgStore_StrictWorkspaceScoping(t *testing.T) {
	pool := openTestPgPool(t)
	wsA := uuid.New()
	wsB := uuid.New()
	ctx := context.Background()

	storeA := discipline.NewPgStore(pool, &wsA)
	storeB := discipline.NewPgStore(pool, &wsB)
	storeUnscoped := discipline.NewPgStore(pool, nil)

	// Seed three rows: A, B, NULL.
	if err := storeA.Insert(ctx, discipline.InsertParams{
		SessionID:  "ws-scope-A",
		ToolName:   "add_task",
		IsMutating: true,
		Ok:         true,
	}); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	if err := storeB.Insert(ctx, discipline.InsertParams{
		SessionID:  "ws-scope-B",
		ToolName:   "complete_task",
		IsMutating: true,
		Ok:         true,
	}); err != nil {
		t.Fatalf("insert B: %v", err)
	}
	if err := storeUnscoped.Insert(ctx, discipline.InsertParams{
		SessionID:  "ws-scope-NULL",
		ToolName:   "log_decision",
		IsMutating: true,
		Ok:         true,
	}); err != nil {
		t.Fatalf("insert NULL: %v", err)
	}

	t.Run("scoped store reads only its own workspace", func(t *testing.T) {
		got, err := storeA.RecentMutating(ctx, time.Now().Add(-time.Hour), 100)
		if err != nil {
			t.Fatalf("storeA RecentMutating: %v", err)
		}
		// Filter to the rows we just seeded (the test PG pool may have
		// other rows from earlier subtests in the same package run).
		var sessions []string
		for _, ev := range got {
			sessions = append(sessions, ev.SessionID)
		}
		if !containsExactly(sessions, "ws-scope-A") {
			t.Errorf("storeA: expected only ws-scope-A in seeded set, got %+v", sessions)
		}
	})

	t.Run("scoped store does NOT see legacy NULL workspace rows", func(t *testing.T) {
		got, err := storeA.RecentMutating(ctx, time.Now().Add(-time.Hour), 100)
		if err != nil {
			t.Fatalf("storeA RecentMutating: %v", err)
		}
		for _, ev := range got {
			if ev.SessionID == "ws-scope-NULL" {
				t.Errorf("storeA leaked NULL row: %+v", ev)
			}
			if ev.SessionID == "ws-scope-B" {
				t.Errorf("storeA leaked B row: %+v", ev)
			}
		}
	})

	t.Run("unscoped store sees only NULL workspace rows", func(t *testing.T) {
		got, err := storeUnscoped.RecentMutating(ctx, time.Now().Add(-time.Hour), 100)
		if err != nil {
			t.Fatalf("unscoped RecentMutating: %v", err)
		}
		for _, ev := range got {
			if ev.SessionID == "ws-scope-A" || ev.SessionID == "ws-scope-B" {
				t.Errorf("unscoped store leaked scoped row: %+v", ev)
			}
		}
		// And the NULL row IS visible.
		if !containsSession(got, "ws-scope-NULL") {
			t.Errorf("unscoped store missing NULL row; got %d events", len(got))
		}
	})

	t.Run("RecentDecisionTimes also enforces strict scoping", func(t *testing.T) {
		// Seed log_decision with the same session in both A and B; A's
		// scoped read should see only one timestamp, not both.
		if err := storeA.Insert(ctx, discipline.InsertParams{
			SessionID:  "shared-decisions",
			ToolName:   "log_decision",
			IsMutating: true,
			Ok:         true,
		}); err != nil {
			t.Fatalf("insert decision A: %v", err)
		}
		if err := storeB.Insert(ctx, discipline.InsertParams{
			SessionID:  "shared-decisions",
			ToolName:   "confirm_plan",
			IsMutating: true,
			Ok:         true,
		}); err != nil {
			t.Fatalf("insert decision B: %v", err)
		}

		got, err := storeA.RecentDecisionTimes(ctx, "shared-decisions", time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatalf("RecentDecisionTimes: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("RecentDecisionTimes: want 1 (workspace A only), got %d", len(got))
		}
	})
}

// containsExactly returns true if the only sessions present in the slice
// (intersected with the seeded set) is the expected one. Tolerant of
// pollution from other test rows in the shared PG pool.
func containsExactly(got []string, want string) bool {
	seeded := map[string]bool{
		"ws-scope-A": true, "ws-scope-B": true, "ws-scope-NULL": true,
	}
	hits := 0
	for _, s := range got {
		if !seeded[s] {
			continue
		}
		if s != want {
			return false
		}
		hits++
	}
	return hits == 1
}

func containsSession(got []discipline.Event, want string) bool {
	for _, ev := range got {
		if ev.SessionID == want {
			return true
		}
	}
	return false
}

func ptrUUID(id uuid.UUID) *uuid.UUID { return &id }
