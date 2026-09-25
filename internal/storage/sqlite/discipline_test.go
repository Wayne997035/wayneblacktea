package sqlite_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// openDisciplineDB opens an in-memory SQLite DB scoped to no workspace.
// Used by every test in this file so each test gets a fresh schema.
func openDisciplineDB(t *testing.T) *wbtsqlite.DB {
	t.Helper()
	return openDisciplineDBWS(t, "")
}

// openDisciplineDBWS opens an in-memory SQLite DB scoped to the given
// workspace string. Empty string = unscoped (legacy single-tenant) mode.
// Used to verify strict workspace scoping in RecentMutating /
// RecentDecisionTimes.
func openDisciplineDBWS(t *testing.T, workspaceID string) *wbtsqlite.DB {
	t.Helper()
	db, err := wbtsqlite.OpenTemplated(t, context.Background(), ":memory:", workspaceID) // [F0925-09] semantics-preserving template helper
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
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
// DisciplineStore.Insert's column list back to the pre-ticket 7 columns
// (internal/storage/sqlite/discipline.go) makes this Scan fail outright
// (column count / unknown column), which is why every caller below treats a
// Scan error as fatal rather than tolerating it.
func queryDisciplineOutcome(t *testing.T, db *wbtsqlite.DB, sessionID, toolName string) disciplineOutcomeRow {
	t.Helper()
	row := db.QueryRowContext(context.Background(),
		`SELECT ok, error_class, response_bytes, duration_ms FROM discipline_events
		 WHERE session_id = ? AND tool_name = ? ORDER BY id DESC LIMIT 1`,
		sessionID, toolName)
	var (
		okInt int64
		out   disciplineOutcomeRow
	)
	if err := row.Scan(&okInt, &out.ErrorClass, &out.ResponseBytes, &out.DurationMs); err != nil {
		t.Fatalf("query discipline outcome columns for %s/%s: %v", sessionID, toolName, err)
	}
	out.Ok = okInt != 0
	return out
}

func TestSQLiteDisciplineStore_Insert(t *testing.T) {
	t.Parallel() // [F0925-10]
	ctx := context.Background()
	wsID := uuid.New()
	linkedID := uuid.New()
	someBytes := 256

	tests := []struct {
		name        string
		dbWorkspace string // workspace scope of the DB (matches InsertParams' workspace when set)
		params      discipline.InsertParams
	}{
		{
			name:        "happy path mutating event with all fields",
			dbWorkspace: wsID.String(),
			params: discipline.InsertParams{
				SessionID:        "mcp-1234-5678",
				RepoName:         "wayneblacktea",
				ToolName:         "log_decision",
				IsMutating:       true,
				LinkedDecisionID: &linkedID,
				WorkspaceID:      &wsID,
				Ok:               true,
				ResponseBytes:    &someBytes,
				DurationMs:       42,
			},
		},
		{
			name: "minimal required fields, read-only",
			params: discipline.InsertParams{
				SessionID:  "mcp-9999-0",
				ToolName:   "list_tasks",
				IsMutating: false,
				Ok:         true,
			},
		},
		{
			name: "empty repo_name persists as NULL",
			params: discipline.InsertParams{
				SessionID:  "mcp-1234-5678",
				ToolName:   "add_task",
				IsMutating: true,
				Ok:         true,
			},
		},
		{
			// [F184-05] failed call: ok=false, error_class="internal".
			// response_bytes/duration_ms are still measured — mirrors
			// disciplineMiddleware's tool-level-failure path (res != nil,
			// res.IsError == true).
			name: "failed call records ok=false and error_class",
			params: discipline.InsertParams{
				SessionID:     "mcp-fail-0001",
				ToolName:      "create_project",
				IsMutating:    true,
				Ok:            false,
				ErrorClass:    discipline.ErrorClassInternal,
				ResponseBytes: &someBytes,
				DurationMs:    7,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := openDisciplineDBWS(t, tc.dbWorkspace)
			store := wbtsqlite.NewDisciplineStore(db)

			if err := store.Insert(ctx, tc.params); err != nil {
				t.Fatalf("Insert: %v", err)
			}

			// [F184-04] Verify the four new columns landed exactly as
			// passed. See queryDisciplineOutcome's doc for the mutation
			// this guards against.
			assertDisciplineOutcomeColumns(t, db, tc.params)

			// Read back via RecentMutating / store query: only mutating AND
			// ok events appear there, so this also exercises both filters.
			assertRecentMutatingReflectsInsert(t, ctx, store, tc.params)
		})
	}
}

// assertDisciplineOutcomeColumns verifies the four F184-04 columns
// (ok/error_class/response_bytes/duration_ms) landed exactly as passed to
// Insert. Extracted out of TestSQLiteDisciplineStore_Insert to keep that
// test's cyclomatic complexity under golangci-lint's gocyclo threshold —
// behavior is unchanged, only the code shape.
func assertDisciplineOutcomeColumns(t *testing.T, db *wbtsqlite.DB, params discipline.InsertParams) {
	t.Helper()
	got := queryDisciplineOutcome(t, db, params.SessionID, params.ToolName)
	if got.Ok != params.Ok {
		t.Errorf("ok: want %v, got %v", params.Ok, got.Ok)
	}
	wantErrClass := string(params.ErrorClass)
	gotErrClass := ""
	if got.ErrorClass.Valid {
		gotErrClass = got.ErrorClass.String
	}
	if gotErrClass != wantErrClass {
		t.Errorf("error_class: want %q, got %q", wantErrClass, gotErrClass)
	}
	if params.ResponseBytes == nil {
		if got.ResponseBytes.Valid {
			t.Errorf("response_bytes: want NULL, got %d", got.ResponseBytes.Int64)
		}
	} else if !got.ResponseBytes.Valid || got.ResponseBytes.Int64 != int64(*params.ResponseBytes) {
		t.Errorf("response_bytes: want %d, got %+v", *params.ResponseBytes, got.ResponseBytes)
	}
	if !got.DurationMs.Valid || got.DurationMs.Int64 != int64(params.DurationMs) {
		t.Errorf("duration_ms: want %d, got %+v", params.DurationMs, got.DurationMs)
	}
}

// assertRecentMutatingReflectsInsert verifies RecentMutating includes the
// just-inserted row iff it was both mutating and ok, and that its fields
// match when it does (or that it's excluded entirely otherwise). Extracted
// out of TestSQLiteDisciplineStore_Insert for the same gocyclo reason as
// assertDisciplineOutcomeColumns.
func assertRecentMutatingReflectsInsert(
	t *testing.T, ctx context.Context, store *wbtsqlite.DisciplineStore, params discipline.InsertParams,
) {
	t.Helper()
	events, err := store.RecentMutating(ctx, time.Now().Add(-time.Minute), 100)
	if err != nil {
		t.Fatalf("RecentMutating: %v", err)
	}
	if params.IsMutating && params.Ok {
		if len(events) != 1 {
			t.Fatalf("expected 1 mutating+ok event, got %d", len(events))
		}
		ev := events[0]
		if ev.SessionID != params.SessionID {
			t.Errorf("session_id: want %q, got %q", params.SessionID, ev.SessionID)
		}
		if ev.ToolName != params.ToolName {
			t.Errorf("tool_name: want %q, got %q", params.ToolName, ev.ToolName)
		}
		if !ev.IsMutating {
			t.Errorf("is_mutating: want true, got false")
		}
		if ev.RepoName != params.RepoName {
			t.Errorf("repo_name: want %q, got %q", params.RepoName, ev.RepoName)
		}
	} else if len(events) != 0 {
		t.Fatalf("expected 0 mutating+ok events (IsMutating=%v Ok=%v), got %d", params.IsMutating, params.Ok, len(events))
	}
}

func TestSQLiteDisciplineStore_RecentMutating(t *testing.T) {
	t.Parallel() // [F0925-10]
	ctx := context.Background()
	db := openDisciplineDB(t)
	store := wbtsqlite.NewDisciplineStore(db)

	// Seed mix of mutating/read-only/failed events.
	for _, p := range []discipline.InsertParams{
		{SessionID: "s1", ToolName: "add_task", IsMutating: true, Ok: true},
		{SessionID: "s1", ToolName: "list_tasks", IsMutating: false, Ok: true},
		{SessionID: "s2", ToolName: "complete_task", IsMutating: true, Ok: true},
		// [F184-05] a failed mutating call must NOT count as drift —
		// Acceptance criteria row 5. Verified: dropping
		// `AND ok = 1` from RecentMutating's WHERE clause
		// (internal/storage/sqlite/discipline.go) makes both subtests
		// below fail (3 events instead of 2, and s3 leaking through) — see
		// the "突變證明" section of the implement record for the actual
		// FAIL output from running that mutation.
		{SessionID: "s3", ToolName: "delete_task", IsMutating: true, Ok: false, ErrorClass: discipline.ErrorClassInternal},
	} {
		if err := store.Insert(ctx, p); err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}

	t.Run("filters to mutating only", func(t *testing.T) {
		got, err := store.RecentMutating(ctx, time.Now().Add(-time.Minute), 100)
		if err != nil {
			t.Fatalf("RecentMutating: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("expected 2 mutating events, got %d", len(got))
		}
		for _, ev := range got {
			if !ev.IsMutating {
				t.Errorf("non-mutating event leaked: %+v", ev)
			}
		}
	})

	t.Run("excludes failed mutating events", func(t *testing.T) {
		got, err := store.RecentMutating(ctx, time.Now().Add(-time.Minute), 100)
		if err != nil {
			t.Fatalf("RecentMutating: %v", err)
		}
		for _, ev := range got {
			if ev.SessionID == "s3" {
				t.Errorf("failed mutating event leaked into RecentMutating: %+v", ev)
			}
		}
	})

	t.Run("respects since cutoff", func(t *testing.T) {
		future := time.Now().Add(1 * time.Hour)
		got, err := store.RecentMutating(ctx, future, 100)
		if err != nil {
			t.Fatalf("RecentMutating: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 events past future cutoff, got %d", len(got))
		}
	})

	t.Run("respects limit", func(t *testing.T) {
		got, err := store.RecentMutating(ctx, time.Now().Add(-time.Minute), 1)
		if err != nil {
			t.Fatalf("RecentMutating: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("expected 1 event with limit=1, got %d", len(got))
		}
	})
}

func TestSQLiteDisciplineStore_RecentDecisionTimes(t *testing.T) {
	t.Parallel() // [F0925-10]
	ctx := context.Background()
	db := openDisciplineDB(t)
	store := wbtsqlite.NewDisciplineStore(db)

	for _, p := range []discipline.InsertParams{
		{SessionID: "s1", ToolName: "log_decision", IsMutating: true, Ok: true},
		{SessionID: "s1", ToolName: "confirm_plan", IsMutating: true, Ok: true},
		{SessionID: "s1", ToolName: "add_task", IsMutating: true, Ok: true}, // not a decision tool
		{SessionID: "s2", ToolName: "log_decision", IsMutating: true, Ok: true},
		// [F184-05] a failed log_decision must not suppress a real drift
		// signal — Acceptance criteria row 6.
		// Verified: dropping `AND ok = 1` from RecentDecisionTimes' WHERE
		// clause makes both subtests below fail (3 events instead of 2 for
		// s1) — see the implement record's "突變證明" section for the
		// actual FAIL output from running that mutation.
		{SessionID: "s1", ToolName: "log_decision", IsMutating: true, Ok: false, ErrorClass: discipline.ErrorClassInternal},
	} {
		if err := store.Insert(ctx, p); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	t.Run("scoped to session and decision tools", func(t *testing.T) {
		got, err := store.RecentDecisionTimes(ctx, "s1", time.Now().Add(-time.Minute))
		if err != nil {
			t.Fatalf("RecentDecisionTimes: %v", err)
		}
		// Only log_decision + confirm_plan rows under s1 with ok=true: 2
		// expected (add_task is mutating but NOT a decision tool; the
		// failed log_decision under s1 is excluded by the ok filter).
		if len(got) != 2 {
			t.Errorf("expected 2 decisions, got %d", len(got))
		}
	})

	t.Run("excludes failed decision calls", func(t *testing.T) {
		got, err := store.RecentDecisionTimes(ctx, "s1", time.Now().Add(-time.Minute))
		if err != nil {
			t.Fatalf("RecentDecisionTimes: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("failed log_decision leaked into RecentDecisionTimes: expected 2, got %d", len(got))
		}
	})

	t.Run("unknown session returns empty", func(t *testing.T) {
		got, err := store.RecentDecisionTimes(ctx, "no-such-session", time.Now().Add(-time.Minute))
		if err != nil {
			t.Fatalf("RecentDecisionTimes: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 decisions, got %d", len(got))
		}
	})

	t.Run("since filter excludes older rows", func(t *testing.T) {
		got, err := store.RecentDecisionTimes(ctx, "s1", time.Now().Add(1*time.Hour))
		if err != nil {
			t.Fatalf("RecentDecisionTimes: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 decisions past future cutoff, got %d", len(got))
		}
	})
}

// Strict-workspace-scoping suite (PR #87 R2 / M2): split into 4 top-level
// tests rather than a single t.Run-heavy func to keep gocyclo happy and
// give a clearer failure mode per scenario.

// TestSQLiteDisciplineStore_StrictScoping_ScopedReadsOnlyOwnWorkspace:
// scoped store sees only its own workspace_id rows and never the other
// workspace's, even when both rows are written through the same store.
func TestSQLiteDisciplineStore_StrictScoping_ScopedReadsOnlyOwnWorkspace(t *testing.T) {
	t.Parallel() // [F0925-10]
	ctx := context.Background()
	wsA := uuid.New()
	wsB := uuid.New()

	db := openDisciplineDBWS(t, wsA.String())
	store := wbtsqlite.NewDisciplineStore(db)

	// Row in workspace A (via store scope fall-through).
	if err := store.Insert(ctx, discipline.InsertParams{
		SessionID:  "sA",
		ToolName:   "add_task",
		IsMutating: true,
		Ok:         true,
	}); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	// Row in workspace B (explicit override on a store scoped to A).
	if err := store.Insert(ctx, discipline.InsertParams{
		SessionID:   "sB",
		ToolName:    "complete_task",
		IsMutating:  true,
		Ok:          true,
		WorkspaceID: &wsB,
	}); err != nil {
		t.Fatalf("insert B: %v", err)
	}

	got, err := store.RecentMutating(ctx, time.Now().Add(-time.Minute), 100)
	if err != nil {
		t.Fatalf("RecentMutating: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("scoped read: want 1 event, got %d (%+v)", len(got), got)
	}
	if got[0].SessionID != "sA" {
		t.Errorf("scoped read leaked from B: %s", got[0].SessionID)
	}
}

// TestSQLiteDisciplineStore_StrictScoping_ScopedDoesNotSeeNULL: scoped
// store does NOT see legacy NULL-workspace rows, preventing pre-migration
// data from leaking into a multi-tenant scoped read.
func TestSQLiteDisciplineStore_StrictScoping_ScopedDoesNotSeeNULL(t *testing.T) {
	t.Parallel() // [F0925-10]
	ctx := context.Background()
	wsA := uuid.New()

	db := openDisciplineDBWS(t, wsA.String())
	store := wbtsqlite.NewDisciplineStore(db)

	// Workspace-A row.
	if err := store.Insert(ctx, discipline.InsertParams{
		SessionID:  "sA",
		ToolName:   "add_task",
		IsMutating: true,
		Ok:         true,
	}); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	// Legacy NULL-workspace row. The store's workspace scope is on the
	// DB, so to write a NULL row we go through the raw conn.
	const insertNullSQL = `INSERT INTO discipline_events
		(session_id, repo_name, tool_name, is_mutating, observed_at, workspace_id)
		VALUES (?, NULL, ?, 1, ?, NULL)`
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	if err := db.ExecContext(ctx, insertNullSQL, "sNULL", "log_decision", now); err != nil {
		t.Fatalf("insert NULL row: %v", err)
	}

	got, err := store.RecentMutating(ctx, time.Now().Add(-time.Minute), 100)
	if err != nil {
		t.Fatalf("RecentMutating: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("scoped read: want 1 event (only A), got %d (%+v)", len(got), got)
	}
	if got[0].SessionID != "sA" {
		t.Errorf("scoped read leaked NULL row: %s", got[0].SessionID)
	}
}

// TestSQLiteDisciplineStore_StrictScoping_UnscopedSeesOnlyNULL: an
// unscoped store sees only NULL-workspace rows, never scoped rows. Mirror
// of the scoped test, ensuring the partition is symmetric.
func TestSQLiteDisciplineStore_StrictScoping_UnscopedSeesOnlyNULL(t *testing.T) {
	t.Parallel() // [F0925-10]
	ctx := context.Background()
	wsA := uuid.New()
	wsB := uuid.New()

	db := openDisciplineDBWS(t, "") // unscoped
	store := wbtsqlite.NewDisciplineStore(db)

	// Three rows: A, B, NULL.
	if err := store.Insert(ctx, discipline.InsertParams{
		SessionID:   "sA",
		ToolName:    "add_task",
		IsMutating:  true,
		Ok:          true,
		WorkspaceID: &wsA,
	}); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	if err := store.Insert(ctx, discipline.InsertParams{
		SessionID:   "sB",
		ToolName:    "complete_task",
		IsMutating:  true,
		Ok:          true,
		WorkspaceID: &wsB,
	}); err != nil {
		t.Fatalf("insert B: %v", err)
	}
	if err := store.Insert(ctx, discipline.InsertParams{
		SessionID:  "sNULL",
		ToolName:   "log_decision",
		IsMutating: true,
		Ok:         true,
	}); err != nil {
		t.Fatalf("insert NULL: %v", err)
	}

	got, err := store.RecentMutating(ctx, time.Now().Add(-time.Minute), 100)
	if err != nil {
		t.Fatalf("RecentMutating: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("unscoped read: want 1 (NULL only), got %d (%+v)", len(got), got)
	}
	if got[0].SessionID != "sNULL" {
		t.Errorf("unscoped read leaked scoped row: %s", got[0].SessionID)
	}
}

// TestSQLiteDisciplineStore_StrictScoping_RecentDecisionTimes: the
// decision-times read path applies the same partition rule.
func TestSQLiteDisciplineStore_StrictScoping_RecentDecisionTimes(t *testing.T) {
	t.Parallel() // [F0925-10]
	ctx := context.Background()
	wsA := uuid.New()
	wsB := uuid.New()

	db := openDisciplineDBWS(t, wsA.String())
	store := wbtsqlite.NewDisciplineStore(db)

	// log_decision in workspace A (via scope) and confirm_plan in B
	// (override) share session_id "shared". Scoped read must see ONLY
	// the A timestamp.
	if err := store.Insert(ctx, discipline.InsertParams{
		SessionID:  "shared",
		ToolName:   "log_decision",
		IsMutating: true,
		Ok:         true,
	}); err != nil {
		t.Fatalf("insert A decision: %v", err)
	}
	if err := store.Insert(ctx, discipline.InsertParams{
		SessionID:   "shared",
		ToolName:    "confirm_plan",
		IsMutating:  true,
		Ok:          true,
		WorkspaceID: &wsB,
	}); err != nil {
		t.Fatalf("insert B decision: %v", err)
	}

	got, err := store.RecentDecisionTimes(ctx, "shared", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("RecentDecisionTimes: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("RecentDecisionTimes: want 1 (workspace A only), got %d", len(got))
	}
}
