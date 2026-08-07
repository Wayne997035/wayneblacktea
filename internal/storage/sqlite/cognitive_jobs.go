package sqlite

import (
	"context"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
)

// CognitiveJobsStore is the SQLite-backed implementation of
// scheduler.CognitiveSQLiteStore — the narrow query surface backing the 4
// scheduler cognitive jobs (stuck_task_detection, decision_outcome_review,
// knowledge_to_skill_candidate, proposal_cleanup) whose Postgres
// implementation is raw SQL against the scheduler's own disciplinePool
// (internal/scheduler/cognitive_jobs.go). Kept in its own file — separate
// from GTDStore/DecisionStore/KnowledgeStore/ProposalStore — because these 4
// methods are scheduler-local plumbing, NOT part of
// gtd/decision/knowledge/proposal.StoreIface (backend-security-design.md
// domain-ownership rule). GTD decision G4 (6ea0b014): jobs whose proposals
// are user-observable get SQLite parity; jobs that only prune
// disk-growth-only observability tables stay Postgres-only (see
// internal/scheduler/scheduler.go's pgOnlyJobs capability contract — that's
// a DIFFERENT, deliberately-not-implemented set from the 4 methods here).
type CognitiveJobsStore struct {
	db *DB
}

// NewCognitiveJobsStore wraps an open DB into a CognitiveJobsStore.
func NewCognitiveJobsStore(d *DB) *CognitiveJobsStore {
	return &CognitiveJobsStore{db: d}
}

// NOTE: no `var _ scheduler.CognitiveSQLiteStore = (*CognitiveJobsStore)(nil)`
// compile-time assertion in THIS file — internal/scheduler transitively
// imports internal/storage (scheduler -> notion -> storage -> storage/sqlite),
// so importing scheduler from package sqlite would create an import cycle.
// The assertion instead lives in cognitive_jobs_test.go, which is
// `package sqlite_test` (external test package, like every other _test.go
// file in this directory) — a separate compiled package that can safely
// import both scheduler and sqlite without the cycle, since storage/sqlite
// never imports sqlite_test back. cmd/server/main.go's WithCognitiveDeps
// wiring call site is a second, redundant compile-time check on the
// production path.

// StuckTasks returns in_progress tasks whose updated_at is older than
// olderThan, scoped to the configured workspace and ordered oldest-first.
// Mirrors the Postgres query in cognitive_jobs.go's pgStuckTasks (job 2),
// including its dedup guard: a task already carrying a pending
// scheduler:stuck_task proposal (matched via
// json_extract(payload,'$.source_entity_id'), the SQLite twin of Postgres's
// payload->>'source_entity_id' — pending_proposals.payload is TEXT JSON in
// SQLite, not JSONB, so `->>` isn't available; modernc.org/sqlite ships
// JSON1 built in) is excluded so a follow-up run doesn't re-propose it.
// Reuses tasksSelectCols/scanTask (gtd.go) so the returned db.Task rows are
// identical in shape to every other SQLite task read.
func (s *CognitiveJobsStore) StuckTasks(ctx context.Context, olderThan time.Duration) ([]db.Task, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(sqliteMillisLayout)
	const q = `SELECT ` + tasksSelectCols + ` FROM tasks
		WHERE (?1 IS NULL OR workspace_id = ?1)
		  AND status = 'in_progress'
		  AND updated_at < ?2
		  AND NOT EXISTS (
		      SELECT 1 FROM pending_proposals p
		      WHERE p.type = 'task'
		        AND p.proposed_by = 'scheduler:stuck_task'
		        AND p.status = 'pending'
		        AND json_extract(p.payload, '$.source_entity_id') = tasks.id
		  )
		ORDER BY updated_at ASC`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg(), cutoff)
	if err != nil {
		return nil, errWrap("StuckTasks", err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.Task
	for rows.Next() {
		t, serr := scanTask(rows.Scan)
		if serr != nil {
			return nil, errWrap("StuckTasks scan", serr)
		}
		out = append(out, t)
	}
	return out, errWrap("StuckTasks iter", rows.Err())
}

// DecisionsPendingOutcomeReview returns decisions created before the
// olderThan cutoff that have no recorded outcome AND no existing pending
// scheduler:decision_outcome_review proposal (dedup — mirrors the Postgres
// NOT EXISTS guard added by the 2026-07-19 incident fix, see
// decisionOutcomeReviewDailyCap's doc comment in cognitive_jobs.go). Ordered
// oldest-first and capped at limit rows so consecutive daily runs drain a
// backlog forward instead of reprocessing the same head-of-queue rows.
// pending_proposals.payload is stored as TEXT JSON in SQLite (unlike
// Postgres's JSONB), so the dedup match uses json_extract instead of the
// Postgres `->>'source_entity_id'` operator — modernc.org/sqlite ships
// JSON1 built in.
func (s *CognitiveJobsStore) DecisionsPendingOutcomeReview(
	ctx context.Context, olderThan time.Duration, limit int,
) ([]db.Decision, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(sqliteMillisLayout)
	const q = `SELECT ` + decisionsSelectCols + ` FROM decisions d
		WHERE (?1 IS NULL OR d.workspace_id = ?1)
		  AND d.created_at < ?2
		  AND NOT EXISTS (
		      SELECT 1 FROM outcomes o
		      WHERE o.entity_type = 'decision' AND o.entity_id = d.id
		  )
		  AND NOT EXISTS (
		      SELECT 1 FROM pending_proposals p
		      WHERE p.type = 'task'
		        AND p.proposed_by = 'scheduler:decision_outcome_review'
		        AND p.status = 'pending'
		        AND json_extract(p.payload, '$.source_entity_id') = d.id
		  )
		ORDER BY d.created_at ASC
		LIMIT ?3`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg(), cutoff, limit)
	if err != nil {
		return nil, errWrap("DecisionsPendingOutcomeReview", err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.Decision
	for rows.Next() {
		d, serr := scanDecision(rows.Scan)
		if serr != nil {
			return nil, errWrap("DecisionsPendingOutcomeReview scan", serr)
		}
		out = append(out, d)
	}
	return out, errWrap("DecisionsPendingOutcomeReview iter", rows.Err())
}

// HighRecallKnowledgeItems returns non-archived knowledge items whose
// recall_count exceeds minRecallCount, scoped to the configured workspace,
// ordered by recall_count descending. Mirrors the Postgres query in
// cognitive_jobs.go's pgHighRecallKnowledgeItems (job 4), including its
// dedup guard: an item already carrying a pending scheduler:knowledge_to_skill
// proposal (matched via json_extract, same SQLite/JSONB dialect split
// documented on StuckTasks above) is excluded. Reuses
// knowledgeSelectCols/scanKnowledgeItem (knowledge.go).
func (s *CognitiveJobsStore) HighRecallKnowledgeItems(ctx context.Context, minRecallCount int) ([]db.KnowledgeItem, error) {
	const q = `SELECT ` + knowledgeSelectCols + ` FROM knowledge_items
		WHERE (?1 IS NULL OR workspace_id = ?1)
		  AND recall_count > ?2
		  AND archived_at IS NULL
		  AND NOT EXISTS (
		      SELECT 1 FROM pending_proposals p
		      WHERE p.type = 'knowledge'
		        AND p.proposed_by = 'scheduler:knowledge_to_skill'
		        AND p.status = 'pending'
		        AND json_extract(p.payload, '$.source_entity_id') = knowledge_items.id
		  )
		ORDER BY recall_count DESC`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg(), minRecallCount)
	if err != nil {
		return nil, errWrap("HighRecallKnowledgeItems", err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.KnowledgeItem
	for rows.Next() {
		item, serr := scanKnowledgeItem(rows.Scan)
		if serr != nil {
			return nil, errWrap("HighRecallKnowledgeItems scan", serr)
		}
		out = append(out, item)
	}
	return out, errWrap("HighRecallKnowledgeItems iter", rows.Err())
}

// ExpireStaleScheduledProposals marks scheduler-originated pending proposals
// older than olderThan as rejected with reason, returning the number of rows
// affected. This is a SQLite-native reimplementation of the retention
// arithmetic — NOT a call-through to any Postgres code path — per GTD
// decision G4 (6ea0b014): SQLite has no `NOW() - INTERVAL` syntax, so the
// cutoff is computed in Go and bound as a parameter instead of the Postgres
// interval literal. Deliberately NOT workspace-scoped, matching the Postgres
// job's actual behaviour (it expires stale scheduler proposals across every
// workspace, not just one — see cognitive_jobs.go runProposalCleanup's PG
// branch). The proposed_by LIKE 'scheduler:%' guard is the critical safety
// boundary and MUST NOT be widened to touch user-submitted proposals
// (mirrors the Postgres comment).
func (s *CognitiveJobsStore) ExpireStaleScheduledProposals(
	ctx context.Context, olderThan time.Duration, reason string,
) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(sqliteMillisLayout)
	now := sqliteNowMillis()
	const q = `UPDATE pending_proposals
		SET status = 'rejected', resolved_at = ?1, reason = ?2
		WHERE proposed_by LIKE 'scheduler:%'
		  AND status = 'pending'
		  AND created_at < ?3`
	res, err := s.db.conn.ExecContext(ctx, q, now, reason, cutoff)
	if err != nil {
		return 0, errWrap("ExpireStaleScheduledProposals", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, errWrap("ExpireStaleScheduledProposals rows affected", err)
	}
	return n, nil
}
