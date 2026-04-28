package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

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

const pendingProposalsSelectCols = `id, workspace_id, type, payload, status,
	proposed_by, created_at, resolved_at`

func scanPendingProposal(scan func(...any) error) (db.PendingProposal, error) {
	var (
		p                         db.PendingProposal
		idStr                     string
		workspaceNS, proposedByNS sql.NullString
		createdNS, resolvedNS     sql.NullString
	)
	err := scan(&idStr, &workspaceNS, &p.Type, &p.Payload, &p.Status,
		&proposedByNS, &createdNS, &resolvedNS)
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
	return p, nil
}

// Create records a new pending proposal.
func (s *ProposalStore) Create(ctx context.Context, p proposal.CreateParams) (*db.PendingProposal, error) {
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

// ListPending returns all pending proposals, newest first.
func (s *ProposalStore) ListPending(ctx context.Context) ([]db.PendingProposal, error) {
	const q = `SELECT ` + pendingProposalsSelectCols + ` FROM pending_proposals
		WHERE status = 'pending'
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY created_at DESC, id DESC`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg())
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
	return out, errWrap("ListPendingProposals iter", rows.Err())
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

// AutoProposeConceptFromKnowledge creates a pending concept proposal from a
// knowledge item when the item type is suitable for spaced repetition.
func (s *ProposalStore) AutoProposeConceptFromKnowledge(
	ctx context.Context, item *db.KnowledgeItem, proposedBy string,
) (*db.PendingProposal, error) {
	if !proposal.ShouldAutoProposeFor(item) {
		return nil, nil //nolint:nilnil // same sentinel behavior as the Postgres store
	}
	payload, err := json.Marshal(proposal.ConceptCandidate{
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
