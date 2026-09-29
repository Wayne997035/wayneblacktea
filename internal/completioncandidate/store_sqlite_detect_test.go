package completioncandidate_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/testutil/sqlitetemplate"
	"github.com/google/uuid"
)

// detectSQLiteTemplateKey identifies this file's shared migrate-once SQLite
// template in sqlitetemplate's cross-package template registry (see that
// package's doc comment for why migrate-once exists). Distinct from the
// "internal/storage/sqlite" and "internal/mcp" keys used elsewhere — each
// key must map to exactly one Migrator for the process's lifetime.
const detectSQLiteTemplateKey = "internal/completioncandidate/detect"

// oldTimestamp is well outside any lookback/stale window this package's
// detection rules use (max 4 weeks for rule1, max 30 days for rules 2-4).
const oldTimestamp = "2000-01-01T00:00:00.000Z"

// migrateDetectSQLiteTemplate builds a fully-migrated SQLite database at
// path via the real production wbtsqlite.Open/Close path -- the same thing
// completioncandidate.NewSQLiteStore's real caller
// (internal/mcp/capabilities.go ResolveCandidateStore) sits on top of.
func migrateDetectSQLiteTemplate(ctx context.Context, path string) error {
	d, err := wbtsqlite.Open(ctx, path, "")
	if err != nil {
		return fmt.Errorf("migrate completioncandidate detect sqlite template: %w", err)
	}
	if err := d.Close(); err != nil {
		return fmt.Errorf("close completioncandidate detect sqlite template: %w", err)
	}
	return nil
}

// openDetectDB opens a fresh, independent, fully-migrated SQLite database
// for the TestSQLiteDetect_* tests below, wired exactly like production:
// wbtsqlite.Open's connection pool is capped at 1 (internal/storage/sqlite's
// SetMaxOpenConns(1)), which is the precondition [F0929-75] fixes a deadlock
// under.
func openDetectDB(t *testing.T, name string) *wbtsqlite.DB {
	t.Helper()
	path := sqlitetemplate.Path(t, detectSQLiteTemplateKey, migrateDetectSQLiteTemplate, name)
	d, err := wbtsqlite.Open(context.Background(), path, "")
	if err != nil {
		t.Fatalf("open completioncandidate detect sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// seedStaleInProgress seeds one task that only rule1 (detectStaleInProgress)
// matches: status='in_progress' and updated_at far outside the default 24h
// stale window.
func seedStaleInProgress(t *testing.T, d *wbtsqlite.DB, taskID uuid.UUID) {
	t.Helper()
	if err := d.ExecContext(context.Background(),
		`INSERT INTO tasks (id, title, status, updated_at) VALUES (?,?,?,?)`,
		taskID.String(), "detect test task", "in_progress", oldTimestamp,
	); err != nil {
		t.Fatalf("seed task (rule1): %v", err)
	}
}

// seedFinishWorkGap seeds a completed work_session linked to a pending task
// via work_session_tasks -- only rule2 (detectFinishWorkGap) matches this
// shape. status='pending' (not 'in_progress') so rule1 cannot also match.
func seedFinishWorkGap(t *testing.T, d *wbtsqlite.DB, taskID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, updated_at) VALUES (?,?,?,?)`,
		taskID.String(), "detect test task", "pending", time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed task (rule2): %v", err)
	}
	sessionID := uuid.New()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := d.ExecContext(ctx,
		`INSERT INTO work_sessions (id, workspace_id, repo_name, title, goal, status, source, completed_at)
			VALUES (?,?,?,?,?,?,?,?)`,
		sessionID.String(), "detect-test-workspace", "detect-test-repo",
		"detect test session", "detect test goal", "completed", "manual", now,
	); err != nil {
		t.Fatalf("seed work_sessions (rule2): %v", err)
	}
	if err := d.ExecContext(ctx,
		`INSERT INTO work_session_tasks (session_id, task_id, role) VALUES (?,?,?)`,
		sessionID.String(), taskID.String(), "primary",
	); err != nil {
		t.Fatalf("seed work_session_tasks (rule2): %v", err)
	}
}

// seedArtifactEvidence seeds a recent activity_log row whose notes reference
// the task and whose action is 'worksession:finished' -- only rule3
// (detectArtifactEvidence) matches this shape. status='pending' so rule1
// cannot also match.
func seedArtifactEvidence(t *testing.T, d *wbtsqlite.DB, taskID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, updated_at) VALUES (?,?,?,?)`,
		taskID.String(), "detect test task", "pending", time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed task (rule3): %v", err)
	}
	notes := "finished work, see https://example.com/pr/1 for task " + taskID.String()
	if err := d.ExecContext(ctx,
		`INSERT INTO activity_log (id, actor, action, notes, created_at) VALUES (?,?,?,?,?)`,
		uuid.New().String(), "system", "worksession:finished", notes, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed activity_log (rule3): %v", err)
	}
}

// seedCompletionSignal seeds a recent activity_log row whose notes reference
// the task and whose action is a completion-signal action -- only rule4
// (detectCompletionSignal) matches this shape. status='pending' so rule1
// cannot also match, and action='task:updated' (not 'worksession:finished')
// so rule3 cannot also match.
func seedCompletionSignal(t *testing.T, d *wbtsqlite.DB, taskID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, updated_at) VALUES (?,?,?,?)`,
		taskID.String(), "detect test task", "pending", time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed task (rule4): %v", err)
	}
	notes := "task:updated event for " + taskID.String()
	if err := d.ExecContext(ctx,
		`INSERT INTO activity_log (id, actor, action, notes, created_at) VALUES (?,?,?,?,?)`,
		uuid.New().String(), "system", "task:updated", notes, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed activity_log (rule4): %v", err)
	}
}

// runDetectSingleRule opens a fresh, production-shaped (single-connection)
// SQLite database, seeds it with data that matches exactly one detection
// rule, runs DetectAndUpsert under a 5s timeout, and asserts exactly one
// candidate came back with the expected reason -- proving [F0929-75]: the
// rule no longer deadlocks against its own open rows on a 1-connection pool.
func runDetectSingleRule(
	t *testing.T,
	dbName string,
	seed func(t *testing.T, d *wbtsqlite.DB, taskID uuid.UUID),
	reason completioncandidate.Reason,
) {
	t.Helper()
	d := openDetectDB(t, dbName)

	// Production-shape guard: this must be the same 1-connection pool
	// production runs under, or the test would not actually exercise the
	// deadlock this fix addresses.
	if stats := d.SqlConn().Stats(); stats.MaxOpenConnections != 1 {
		t.Fatalf("test DB is not production-shaped: MaxOpenConnections = %d, want 1", stats.MaxOpenConnections)
	}

	taskID := uuid.New()
	seed(t, d, taskID)

	store := completioncandidate.NewSQLiteStore(d.SqlConn(), d.WorkspaceID())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	candidates, err := store.DetectAndUpsert(ctx, completioncandidate.DetectParams{})
	if err != nil {
		t.Fatalf("DetectAndUpsert: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("DetectAndUpsert: got %d candidate(s), want exactly 1: %+v", len(candidates), candidates)
	}
	got := candidates[0]
	t.Logf("DetectAndUpsert produced %d candidate(s): reason=%s task_id=%s", len(candidates), got.Reason, got.TaskID)
	if got.Reason != reason {
		t.Errorf("candidate reason: got %q, want %q", got.Reason, reason)
	}
	if got.TaskID != taskID {
		t.Errorf("candidate task_id: got %s, want %s", got.TaskID, taskID)
	}

	pending, err := store.ListPendingCandidates(ctx, nil)
	if err != nil {
		t.Fatalf("ListPendingCandidates: %v", err)
	}
	found := false
	for _, c := range pending {
		if c.TaskID == taskID && c.Reason == reason {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListPendingCandidates does not contain the detected candidate (task_id=%s reason=%s)", taskID, reason)
	}
}

// TestSQLiteDetect_StaleInProgress_SingleConnNoDeadlock exercises rule1 on a
// production-shaped single-connection SQLite database. [F0929-75]
func TestSQLiteDetect_StaleInProgress_SingleConnNoDeadlock(t *testing.T) {
	runDetectSingleRule(t, "rule1-stale.db", seedStaleInProgress, completioncandidate.ReasonStaleInProgress)
}

// TestSQLiteDetect_FinishWorkGap_SingleConnNoDeadlock exercises rule2 on a
// production-shaped single-connection SQLite database. [F0929-75]
func TestSQLiteDetect_FinishWorkGap_SingleConnNoDeadlock(t *testing.T) {
	runDetectSingleRule(t, "rule2-finishgap.db", seedFinishWorkGap, completioncandidate.ReasonFinishWorkGap)
}

// TestSQLiteDetect_ArtifactEvidence_SingleConnNoDeadlock exercises rule3 on
// a production-shaped single-connection SQLite database. [F0929-75]
func TestSQLiteDetect_ArtifactEvidence_SingleConnNoDeadlock(t *testing.T) {
	runDetectSingleRule(t, "rule3-artifact.db", seedArtifactEvidence, completioncandidate.ReasonArtifactEvidence)
}

// TestSQLiteDetect_CompletionSignal_SingleConnNoDeadlock exercises rule4 on
// a production-shaped single-connection SQLite database. [F0929-75]
func TestSQLiteDetect_CompletionSignal_SingleConnNoDeadlock(t *testing.T) {
	runDetectSingleRule(t, "rule4-completion.db", seedCompletionSignal, completioncandidate.ReasonCompletionSignal)
}
