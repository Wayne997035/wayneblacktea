package sqlite_test

import (
	"context"
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
	db, err := wbtsqlite.Open(context.Background(), ":memory:", workspaceID)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSQLiteDisciplineStore_Insert(t *testing.T) {
	ctx := context.Background()
	wsID := uuid.New()
	linkedID := uuid.New()

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
			},
		},
		{
			name: "minimal required fields, read-only",
			params: discipline.InsertParams{
				SessionID:  "mcp-9999-0",
				ToolName:   "list_tasks",
				IsMutating: false,
			},
		},
		{
			name: "empty repo_name persists as NULL",
			params: discipline.InsertParams{
				SessionID:  "mcp-1234-5678",
				ToolName:   "add_task",
				IsMutating: true,
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

			// Read back via RecentMutating / store query: only mutating
			// events appear there, so this also exercises the filter.
			events, err := store.RecentMutating(ctx, time.Now().Add(-time.Minute), 100)
			if err != nil {
				t.Fatalf("RecentMutating: %v", err)
			}
			if tc.params.IsMutating {
				if len(events) != 1 {
					t.Fatalf("expected 1 mutating event, got %d", len(events))
				}
				ev := events[0]
				if ev.SessionID != tc.params.SessionID {
					t.Errorf("session_id: want %q, got %q", tc.params.SessionID, ev.SessionID)
				}
				if ev.ToolName != tc.params.ToolName {
					t.Errorf("tool_name: want %q, got %q", tc.params.ToolName, ev.ToolName)
				}
				if !ev.IsMutating {
					t.Errorf("is_mutating: want true, got false")
				}
				if ev.RepoName != tc.params.RepoName {
					t.Errorf("repo_name: want %q, got %q", tc.params.RepoName, ev.RepoName)
				}
			} else if len(events) != 0 {
				t.Fatalf("expected 0 mutating events for read-only insert, got %d", len(events))
			}
		})
	}
}

func TestSQLiteDisciplineStore_RecentMutating(t *testing.T) {
	ctx := context.Background()
	db := openDisciplineDB(t)
	store := wbtsqlite.NewDisciplineStore(db)

	// Seed mix of mutating/read-only events.
	for _, p := range []discipline.InsertParams{
		{SessionID: "s1", ToolName: "add_task", IsMutating: true},
		{SessionID: "s1", ToolName: "list_tasks", IsMutating: false},
		{SessionID: "s2", ToolName: "complete_task", IsMutating: true},
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
	ctx := context.Background()
	db := openDisciplineDB(t)
	store := wbtsqlite.NewDisciplineStore(db)

	for _, p := range []discipline.InsertParams{
		{SessionID: "s1", ToolName: "log_decision", IsMutating: true},
		{SessionID: "s1", ToolName: "confirm_plan", IsMutating: true},
		{SessionID: "s1", ToolName: "add_task", IsMutating: true}, // not a decision tool
		{SessionID: "s2", ToolName: "log_decision", IsMutating: true},
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
		// Only log_decision + confirm_plan rows under s1: 2 expected
		// (add_task is mutating but NOT a decision tool, so excluded).
		if len(got) != 2 {
			t.Errorf("expected 2 decisions, got %d", len(got))
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
	}); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	// Row in workspace B (explicit override on a store scoped to A).
	if err := store.Insert(ctx, discipline.InsertParams{
		SessionID:   "sB",
		ToolName:    "complete_task",
		IsMutating:  true,
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
	ctx := context.Background()
	wsA := uuid.New()

	db := openDisciplineDBWS(t, wsA.String())
	store := wbtsqlite.NewDisciplineStore(db)

	// Workspace-A row.
	if err := store.Insert(ctx, discipline.InsertParams{
		SessionID:  "sA",
		ToolName:   "add_task",
		IsMutating: true,
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
		WorkspaceID: &wsA,
	}); err != nil {
		t.Fatalf("insert A: %v", err)
	}
	if err := store.Insert(ctx, discipline.InsertParams{
		SessionID:   "sB",
		ToolName:    "complete_task",
		IsMutating:  true,
		WorkspaceID: &wsB,
	}); err != nil {
		t.Fatalf("insert B: %v", err)
	}
	if err := store.Insert(ctx, discipline.InsertParams{
		SessionID:  "sNULL",
		ToolName:   "log_decision",
		IsMutating: true,
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
	}); err != nil {
		t.Fatalf("insert A decision: %v", err)
	}
	if err := store.Insert(ctx, discipline.InsertParams{
		SessionID:   "shared",
		ToolName:    "confirm_plan",
		IsMutating:  true,
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
