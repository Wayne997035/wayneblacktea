//go:build integration

package completioncandidate_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/google/uuid"
)

// TestCandidateStore_PG_WriteAutoApplied verifies the PG path of the
// reconcile-time audit write. Mirrors TestSQLiteWriteAutoApplied.
func TestCandidateStore_PG_WriteAutoApplied(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := completioncandidate.NewPgStore(pool, &wsID)
	ctx := context.Background()

	taskID := uuid.New()
	insertPgTask(t, pool, wsID, taskID, "completed", time.Now().UTC())

	prURL := "https://github.com/owner/repo/pull/1"
	if err := store.WriteAutoApplied(ctx, taskID, []string{prURL}, prURL); err != nil {
		t.Fatalf("WriteAutoApplied: %v", err)
	}

	const q = `SELECT status, reason, suggested_artifact, evidence_refs, resolved_at
		FROM completion_candidates WHERE task_id = $1`
	var (
		status, reason string
		artifact       *string
		refs           []string
		resolvedAt     *time.Time
	)
	if err := pool.QueryRow(ctx, q, taskID).Scan(&status, &reason, &artifact, &refs, &resolvedAt); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if status != "auto_applied" {
		t.Errorf("status = %q, want auto_applied", status)
	}
	if reason != "artifact_evidence" {
		t.Errorf("reason = %q, want artifact_evidence", reason)
	}
	if artifact == nil || *artifact != prURL {
		t.Errorf("suggested_artifact = %v, want %q", artifact, prURL)
	}
	if len(refs) != 1 || refs[0] != prURL {
		t.Errorf("evidence_refs = %v, want [%q]", refs, prURL)
	}
	if resolvedAt == nil {
		t.Error("resolved_at must be set")
	}
}

// TestCandidateStore_PG_WriteAutoApplied_OnConflict verifies the ON CONFLICT
// path: pre-existing pending candidate transitions to auto_applied on re-call.
func TestCandidateStore_PG_WriteAutoApplied_OnConflict(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := completioncandidate.NewPgStore(pool, &wsID)
	ctx := context.Background()

	taskID := uuid.New()
	insertPgTask(t, pool, wsID, taskID, "in_progress", time.Now().UTC())

	// Seed a pending candidate via UpsertCandidate first.
	_, err := store.UpsertCandidate(ctx, completioncandidate.UpsertParams{
		WorkspaceID:  &wsID,
		TaskID:       taskID,
		Reason:       completioncandidate.ReasonArtifactEvidence,
		Confidence:   completioncandidate.ConfidenceMedium,
		EvidenceRefs: []string{"old"},
	})
	if err != nil {
		t.Fatalf("seed Upsert: %v", err)
	}

	prURL := "https://github.com/owner/repo/pull/2"
	if err := store.WriteAutoApplied(ctx, taskID, []string{prURL}, prURL); err != nil {
		t.Fatalf("WriteAutoApplied: %v", err)
	}

	const q = `SELECT status, evidence_refs, suggested_artifact, resolved_at
		FROM completion_candidates WHERE task_id = $1`
	var (
		status   string
		refs     []string
		artifact *string
		resAt    *time.Time
	)
	if err := pool.QueryRow(ctx, q, taskID).Scan(&status, &refs, &artifact, &resAt); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if status != "auto_applied" {
		t.Errorf("status = %q, want auto_applied (forced by WriteAutoApplied)", status)
	}
	if len(refs) != 1 || refs[0] != prURL {
		t.Errorf("evidence_refs = %v, want [%q] (replaced)", refs, prURL)
	}
	if artifact == nil || *artifact != prURL {
		t.Errorf("suggested_artifact = %v, want %q", artifact, prURL)
	}
	if resAt == nil {
		t.Error("resolved_at must be set on flip to auto_applied")
	}

	// Row count must remain 1.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM completion_candidates WHERE task_id=$1`,
		taskID,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("row count = %d, want 1 (ON CONFLICT must not duplicate)", n)
	}
}
