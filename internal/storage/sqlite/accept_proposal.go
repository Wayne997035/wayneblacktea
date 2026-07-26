package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// AcceptDeps groups the concrete SQLite store handles sqliteAcceptAdapter
// needs. Threaded in from a storage.ServerStores by internal/storage's
// AcceptSeam — see proposal.PgAcceptDeps's doc comment for why AcceptSeam
// itself lives in internal/storage rather than internal/proposal or here.
type AcceptDeps struct {
	Proposal  *ProposalStore
	GTD       *GTDStore
	Learning  *LearningStore
	Decision  *DecisionStore
	Knowledge *KnowledgeStore
}

// sqliteAcceptAdapter is the SQLite-backed proposal.AcceptAdapter used by
// internal/storage's AcceptSeam. Constructed fresh per Accept call via
// NewAcceptAdapter; holds the open *sql.Tx as state between the interface's
// method calls (see proposal.AcceptAdapter's doc comment — not safe for
// concurrent reuse across calls).
type sqliteAcceptAdapter struct {
	proposal.BaseAcceptAdapter
	id   uuid.UUID
	deps AcceptDeps
	tx   *sql.Tx
}

// NewAcceptAdapter constructs a fresh sqliteAcceptAdapter for one
// proposal.AcceptOrchestration(ctx, id, adapter) call.
func NewAcceptAdapter(id uuid.UUID, deps AcceptDeps) proposal.AcceptAdapter {
	return &sqliteAcceptAdapter{id: id, deps: deps}
}

func (a *sqliteAcceptAdapter) ReadPending(ctx context.Context) (*db.PendingProposal, error) {
	return a.deps.Proposal.Get(ctx, a.id)
}

// PrepareOutOfBand overrides BaseAcceptAdapter's no-op only for
// TypeKnowledge: it runs KnowledgeStore.Prepare (URL dedup only — SQLite has
// no embedding client wired up yet, see knowledge.PreparedItem.Vec's doc
// comment) before any transaction opens. The other 6 types fall through to
// the embedded BaseAcceptAdapter.
func (a *sqliteAcceptAdapter) PrepareOutOfBand(ctx context.Context, prop *db.PendingProposal) (any, error) {
	if proposal.Type(prop.Type) != proposal.TypeKnowledge {
		prepared, err := a.BaseAcceptAdapter.PrepareOutOfBand(ctx, prop)
		if err != nil {
			return nil, fmt.Errorf("%w", err)
		}
		return prepared, nil
	}
	kp, err := proposal.DecodeKnowledgePayload(prop.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	if a.deps.Knowledge == nil {
		return nil, fmt.Errorf("knowledge proposal materialisation requires SQLite knowledge store")
	}
	prep, err := a.deps.Knowledge.Prepare(ctx, knowledge.AddItemParams{
		Type:    string(knowledge.TypeTIL),
		Title:   kp.Title,
		Content: kp.Content,
		Tags:    kp.Tags,
		Source:  prop.ProposedBy.String,
	})
	if err != nil {
		return nil, fmt.Errorf("%w", err) // context added one level up by AcceptOrchestration; ErrDuplicate stays errors.Is-detectable
	}
	return prep, nil
}

func (a *sqliteAcceptAdapter) BeginTx(ctx context.Context) error {
	tx, err := a.deps.Proposal.DB().conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w", err) // context added one level up by AcceptOrchestration
	}
	a.tx = tx
	return nil
}

// Materialize ports the 4 already-tx-correct SQLite cases verbatim (same
// decode + store calls) from internal/mcp/tools_proposal.go's
// materializeFromPayloadSQLiteTx: TypeDecision, TypeGoal, TypeProject,
// TypeConcept. TypeKnowledge, TypeTask and TypePlaybook are not yet tx-wired
// on SQLite (the mcp-package original defers them to a post-commit,
// non-atomic step) — wiring them is the A1-seam task's job, not this one's.
func (a *sqliteAcceptAdapter) Materialize(ctx context.Context, prop *db.PendingProposal, _ any) (any, error) {
	switch proposal.Type(prop.Type) {
	case proposal.TypeDecision:
		return a.materializeDecision(ctx, prop)
	case proposal.TypeGoal:
		return a.materializeGoal(ctx, prop)
	case proposal.TypeProject:
		return a.materializeProject(ctx, prop)
	case proposal.TypeConcept:
		return a.materializeConcept(ctx, prop)
	case proposal.TypeKnowledge, proposal.TypeTask, proposal.TypePlaybook:
		return nil, fmt.Errorf("A1-seam TODO: %s materialize not yet tx-wired on sqlite", prop.Type)
	default:
		return nil, fmt.Errorf("unknown proposal type %q", prop.Type)
	}
}

func (a *sqliteAcceptAdapter) materializeDecision(ctx context.Context, prop *db.PendingProposal) (any, error) {
	dp, err := proposal.DecodeDecisionParams(prop.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	if a.deps.Decision == nil {
		return nil, fmt.Errorf("decision proposal materialisation requires SQLite decision store")
	}
	decisionID, err := a.deps.Decision.LogTx(ctx, a.tx, dp)
	if err != nil {
		return nil, fmt.Errorf("creating decision: %w", err)
	}
	return map[string]string{"id": decisionID.String(), "title": dp.Title}, nil
}

func (a *sqliteAcceptAdapter) materializeGoal(ctx context.Context, prop *db.PendingProposal) (any, error) {
	gp, err := proposal.DecodeGoalParams(prop.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	goalID, err := a.deps.GTD.CreateGoalTx(ctx, a.tx, gp)
	if err != nil {
		return nil, fmt.Errorf("creating goal: %w", err)
	}
	return map[string]string{"id": goalID.String(), "title": gp.Title}, nil
}

func (a *sqliteAcceptAdapter) materializeProject(ctx context.Context, prop *db.PendingProposal) (any, error) {
	pp, err := proposal.DecodeProjectParams(prop.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	projectID, err := a.deps.GTD.CreateProjectTx(ctx, a.tx, pp)
	if err != nil {
		return nil, fmt.Errorf("creating project: %w", err)
	}
	return map[string]string{"id": projectID.String(), "name": pp.Name, "title": pp.Title}, nil
}

func (a *sqliteAcceptAdapter) materializeConcept(ctx context.Context, prop *db.PendingProposal) (any, error) {
	cp, err := proposal.DecodeConceptPayload(prop.Payload)
	if err != nil {
		return nil, fmt.Errorf("%w", err)
	}
	conceptID, err := a.deps.Learning.CreateConceptTx(ctx, a.tx, cp.Title, cp.Content, cp.Tags)
	if err != nil {
		return nil, fmt.Errorf("creating concept: %w", err)
	}
	return map[string]string{"id": conceptID.String(), "title": cp.Title}, nil
}

func (a *sqliteAcceptAdapter) ResolveAccepted(ctx context.Context) (*db.PendingProposal, error) {
	if err := a.deps.Proposal.ResolveTx(ctx, a.tx, a.id, proposal.StatusAccepted); err != nil {
		return nil, fmt.Errorf("%w", err) // context added one level up by AcceptOrchestration
	}
	// ResolveTx (unlike the Postgres RETURNING-based Resolve) does not return
	// the updated row, and re-reading via a.deps.Proposal.Get here would
	// deadlock: SQLite serialises all access through a single pooled
	// connection (db.SetMaxOpenConns(1) in Open), and that connection is
	// still held open by a.tx — see the identical constraint documented on
	// sqliteBeginTaskAdapter.ResolveGuardBlocked in gtd.go. Read back via the
	// still-open tx instead.
	return a.getPendingTx(ctx)
}

// getPendingTx re-reads the proposal row via the still-open tx, mirroring
// ProposalStore.Get's query but scanning through tx instead of s.db.conn.
func (a *sqliteAcceptAdapter) getPendingTx(ctx context.Context) (*db.PendingProposal, error) {
	const q = `SELECT ` + pendingProposalsSelectCols + ` FROM pending_proposals
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`
	p, err := scanPendingProposal(a.tx.QueryRowContext(ctx, q, a.id.String(), a.deps.Proposal.db.workspaceArg()).Scan)
	if err != nil {
		return nil, fmt.Errorf("re-reading resolved proposal %s: %w", a.id, err)
	}
	return &p, nil
}

func (a *sqliteAcceptAdapter) Commit(context.Context) error {
	if err := a.tx.Commit(); err != nil {
		return fmt.Errorf("%w", err) // context added one level up by AcceptOrchestration
	}
	return nil
}

func (a *sqliteAcceptAdapter) Rollback(context.Context) {
	_ = a.tx.Rollback()
}
