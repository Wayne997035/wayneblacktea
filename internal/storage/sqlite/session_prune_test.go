package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
)

func handoffRowExists(t *testing.T, d *sqlite.DB, id string) bool {
	t.Helper()
	var n int
	row := d.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM session_handoffs WHERE id = ?`, id)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count session_handoffs: %v", err)
	}
	return n == 1
}

// TestSessionStore_PruneOlderThan_Expired verifies that a RESOLVED handoff
// older than the cutoff is deleted.
func TestSessionStore_PruneOlderThan_Expired(t *testing.T) {
	d, s := openSessionStoreWithDB(t, "")
	ctx := context.Background()

	h, err := s.SetHandoff(ctx, session.HandoffParams{RepoName: "r", Intent: "expired resolved"})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}
	if err := s.Resolve(ctx, h.ID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	resolvedAt := time.Now().Add(-400 * 24 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z07:00")
	if err := d.ExecContext(
		ctx,
		`UPDATE session_handoffs SET resolved_at = ? WHERE id = ?`, resolvedAt, h.ID.String(),
	); err != nil {
		t.Fatalf("backdate resolved_at: %v", err)
	}

	cutoff := time.Now().Add(-365 * 24 * time.Hour)
	n, err := s.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if n != 1 {
		t.Errorf("rows deleted = %d, want 1", n)
	}
	if handoffRowExists(t, d, h.ID.String()) {
		t.Error("expired resolved handoff survived prune")
	}
}

// TestSessionStore_PruneOlderThan_NotExpired_OpenHandoffNeverPruned verifies
// that an unresolved handoff is never pruned regardless of age, and that a
// recently-resolved handoff is not pruned.
func TestSessionStore_PruneOlderThan_NotExpired_OpenHandoffNeverPruned(t *testing.T) {
	d, s := openSessionStoreWithDB(t, "")
	ctx := context.Background()

	open, err := s.SetHandoff(ctx, session.HandoffParams{RepoName: "r", Intent: "still open"})
	if err != nil {
		t.Fatalf("SetHandoff (open): %v", err)
	}
	ancientCreated := time.Now().Add(-1000 * 24 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z07:00")
	if err := d.ExecContext(
		ctx,
		`UPDATE session_handoffs SET created_at = ? WHERE id = ?`, ancientCreated, open.ID.String(),
	); err != nil {
		t.Fatalf("backdate created_at: %v", err)
	}

	fresh, err := s.SetHandoff(ctx, session.HandoffParams{RepoName: "r", Intent: "recently resolved"})
	if err != nil {
		t.Fatalf("SetHandoff (fresh): %v", err)
	}
	if err := s.Resolve(ctx, fresh.ID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	cutoff := time.Now().Add(-365 * 24 * time.Hour)
	if _, err := s.PruneOlderThan(ctx, cutoff); err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if !handoffRowExists(t, d, open.ID.String()) {
		t.Error("open handoff was pruned despite resolved_at IS NULL")
	}
	if !handoffRowExists(t, d, fresh.ID.String()) {
		t.Error("recently-resolved handoff was pruned")
	}
}

// TestSessionStore_PruneOlderThan_Boundary verifies the cutoff comparison is
// strict-less-than: a resolved handoff exactly at the cutoff survives, one
// resolved 1 second before the cutoff is deleted.
func TestSessionStore_PruneOlderThan_Boundary(t *testing.T) {
	d, s := openSessionStoreWithDB(t, "")
	ctx := context.Background()

	cutoff := time.Now().Add(-365 * 24 * time.Hour)
	cutoffStr := cutoff.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	justBeforeStr := cutoff.Add(-1 * time.Second).UTC().Format("2006-01-02T15:04:05.000Z07:00")

	atCutoff, err := s.SetHandoff(ctx, session.HandoffParams{RepoName: "r", Intent: "at cutoff"})
	if err != nil {
		t.Fatalf("SetHandoff (atCutoff): %v", err)
	}
	if err := s.Resolve(ctx, atCutoff.ID); err != nil {
		t.Fatalf("Resolve (atCutoff): %v", err)
	}
	if err := d.ExecContext(
		ctx,
		`UPDATE session_handoffs SET resolved_at = ? WHERE id = ?`, cutoffStr, atCutoff.ID.String(),
	); err != nil {
		t.Fatalf("backdate atCutoff: %v", err)
	}

	justBefore, err := s.SetHandoff(ctx, session.HandoffParams{RepoName: "r", Intent: "just before cutoff"})
	if err != nil {
		t.Fatalf("SetHandoff (justBefore): %v", err)
	}
	if err := s.Resolve(ctx, justBefore.ID); err != nil {
		t.Fatalf("Resolve (justBefore): %v", err)
	}
	if err := d.ExecContext(
		ctx,
		`UPDATE session_handoffs SET resolved_at = ? WHERE id = ?`, justBeforeStr, justBefore.ID.String(),
	); err != nil {
		t.Fatalf("backdate justBefore: %v", err)
	}

	if _, err := s.PruneOlderThan(ctx, cutoff); err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if !handoffRowExists(t, d, atCutoff.ID.String()) {
		t.Error("handoff resolved exactly at cutoff was pruned (want: survive)")
	}
	if handoffRowExists(t, d, justBefore.ID.String()) {
		t.Error("handoff resolved just before cutoff survived (want: pruned)")
	}
}
