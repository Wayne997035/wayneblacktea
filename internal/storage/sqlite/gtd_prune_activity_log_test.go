package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
)

// backdateActivityLog directly rewrites the created_at column for all
// activity_log rows with the given action, bypassing LogActivity (which
// always stamps NOW()). Mirrors the backdating pattern used in
// mergedprs/store_test.go.
func backdateActivityLog(t *testing.T, d *sqlite.DB, action string, createdAt time.Time) {
	t.Helper()
	ts := createdAt.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	if err := d.ExecContext(
		context.Background(),
		`UPDATE activity_log SET created_at = ? WHERE action = ?`, ts, action,
	); err != nil {
		t.Fatalf("backdate activity_log: %v", err)
	}
}

func countActivityLogRows(t *testing.T, d *sqlite.DB, action string) int {
	t.Helper()
	var n int
	row := d.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM activity_log WHERE action = ?`, action)
	if err := row.Scan(&n); err != nil {
		t.Fatalf("count activity_log: %v", err)
	}
	return n
}

// TestGTDStore_PruneOlderThan_ActivityLog_Expired verifies that a row older
// than the cutoff is deleted.
func TestGTDStore_PruneOlderThan_ActivityLog_Expired(t *testing.T) {
	ctx := context.Background()
	d, err := sqlite.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s := sqlite.NewGTDStore(d)

	if err := s.LogActivity(ctx, "actor", "expired_action", nil, ""); err != nil {
		t.Fatalf("LogActivity: %v", err)
	}
	backdateActivityLog(t, d, "expired_action", time.Now().Add(-400*24*time.Hour))

	cutoff := time.Now().Add(-365 * 24 * time.Hour)
	n, err := s.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if n != 1 {
		t.Errorf("rows deleted = %d, want 1", n)
	}
	if got := countActivityLogRows(t, d, "expired_action"); got != 0 {
		t.Errorf("expired row survived: count = %d, want 0", got)
	}
}

// TestGTDStore_PruneOlderThan_ActivityLog_NotExpired verifies that a row
// younger than the cutoff is NOT deleted.
func TestGTDStore_PruneOlderThan_ActivityLog_NotExpired(t *testing.T) {
	ctx := context.Background()
	d, err := sqlite.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s := sqlite.NewGTDStore(d)

	if err := s.LogActivity(ctx, "actor", "fresh_action", nil, ""); err != nil {
		t.Fatalf("LogActivity: %v", err)
	}
	backdateActivityLog(t, d, "fresh_action", time.Now().Add(-10*24*time.Hour))

	cutoff := time.Now().Add(-365 * 24 * time.Hour)
	n, err := s.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if n != 0 {
		t.Errorf("rows deleted = %d, want 0 (row is within retention)", n)
	}
	if got := countActivityLogRows(t, d, "fresh_action"); got != 1 {
		t.Errorf("fresh row was pruned: count = %d, want 1", got)
	}
}

// TestGTDStore_PruneOlderThan_ActivityLog_Boundary verifies the cutoff
// comparison is strict-less-than: a row exactly at the cutoff survives, a
// row 1 second before the cutoff is deleted.
func TestGTDStore_PruneOlderThan_ActivityLog_Boundary(t *testing.T) {
	ctx := context.Background()
	d, err := sqlite.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	s := sqlite.NewGTDStore(d)

	cutoff := time.Now().Add(-365 * 24 * time.Hour)

	if err := s.LogActivity(ctx, "actor", "at_cutoff", nil, ""); err != nil {
		t.Fatalf("LogActivity (at_cutoff): %v", err)
	}
	backdateActivityLog(t, d, "at_cutoff", cutoff)

	if err := s.LogActivity(ctx, "actor", "just_before_cutoff", nil, ""); err != nil {
		t.Fatalf("LogActivity (just_before_cutoff): %v", err)
	}
	backdateActivityLog(t, d, "just_before_cutoff", cutoff.Add(-1*time.Second))

	n, err := s.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if n != 1 {
		t.Errorf("rows deleted = %d, want 1 (only just_before_cutoff)", n)
	}
	if got := countActivityLogRows(t, d, "at_cutoff"); got != 1 {
		t.Errorf("row exactly at cutoff was pruned: count = %d, want 1 (survive)", got)
	}
	if got := countActivityLogRows(t, d, "just_before_cutoff"); got != 0 {
		t.Errorf("row just before cutoff survived: count = %d, want 0 (pruned)", got)
	}
}
