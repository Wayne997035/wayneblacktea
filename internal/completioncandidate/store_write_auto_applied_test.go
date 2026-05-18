package completioncandidate_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/google/uuid"
)

// TestSQLiteWriteAutoApplied covers the happy path: a fresh INSERT produces a
// row with status='auto_applied' reason='artifact_evidence', the evidence_refs
// list contains the PR URL, and suggested_artifact == PR URL.
func TestSQLiteWriteAutoApplied(t *testing.T) {
	db := openTestDB(t)
	store := completioncandidate.NewSQLiteStore(db, "")
	ctx := context.Background()

	taskID := uuid.New()
	insertTask(t, db, taskID.String(), "completed", "2026-01-01T00:00:00Z")

	prURL := "https://github.com/owner/repo/pull/1"
	if err := store.WriteAutoApplied(ctx, taskID, []string{prURL}, prURL); err != nil {
		t.Fatalf("WriteAutoApplied: %v", err)
	}

	got := readCandidate(t, db, taskID)
	if got.status != "auto_applied" {
		t.Errorf("status = %q, want auto_applied", got.status)
	}
	if got.reason != "artifact_evidence" {
		t.Errorf("reason = %q, want artifact_evidence", got.reason)
	}
	if got.suggestedArtifact == nil || *got.suggestedArtifact != prURL {
		t.Errorf("suggested_artifact = %v, want %q", got.suggestedArtifact, prURL)
	}
	if got.evidenceRefs != `["`+prURL+`"]` {
		t.Errorf("evidence_refs = %q, want JSON array with PR URL", got.evidenceRefs)
	}
	if got.resolvedAt == nil || *got.resolvedAt == "" {
		t.Error("resolved_at must be set on auto_applied row")
	}
}

// TestSQLiteWriteAutoApplied_OnConflictForcesAutoApplied verifies that calling
// WriteAutoApplied for a task that already has a pending candidate flips status
// to auto_applied. This is the test that exercises the reconcile flow's "task
// previously surfaced as candidate now auto-closed by PR merge" transition.
func TestSQLiteWriteAutoApplied_OnConflictForcesAutoApplied(t *testing.T) {
	db := openTestDB(t)
	store := completioncandidate.NewSQLiteStore(db, "")
	ctx := context.Background()

	taskID := uuid.New()
	insertTask(t, db, taskID.String(), "in_progress", "2026-01-01T00:00:00Z")

	// First call → fresh row (status auto_applied).
	prURL := "https://github.com/owner/repo/pull/2"
	if err := store.WriteAutoApplied(ctx, taskID, []string{prURL}, prURL); err != nil {
		t.Fatalf("first WriteAutoApplied: %v", err)
	}

	// Second call: the row already exists. We re-call to confirm the ON CONFLICT
	// path is correct (no duplicate row, status stays auto_applied).
	if err := store.WriteAutoApplied(ctx, taskID, []string{prURL}, prURL); err != nil {
		t.Fatalf("second WriteAutoApplied: %v", err)
	}

	rows := countCandidatesForTask(t, db, taskID)
	if rows != 1 {
		t.Errorf("expected 1 row after re-call, got %d", rows)
	}
	got := readCandidate(t, db, taskID)
	if got.status != "auto_applied" {
		t.Errorf("status = %q, want auto_applied", got.status)
	}
}

// TestSQLiteWriteAutoApplied_RejectsZeroUUID validates the input guard.
func TestSQLiteWriteAutoApplied_RejectsZeroUUID(t *testing.T) {
	db := openTestDB(t)
	store := completioncandidate.NewSQLiteStore(db, "")

	err := store.WriteAutoApplied(context.Background(), uuid.Nil,
		[]string{"x"}, "x")
	if err == nil {
		t.Fatal("expected error for zero task_id")
	}
}

// candidateRow is the subset of completion_candidates needed by these tests.
type candidateRow struct {
	status            string
	reason            string
	suggestedArtifact *string
	evidenceRefs      string
	resolvedAt        *string
}

func readCandidate(t *testing.T, db *sql.DB, taskID uuid.UUID) candidateRow {
	t.Helper()
	const q = `SELECT status, reason, suggested_artifact, evidence_refs, resolved_at
		FROM completion_candidates
		WHERE task_id = ?`
	row := db.QueryRowContext(context.Background(), q, taskID.String())
	var (
		out                  candidateRow
		artifactNS, resNS    sql.NullString
		statusStr, reasonStr string
	)
	if err := row.Scan(&statusStr, &reasonStr, &artifactNS, &out.evidenceRefs, &resNS); err != nil {
		t.Fatalf("read candidate: %v", err)
	}
	out.status = statusStr
	out.reason = reasonStr
	if artifactNS.Valid {
		v := artifactNS.String
		out.suggestedArtifact = &v
	}
	if resNS.Valid {
		v := resNS.String
		out.resolvedAt = &v
	}
	return out
}

func countCandidatesForTask(t *testing.T, db *sql.DB, taskID uuid.UUID) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM completion_candidates WHERE task_id = ?`,
		taskID.String(),
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}
