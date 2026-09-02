package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// ProposalStore is the SQLite-backed implementation of proposal.StoreIface.
type ProposalStore struct {
	db *DB
}

// NewProposalStore wraps an open DB into a ProposalStore.
func NewProposalStore(d *DB) *ProposalStore {
	return &ProposalStore{db: d}
}

var _ proposal.StoreIface = (*ProposalStore)(nil)

// DB returns the underlying *DB so callers in the mcp package can begin a
// cross-store transaction via DB().BeginTx without the ProposalStore needing
// to hold references to sibling stores.
func (s *ProposalStore) DB() *DB { return s.db }

const pendingProposalsSelectCols = `id, workspace_id, type, payload, status,
	proposed_by, created_at, resolved_at, reason`

func scanPendingProposal(scan func(...any) error) (db.PendingProposal, error) {
	var (
		p                         db.PendingProposal
		idStr                     string
		workspaceNS, proposedByNS sql.NullString
		reasonNS                  sql.NullString
		createdNS, resolvedNS     sql.NullString
	)
	err := scan(&idStr, &workspaceNS, &p.Type, &p.Payload, &p.Status,
		&proposedByNS, &createdNS, &resolvedNS, &reasonNS)
	if err != nil {
		return db.PendingProposal{}, err
	}
	if id, err := uuid.Parse(idStr); err == nil {
		p.ID = id
	}
	p.WorkspaceID = pgtypeUUID(nsString(workspaceNS))
	p.ProposedBy = pgtypeText(proposedByNS.String, proposedByNS.Valid)
	p.CreatedAt = parseTimestamptz(createdNS)
	p.ResolvedAt = parseTimestamptz(resolvedNS)
	p.Reason = pgtypeText(reasonNS.String, reasonNS.Valid)
	return p, nil
}

// Create records a new pending proposal.
//
// [F981-05] Enforces the same MaxPayloadBytes fail-closed size guard as the
// Postgres store (internal/proposal/store.go's Create) — this is a separate
// Create implementation for the dual-backend seam, not a wrapper around the
// Postgres one, so the write-time protection has to be applied here
// independently or the SQLite backend (the local-dev default, per
// storage.ResolveFromEnv) would remain exposed to the exact unbounded-
// payload gap this ticket closes on Postgres.
func (s *ProposalStore) Create(ctx context.Context, p proposal.CreateParams) (*db.PendingProposal, error) {
	if len(p.Payload) > proposal.MaxPayloadBytes {
		return nil, fmt.Errorf("creating proposal: payload %d bytes exceeds %d byte limit: %w",
			len(p.Payload), proposal.MaxPayloadBytes, proposal.ErrPayloadTooLarge)
	}
	workspaceID := s.db.workspaceArg()
	if p.WorkspaceID != nil {
		workspaceID = p.WorkspaceID.String()
	}
	id := uuid.New()
	const q = `INSERT INTO pending_proposals
		(id, workspace_id, type, payload, proposed_by, created_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6)`
	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(), workspaceID, string(p.Type), string(p.Payload),
		nullStringIfEmpty(p.ProposedBy), sqliteNowMillis())
	if err != nil {
		return nil, errWrap("CreateProposal", err)
	}
	return s.Get(ctx, id)
}

// ImportProposal inserts a pending_proposals row using p's own
// id/status/created_at/resolved_at instead of generating fresh ones, so a
// copy from production preserves resolution history (accepted/rejected
// proposals, not just pending ones). Used by cmd/qa-seed. Fails (no upsert)
// on a duplicate id — callers MUST import into a fresh database.
func (s *ProposalStore) ImportProposal(ctx context.Context, p db.PendingProposal) error {
	const q = `INSERT INTO pending_proposals
		(id, workspace_id, type, payload, status, proposed_by, created_at, resolved_at, reason)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)`
	_, err := s.db.conn.ExecContext(ctx, q,
		p.ID.String(), pgUUIDToNullString(p.WorkspaceID), p.Type, string(p.Payload), p.Status,
		pgTextToNullString(p.ProposedBy), pgTimestamptzToString(p.CreatedAt),
		pgTimestamptzToNullString(p.ResolvedAt), pgTextToNullString(p.Reason))
	if err != nil {
		return errWrap("ImportProposal", err)
	}
	return nil
}

// Get returns a single proposal by ID.
func (s *ProposalStore) Get(ctx context.Context, id uuid.UUID) (*db.PendingProposal, error) {
	const q = `SELECT ` + pendingProposalsSelectCols + ` FROM pending_proposals
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`
	p, err := scanPendingProposal(s.db.conn.QueryRowContext(ctx, q, id.String(), s.db.workspaceArg()).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, proposal.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("GetProposal", err)
	}
	return &p, nil
}

// ListAll returns all proposals of the given type regardless of status, newest
// first, up to limit rows. Status filtering is done in Go by the caller.
// Using parameterized query prevents SQL injection.
func (s *ProposalStore) ListAll(ctx context.Context, proposalType string, limit int32) ([]db.PendingProposal, error) {
	const q = `SELECT ` + pendingProposalsSelectCols + ` FROM pending_proposals
		WHERE type = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY created_at DESC, id DESC
		LIMIT ?3`
	rows, err := s.db.conn.QueryContext(ctx, q, proposalType, s.db.workspaceArg(), limit)
	if err != nil {
		return nil, errWrap("ListAllProposals", err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.PendingProposal
	for rows.Next() {
		p, err := scanPendingProposal(rows.Scan)
		if err != nil {
			return nil, errWrap("ListAllProposals scan", err)
		}
		out = append(out, p)
	}
	return out, errWrap("ListAllProposals iter", rows.Err())
}

// ListPending returns all pending proposals, newest first.
// [F170-06] — unbounded by contract; ListPendingPage is the capped variant.
func (s *ProposalStore) ListPending(ctx context.Context) ([]db.PendingProposal, error) {
	return s.ListPendingPage(ctx, db.UnboundedRowLimit, 0)
}

// ListPendingPage returns at most limit pending proposals starting at offset,
// newest first — [F170-06]. Mirrors proposal.Store.ListPendingPage (Postgres).
func (s *ProposalStore) ListPendingPage(ctx context.Context, limit, offset int32) ([]db.PendingProposal, error) {
	const q = `SELECT ` + pendingProposalsSelectCols + ` FROM pending_proposals
		WHERE status = 'pending'
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY created_at DESC, id DESC
		LIMIT ?2 OFFSET ?3`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg(),
		db.ClampRowLimit(limit), db.ClampRowOffset(offset))
	if err != nil {
		return nil, errWrap("ListPendingProposals", err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.PendingProposal
	for rows.Next() {
		p, err := scanPendingProposal(rows.Scan)
		if err != nil {
			return nil, errWrap("ListPendingProposals scan", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, errWrap("ListPendingProposals iter", err)
	}
	if out == nil {
		// list endpoints MUST return [] not null — MCP's list_pending_proposals
		// serializes this slice directly with no handler-level guard of its own.
		out = []db.PendingProposal{}
	}
	return out, nil
}

// Resolve marks a pending proposal as accepted or rejected.
func (s *ProposalStore) Resolve(ctx context.Context, id uuid.UUID, status proposal.Status) (*db.PendingProposal, error) {
	if status != proposal.StatusAccepted && status != proposal.StatusRejected {
		return nil, fmt.Errorf("resolve: invalid status %q (want accepted or rejected)", status)
	}
	now := sqliteNowMillis()
	const q = `UPDATE pending_proposals
		SET status = ?2, resolved_at = ?3
		WHERE id = ?1
		  AND status = 'pending'
		  AND (?4 IS NULL OR workspace_id = ?4)`
	res, err := s.db.conn.ExecContext(ctx, q, id.String(), string(status), now, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("ResolveProposal", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, proposal.ErrNotFound
	}
	return s.Get(ctx, id)
}

// ResolveTx marks a pending proposal as accepted or rejected within an
// existing *sql.Tx. Used by the confirm_proposal accept path to wrap
// proposal-resolve plus entity-materialise in a single atomic transaction.
func (s *ProposalStore) ResolveTx(ctx context.Context, tx *sql.Tx, id uuid.UUID, status proposal.Status) error {
	if status != proposal.StatusAccepted && status != proposal.StatusRejected {
		return fmt.Errorf("ResolveTx: invalid status %q (want accepted or rejected)", status)
	}
	now := sqliteNowMillis()
	const q = `UPDATE pending_proposals
		SET status = ?2, resolved_at = ?3
		WHERE id = ?1
		  AND status = 'pending'
		  AND (?4 IS NULL OR workspace_id = ?4)`
	res, err := tx.ExecContext(ctx, q, id.String(), string(status), now, s.db.workspaceArg())
	if err != nil {
		return errWrap("ResolveTx", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return proposal.ErrNotFound
	}
	return nil
}

// BatchConfirm resolves multiple proposals independently (best-effort). Each ID
// is processed in its own implicit SQLite transaction so a failure for one ID
// does not roll back the others. The caller is responsible for input validation
// (ids length 1–100, valid status).
func (s *ProposalStore) BatchConfirm(ctx context.Context, ids []uuid.UUID, status proposal.Status) (proposal.BatchConfirmResult, error) {
	if status != proposal.StatusAccepted && status != proposal.StatusRejected {
		return proposal.BatchConfirmResult{}, fmt.Errorf("batch confirm: invalid status %q", status)
	}
	results := make([]proposal.BatchItemResult, 0, len(ids))
	accepted, failed := 0, 0
	for _, id := range ids {
		_, err := s.Resolve(ctx, id, status)
		if err != nil {
			results = append(results, proposal.BatchItemResult{ID: id.String(), OK: false, ErrMsg: err.Error()})
			failed++
		} else {
			results = append(results, proposal.BatchItemResult{ID: id.String(), OK: true})
			accepted++
		}
	}
	return proposal.BatchConfirmResult{Results: results, Accepted: accepted, Failed: failed}, nil
}

// AutoProposeConceptFromKnowledge creates a pending concept proposal from a
// knowledge item when the item type is suitable for spaced repetition.
func (s *ProposalStore) AutoProposeConceptFromKnowledge(
	ctx context.Context, item *db.KnowledgeItem, proposedBy string,
) (*db.PendingProposal, error) {
	if !proposal.ShouldAutoProposeFor(item) {
		return nil, nil //nolint:nilnil // same sentinel behavior as the Postgres store
	}
	// [F0902-51] proposal.MarshalConceptCandidate instead of json.Marshal —
	// see its doc comment for why plain json.Marshal's default HTML-escaping
	// can silently inflate this payload past proposal.MaxPayloadBytes.
	payload, err := proposal.MarshalConceptCandidate(proposal.ConceptCandidate{
		Title:          item.Title,
		Content:        item.Content,
		Tags:           item.Tags,
		SourceItemID:   item.ID.String(),
		SourceItemType: item.Type,
	})
	if err != nil {
		return nil, fmt.Errorf("marshaling concept payload: %w", err)
	}
	return s.Create(ctx, proposal.CreateParams{
		Type:       proposal.TypeConcept,
		Payload:    payload,
		ProposedBy: proposedBy,
	})
}

// MarkAndDeleteStaleProposals is the SQLite-native counterpart to
// scheduler.go's Postgres two-step runDailyPendingProposalsPrune logic
// (internal/scheduler/scheduler.go): (1) mark pending type='task' proposals
// older than taskRetention as status='rejected' with reason markReason, then
// (2) delete resolved (accepted/rejected) rows older than resolvedRetention
// and pending type='decision' rows older than decisionRetention. Other
// pending types (goal/project/concept/knowledge/playbook) are NEVER touched
// by either step — same "unresolved user intent" boundary the Postgres job
// documents.
//
// SQLite has no `NOW() - INTERVAL` syntax (GTD decision G4, 6ea0b014 — same
// rationale as every other SQLite cognitive-job adapter in this package), so
// all three cutoffs are computed in Go from time.Now().UTC() and bound as
// parameters instead of interval literals.
//
// Deliberately NOT workspace-scoped, matching the Postgres job's actual
// behaviour (it prunes stale scheduler proposals across every workspace, not
// just one).
//
// A mark-step failure does NOT block the delete step (mirrors the Postgres
// job: "the mark step's outcome does not gate the delete step" — a
// transient mark failure shouldn't block the resolved-row cleanup). Returns
// (markedRows, deletedRows, err); err is errors.Join(markErr, delErr) so a
// caller can log both failures if both steps fail independently.
func (s *ProposalStore) MarkAndDeleteStaleProposals(
	ctx context.Context, taskRetention, decisionRetention, resolvedRetention time.Duration, markReason string,
) (markedRows, deletedRows int64, err error) {
	now := time.Now().UTC()
	taskCutoff := now.Add(-taskRetention).Format(sqliteMillisLayout)
	decisionCutoff := now.Add(-decisionRetention).Format(sqliteMillisLayout)
	resolvedCutoff := now.Add(-resolvedRetention).Format(sqliteMillisLayout)
	markedAt := sqliteNowMillis()

	const markQ = `UPDATE pending_proposals
		SET status = 'rejected', resolved_at = ?1, reason = ?2
		WHERE status = 'pending' AND type = 'task'
		  AND created_at < ?3`
	var markErr error
	markRes, markExecErr := s.db.conn.ExecContext(ctx, markQ, markedAt, markReason, taskCutoff)
	if markExecErr != nil {
		markErr = errWrap("MarkAndDeleteStaleProposals mark", markExecErr)
	} else if n, raErr := markRes.RowsAffected(); raErr != nil {
		markErr = errWrap("MarkAndDeleteStaleProposals mark rows affected", raErr)
	} else {
		markedRows = n
	}

	// resolved_at IS NULL on still-pending rows, so the first arm can never
	// match a pending row — same invariant the Postgres query's comment
	// documents.
	const deleteQ = `DELETE FROM pending_proposals
		WHERE (status IN ('accepted', 'rejected') AND resolved_at < ?1)
		   OR (status = 'pending' AND type = 'decision' AND created_at < ?2)`
	delRes, delExecErr := s.db.conn.ExecContext(ctx, deleteQ, resolvedCutoff, decisionCutoff)
	if delExecErr != nil {
		return markedRows, 0, errors.Join(markErr, errWrap("MarkAndDeleteStaleProposals delete", delExecErr))
	}
	n, raErr := delRes.RowsAffected()
	if raErr != nil {
		return markedRows, 0, errors.Join(markErr, errWrap("MarkAndDeleteStaleProposals delete rows affected", raErr))
	}
	deletedRows = n
	return markedRows, deletedRows, markErr
}
