package sqlite_test

// Tests for ProposalStore.MarkAndDeleteStaleProposals — the SQLite-native
// counterpart to scheduler.go's Postgres two-step runDailyPendingProposalsPrune
// (GTD 80cf80b6, PR #152 2nd-army finding: SQLite previously had NO
// prune/delete path for pending_proposals at all). Mirrors the PG
// integration coverage in internal/scheduler/pending_proposals_prune_pg_test.go
// (TestScheduler_DailyPendingProposalsPrune_DeletesOnlyExpiredRows /
// TestRunDailyPendingProposalsPrune_TypeTaskTTL) against a real SQLite
// :memory: DB instead of a testcontainer — SQLite is the documented
// testcontainers exception (backend-security-design.md §6.5): no container
// image exists for it, so a real :memory: DB is the "not mocked" bar here.

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// markReasonTTLExpired mirrors scheduler.pendingProposalsTaskTTLReason (a
// different package — this test can't import it without creating an import
// cycle, so it's a local copy for goconst's min-occurrences-3 threshold).
const markReasonTTLExpired = "ttl-expired-30d"

// proposalStatusAccepted / proposalStatusRejected mirror
// proposal.StatusAccepted / proposal.StatusRejected as plain strings for
// goconst's min-occurrences-3 threshold across this file's status-comparison
// assertions.
const (
	proposalStatusAccepted = "accepted"
	proposalStatusRejected = "rejected"
)

// importPruneSeed inserts a pending_proposals row with explicit
// type/status/created_at/resolved_at via ProposalStore.ImportProposal so
// tests can seed deterministic ages without depending on the DB's own clock.
func importPruneSeed(
	t *testing.T, s *sqlite.ProposalStore, typ, status string, createdAt time.Time, resolvedAt *time.Time,
) uuid.UUID {
	t.Helper()
	id := uuid.New()
	p := db.PendingProposal{
		ID:        id,
		Type:      typ,
		Payload:   []byte(`{}`),
		Status:    status,
		CreatedAt: pgtype.Timestamptz{Time: createdAt, Valid: true},
	}
	if resolvedAt != nil {
		p.ResolvedAt = pgtype.Timestamptz{Time: *resolvedAt, Valid: true}
	}
	if err := s.ImportProposal(context.Background(), p); err != nil {
		t.Fatalf("ImportProposal seed (type=%s status=%s): %v", typ, status, err)
	}
	return id
}

func rowExists(t *testing.T, s *sqlite.ProposalStore, id uuid.UUID) bool {
	t.Helper()
	_, err := s.Get(context.Background(), id)
	return err == nil
}

func rowStatus(t *testing.T, s *sqlite.ProposalStore, id uuid.UUID) string {
	t.Helper()
	row, err := s.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	return row.Status
}

// TestProposalStore_MarkAndDeleteStaleProposals_DeletesOnlyExpiredRows is the
// SQLite twin of TestScheduler_DailyPendingProposalsPrune_DeletesOnlyExpiredRows
// (pending_proposals_prune_pg_test.go): seeds a mix of in-window/out-of-window
// rows and asserts the retention policy matches the Postgres job exactly.
func TestProposalStore_MarkAndDeleteStaleProposals_DeletesOnlyExpiredRows(t *testing.T) {
	s := openProposalStore(t, ":memory:", "")
	now := time.Now().UTC()

	resolved100Days := now.AddDate(0, 0, -100)    // outside 90d
	resolved10Days := now.AddDate(0, 0, -10)      // inside 90d
	pendingDecision200 := now.AddDate(0, 0, -200) // outside 180d
	pendingDecision30 := now.AddDate(0, 0, -30)   // inside 180d
	pendingGoal400 := now.AddDate(0, 0, -400)     // outside 180d but type=goal MUST be kept

	type seed struct {
		id         uuid.UUID
		shouldKeep bool
		label      string
	}
	var seeds []seed

	rt100 := resolved100Days
	id := importPruneSeed(t, s, "decision", proposalStatusAccepted, resolved100Days, &rt100)
	seeds = append(seeds, seed{id, false, "accepted decision >90d"})

	rt10 := resolved10Days
	id = importPruneSeed(t, s, "concept", proposalStatusRejected, resolved100Days, &rt100)
	seeds = append(seeds, seed{id, false, "rejected concept >90d"})

	id = importPruneSeed(t, s, "decision", proposalStatusAccepted, resolved10Days, &rt10)
	seeds = append(seeds, seed{id, true, "accepted decision <90d"})

	id = importPruneSeed(t, s, "decision", taskStatusPending, pendingDecision200, nil)
	seeds = append(seeds, seed{id, false, "pending decision >180d"})

	id = importPruneSeed(t, s, "decision", taskStatusPending, pendingDecision30, nil)
	seeds = append(seeds, seed{id, true, "pending decision <180d"})

	id = importPruneSeed(t, s, "goal", taskStatusPending, pendingGoal400, nil)
	seeds = append(seeds, seed{id, true, "pending goal >180d (user intent)"})

	id = importPruneSeed(t, s, "concept", taskStatusPending, pendingGoal400, nil)
	seeds = append(seeds, seed{id, true, "pending concept >180d (user intent)"})

	marked, deleted, err := s.MarkAndDeleteStaleProposals(
		context.Background(), 30*24*time.Hour, 180*24*time.Hour, 90*24*time.Hour, markReasonTTLExpired,
	)
	if err != nil {
		t.Fatalf("MarkAndDeleteStaleProposals: %v", err)
	}
	if marked != 0 {
		t.Errorf("expected 0 marked rows (no pending TypeTask seeded in this fixture), got %d", marked)
	}
	const wantDeleted = 3 // 2 resolved >90d + 1 pending decision >180d
	if deleted != wantDeleted {
		t.Errorf("deletedRows = %d, want %d", deleted, wantDeleted)
	}

	for _, sd := range seeds {
		got := rowExists(t, s, sd.id)
		if sd.shouldKeep && !got {
			t.Errorf("%s: row missing, want kept", sd.label)
		}
		if !sd.shouldKeep && got {
			t.Errorf("%s: row still present, want deleted", sd.label)
		}
	}
}

// TestProposalStore_MarkAndDeleteStaleProposals_TypeTaskTTL is the SQLite
// twin of TestRunDailyPendingProposalsPrune_TypeTaskTTL: pending TypeTask
// proposals older than 30 days are marked rejected/reason (NOT deleted —
// they age out through the resolved retention so the audit trail survives);
// fresh and already-resolved TypeTask rows are left untouched.
func TestProposalStore_MarkAndDeleteStaleProposals_TypeTaskTTL(t *testing.T) {
	s := openProposalStore(t, ":memory:", "")
	now := time.Now().UTC()

	freshID := importPruneSeed(t, s, "task", taskStatusPending, now.AddDate(0, 0, -1), nil)
	staleID := importPruneSeed(t, s, "task", taskStatusPending, now.AddDate(0, 0, -31), nil)
	staleAcceptedResolved := now.AddDate(0, 0, -29)
	staleAcceptedID := importPruneSeed(t, s, "task", proposalStatusAccepted, now.AddDate(0, 0, -31), &staleAcceptedResolved)

	marked, _, err := s.MarkAndDeleteStaleProposals(
		context.Background(), 30*24*time.Hour, 180*24*time.Hour, 90*24*time.Hour, markReasonTTLExpired,
	)
	if err != nil {
		t.Fatalf("MarkAndDeleteStaleProposals: %v", err)
	}
	if marked != 1 {
		t.Fatalf("markedRows = %d, want 1 (only staleID qualifies)", marked)
	}

	if got := rowStatus(t, s, staleID); got != proposalStatusRejected {
		t.Errorf("stale TypeTask: status = %q, want %q", got, proposalStatusRejected)
	}
	staleRow, err := s.Get(context.Background(), staleID)
	if err != nil {
		t.Fatalf("Get staleID: %v", err)
	}
	if !staleRow.Reason.Valid || staleRow.Reason.String != markReasonTTLExpired {
		t.Errorf("stale TypeTask: reason = %+v, want %q", staleRow.Reason, markReasonTTLExpired)
	}

	if got := rowStatus(t, s, freshID); got != taskStatusPending {
		t.Errorf("fresh TypeTask: status = %q, want %q (must not be touched)", got, taskStatusPending)
	}
	if got := rowStatus(t, s, staleAcceptedID); got != proposalStatusAccepted {
		t.Errorf("stale-accepted TypeTask: status = %q, want %q (already resolved, not re-touched)", got, proposalStatusAccepted)
	}
}

// TestProposalStore_MarkAndDeleteStaleProposals_EmptyTableNoPanic verifies
// the mark+delete pair is safe to run against an empty table — production
// may go days with no rows to touch. Regression guard for the "MUST have a
// working retention policy" requirement (backend-security-design.md §1.3):
// a panic here would take the whole scheduler job down, not just skip a
// no-op prune.
func TestProposalStore_MarkAndDeleteStaleProposals_EmptyTableNoPanic(t *testing.T) {
	s := openProposalStore(t, ":memory:", "")
	marked, deleted, err := s.MarkAndDeleteStaleProposals(
		context.Background(), 30*24*time.Hour, 180*24*time.Hour, 90*24*time.Hour, markReasonTTLExpired,
	)
	if err != nil {
		t.Fatalf("MarkAndDeleteStaleProposals on empty table: %v", err)
	}
	if marked != 0 || deleted != 0 {
		t.Errorf("expected 0/0 on empty table, got marked=%d deleted=%d", marked, deleted)
	}
}
