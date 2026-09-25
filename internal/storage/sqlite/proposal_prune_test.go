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
//
// As of F184-06/F184-07, goal/concept rows are no longer permanently exempt
// from every retention mechanism in the system — but MarkAndDeleteStaleProposals
// itself still never touches them (that logic lives in the separate
// MarkStaleGoalFamilyProposals method, called independently by the
// scheduler; see that method's own tests below). The status assertion added
// to the goal/concept cases here pins that boundary explicitly, not just
// row existence, so a future accidental merge of the two code paths would
// fail this test rather than pass it silently.
func TestProposalStore_MarkAndDeleteStaleProposals_DeletesOnlyExpiredRows(t *testing.T) {
	t.Parallel() // [F0925-10]
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
		wantStatus string // asserted only when shouldKeep is true
		label      string
	}
	var seeds []seed

	rt100 := resolved100Days
	id := importPruneSeed(t, s, "decision", proposalStatusAccepted, resolved100Days, &rt100)
	seeds = append(seeds, seed{id, false, "", "accepted decision >90d"})

	rt10 := resolved10Days
	id = importPruneSeed(t, s, "concept", proposalStatusRejected, resolved100Days, &rt100)
	seeds = append(seeds, seed{id, false, "", "rejected concept >90d"})

	id = importPruneSeed(t, s, "decision", proposalStatusAccepted, resolved10Days, &rt10)
	seeds = append(seeds, seed{id, true, proposalStatusAccepted, "accepted decision <90d"})

	id = importPruneSeed(t, s, "decision", taskStatusPending, pendingDecision200, nil)
	seeds = append(seeds, seed{id, false, "", "pending decision >180d"})

	id = importPruneSeed(t, s, "decision", taskStatusPending, pendingDecision30, nil)
	seeds = append(seeds, seed{id, true, taskStatusPending, "pending decision <180d"})

	id = importPruneSeed(t, s, "goal", taskStatusPending, pendingGoal400, nil)
	seeds = append(seeds, seed{
		id, true, taskStatusPending,
		"pending goal >180d (MarkAndDeleteStaleProposals never touches it — F184-06 lives in a separate method)",
	})

	id = importPruneSeed(t, s, "concept", taskStatusPending, pendingGoal400, nil)
	seeds = append(seeds, seed{
		id, true, taskStatusPending,
		"pending concept >180d (MarkAndDeleteStaleProposals never touches it — F184-06 lives in a separate method)",
	})

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
		if sd.shouldKeep && got {
			if gotStatus := rowStatus(t, s, sd.id); gotStatus != sd.wantStatus {
				t.Errorf("%s: status = %q, want %q (must not be silently written)", sd.label, gotStatus, sd.wantStatus)
			}
		}
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
	t.Parallel() // [F0925-10]
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
	t.Parallel() // [F0925-10]
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

// ---------------------------------------------------------------------------
// F184-06 / F184-07 — MarkStaleGoalFamilyProposals (goal/project/concept/
// knowledge/playbook 90-day TTL + its dry-run gate).
// ---------------------------------------------------------------------------

// markReasonGoalFamilyTTLExpired mirrors scheduler.pendingProposalsGoalFamilyTTLReason
// (F184-06). Local copy for the same reason markReasonTTLExpired above is:
// this test package can't import the scheduler package's unexported
// constant without an import cycle, and goconst wants ≥3 occurrences.
const markReasonGoalFamilyTTLExpired = "ttl-expired-90d"

// TestProposalStore_MarkStaleGoalFamilyProposals_DryRunTrue_CountOnly_NoWrites
// is F184-07's SQLite dry-run test: with dryRun=true passed explicitly,
// MarkStaleGoalFamilyProposals MUST perform zero writes — seeded stale rows
// of all 5 goal-family types stay exactly as seeded, and the returned count
// equals the number of matching rows.
func TestProposalStore_MarkStaleGoalFamilyProposals_DryRunTrue_CountOnly_NoWrites(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openProposalStore(t, ":memory:", "")
	now := time.Now().UTC()
	stale := now.AddDate(0, 0, -95)

	var ids []uuid.UUID
	for _, typ := range []string{"goal", "project", "concept", "knowledge", "playbook"} {
		ids = append(ids, importPruneSeed(t, s, typ, taskStatusPending, stale, nil))
	}

	rows, err := s.MarkStaleGoalFamilyProposals(context.Background(), 90*24*time.Hour, markReasonGoalFamilyTTLExpired, true)
	if err != nil {
		t.Fatalf("MarkStaleGoalFamilyProposals (dry-run): %v", err)
	}
	if rows != int64(len(ids)) {
		t.Errorf("rows = %d, want %d", rows, len(ids))
	}
	for _, id := range ids {
		if got := rowStatus(t, s, id); got != taskStatusPending {
			t.Errorf("id=%s: status = %q, want %q (dry-run must not write)", id, got, taskStatusPending)
		}
	}
}

// TestProposalStore_MarkStaleGoalFamilyProposals_DryRunFalse_MarksStaleRows
// is F184-06's SQLite mark test: with dryRun=false, stale (>90d) rows of all
// 5 goal-family types MUST be marked status='rejected',
// reason='ttl-expired-90d'; a fresh (<90d) row MUST stay pending.
func TestProposalStore_MarkStaleGoalFamilyProposals_DryRunFalse_MarksStaleRows(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openProposalStore(t, ":memory:", "")
	now := time.Now().UTC()
	stale := now.AddDate(0, 0, -95)
	fresh := now.AddDate(0, 0, -10)

	var staleIDs []uuid.UUID
	for _, typ := range []string{"goal", "project", "concept", "knowledge", "playbook"} {
		staleIDs = append(staleIDs, importPruneSeed(t, s, typ, taskStatusPending, stale, nil))
	}
	freshID := importPruneSeed(t, s, "goal", taskStatusPending, fresh, nil)

	rows, err := s.MarkStaleGoalFamilyProposals(context.Background(), 90*24*time.Hour, markReasonGoalFamilyTTLExpired, false)
	if err != nil {
		t.Fatalf("MarkStaleGoalFamilyProposals: %v", err)
	}
	if rows != int64(len(staleIDs)) {
		t.Fatalf("rows = %d, want %d", rows, len(staleIDs))
	}

	for _, id := range staleIDs {
		if got := rowStatus(t, s, id); got != proposalStatusRejected {
			t.Errorf("id=%s (stale): status = %q, want %q", id, got, proposalStatusRejected)
		}
		row, err := s.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if !row.Reason.Valid || row.Reason.String != markReasonGoalFamilyTTLExpired {
			t.Errorf("id=%s (stale): reason = %+v, want %q", id, row.Reason, markReasonGoalFamilyTTLExpired)
		}
	}
	if got := rowStatus(t, s, freshID); got != taskStatusPending {
		t.Errorf("freshID: status = %q, want %q (must not be touched)", got, taskStatusPending)
	}
}

// TestProposalStore_MarkStaleGoalFamilyProposals_DryRunCount_MatchesActualRows
// is F184-07's SQLite count-accuracy test: seeds 3 matching rows + 2
// non-matching (1 fresh goal-family row, 1 stale but excluded type='task'
// row), asserts the dry-run count is exactly 3, then asserts the real-mark
// count is also exactly 3 and only the 3 matching rows were touched.
func TestProposalStore_MarkStaleGoalFamilyProposals_DryRunCount_MatchesActualRows(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openProposalStore(t, ":memory:", "")
	now := time.Now().UTC()
	stale := now.AddDate(0, 0, -95)
	fresh := now.AddDate(0, 0, -10)

	matching := []uuid.UUID{
		importPruneSeed(t, s, "goal", taskStatusPending, stale, nil),
		importPruneSeed(t, s, "concept", taskStatusPending, stale, nil),
		importPruneSeed(t, s, "playbook", taskStatusPending, stale, nil),
	}
	nonMatching := []uuid.UUID{
		importPruneSeed(t, s, "project", taskStatusPending, fresh, nil),
		importPruneSeed(t, s, "task", taskStatusPending, stale, nil),
	}

	rows, err := s.MarkStaleGoalFamilyProposals(context.Background(), 90*24*time.Hour, markReasonGoalFamilyTTLExpired, true)
	if err != nil {
		t.Fatalf("MarkStaleGoalFamilyProposals (dry-run): %v", err)
	}
	if rows != 3 {
		t.Errorf("dry-run rows = %d, want 3", rows)
	}

	rows, err = s.MarkStaleGoalFamilyProposals(context.Background(), 90*24*time.Hour, markReasonGoalFamilyTTLExpired, false)
	if err != nil {
		t.Fatalf("MarkStaleGoalFamilyProposals (real): %v", err)
	}
	if rows != 3 {
		t.Errorf("real-mark rows = %d, want 3", rows)
	}
	for _, id := range matching {
		if got := rowStatus(t, s, id); got != proposalStatusRejected {
			t.Errorf("matching id=%s: status = %q, want %q", id, got, proposalStatusRejected)
		}
	}
	for _, id := range nonMatching {
		if got := rowStatus(t, s, id); got != taskStatusPending {
			t.Errorf("non-matching id=%s: status = %q, want %q (must never match)", id, got, taskStatusPending)
		}
	}
}

// TestProposalStore_MarkStaleGoalFamilyProposals_DoesNotTouchTaskOrDecision
// proves the new `type IN (goal,project,concept,knowledge,playbook)`
// predicate does not also match type='task' or type='decision' rows — both
// have their own narrower TTL that lives in MarkAndDeleteStaleProposals,
// not here.
func TestProposalStore_MarkStaleGoalFamilyProposals_DoesNotTouchTaskOrDecision(t *testing.T) {
	t.Parallel() // [F0925-10]
	s := openProposalStore(t, ":memory:", "")
	now := time.Now().UTC()
	stale := now.AddDate(0, 0, -95)

	taskID := importPruneSeed(t, s, "task", taskStatusPending, stale, nil)
	decisionID := importPruneSeed(t, s, "decision", taskStatusPending, stale, nil)

	rows, err := s.MarkStaleGoalFamilyProposals(context.Background(), 90*24*time.Hour, markReasonGoalFamilyTTLExpired, false)
	if err != nil {
		t.Fatalf("MarkStaleGoalFamilyProposals: %v", err)
	}
	if rows != 0 {
		t.Errorf("rows = %d, want 0 (task/decision must never match)", rows)
	}
	if got := rowStatus(t, s, taskID); got != taskStatusPending {
		t.Errorf("taskID: status = %q, want %q (its own 30d branch lives in MarkAndDeleteStaleProposals, not here)", got, taskStatusPending)
	}
	if got := rowStatus(t, s, decisionID); got != taskStatusPending {
		t.Errorf("decisionID: status = %q, want %q", got, taskStatusPending)
	}
}
