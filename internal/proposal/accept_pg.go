package proposal

import (
	"context"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgAcceptDeps groups the concrete Postgres store handles pgAcceptAdapter
// needs. Threaded in from a storage.ServerStores by the caller
// (internal/storage/accept_seam.go's AcceptSeam) rather than passed as a
// storage.ServerStores value directly: internal/storage already imports
// internal/proposal (server_stores.go's Proposal() accessor / factory.go),
// so internal/proposal importing internal/storage back would cycle. Hence
// AcceptSeam itself lives in internal/storage, not here — see that file's
// doc comment for the full explanation; this is a deliberate deviation from
// the dispatch's literal `internal/proposal/accept_seam.go` file location.
type PgAcceptDeps struct {
	Pool      *pgxpool.Pool
	Proposal  *Store
	GTD       *gtd.Store
	Learning  *learning.Store
	Decision  *decision.Store
	Knowledge *knowledge.Store
}

// pgAcceptAdapter is the Postgres-backed AcceptAdapter used by
// internal/storage's AcceptSeam. Constructed fresh per Accept call via
// NewPgAcceptAdapter; holds the open tx as state between the interface's
// method calls (see AcceptAdapter's doc comment — not safe for concurrent
// reuse across calls).
type pgAcceptAdapter struct {
	BaseAcceptAdapter
	id   uuid.UUID
	deps PgAcceptDeps
	tx   pgx.Tx
}

// NewPgAcceptAdapter constructs a fresh pgAcceptAdapter for one
// AcceptOrchestration(ctx, id, adapter) call.
func NewPgAcceptAdapter(id uuid.UUID, deps PgAcceptDeps) AcceptAdapter {
	return &pgAcceptAdapter{id: id, deps: deps}
}

func (a *pgAcceptAdapter) ReadPending(ctx context.Context) (*db.PendingProposal, error) {
	return a.deps.Proposal.Get(ctx, a.id)
}

// PrepareOutOfBand overrides BaseAcceptAdapter's no-op only for
// TypeKnowledge: it runs knowledge.Store.Prepare (embedding generation +
// cosine-similarity dedup check — an external network call plus read-only
// DB queries) before any transaction opens. The other 6 types fall through
// to the embedded BaseAcceptAdapter.
func (a *pgAcceptAdapter) PrepareOutOfBand(ctx context.Context, prop *db.PendingProposal) (any, error) {
	if Type(prop.Type) != TypeKnowledge {
		return a.BaseAcceptAdapter.PrepareOutOfBand(ctx, prop)
	}
	kp, err := DecodeKnowledgePayload(prop.Payload)
	if err != nil {
		return nil, err
	}
	if a.deps.Knowledge == nil {
		return nil, fmt.Errorf("knowledge proposal materialisation requires PG knowledge store")
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

func (a *pgAcceptAdapter) BeginTx(ctx context.Context) error {
	tx, err := a.deps.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("%w", err) // context added one level up by AcceptOrchestration
	}
	a.tx = tx
	return nil
}

// Materialize ports the 5 already-tx-correct Postgres cases verbatim (same
// decode + store calls, same error strings) from
// internal/mcp/tools_proposal.go's materializeFromPayloadPg: TypeGoal,
// TypeProject, TypeConcept, TypeDecision, TypeTask. TypeKnowledge and
// TypePlaybook are not yet tx-wired on Postgres (see materializeKnowledgePg
// / playbook.Store.Create's non-tx Create in the mcp-package original) —
// wiring them is the A1-seam task's job, not this one's.
func (a *pgAcceptAdapter) Materialize(ctx context.Context, prop *db.PendingProposal, _ any) (any, error) {
	switch Type(prop.Type) {
	case TypeGoal:
		return a.materializeGoal(ctx, prop)
	case TypeProject:
		return a.materializeProject(ctx, prop)
	case TypeConcept:
		return a.materializeConcept(ctx, prop)
	case TypeDecision:
		return a.materializeDecision(ctx, prop)
	case TypeTask:
		return a.materializeTask(ctx, prop)
	case TypeKnowledge, TypePlaybook:
		return nil, fmt.Errorf("A1-seam TODO: %s materialize not yet tx-wired on postgres", prop.Type)
	default:
		return nil, fmt.Errorf("unknown proposal type %q", prop.Type)
	}
}

func (a *pgAcceptAdapter) materializeGoal(ctx context.Context, prop *db.PendingProposal) (any, error) {
	gp, err := DecodeGoalParams(prop.Payload)
	if err != nil {
		return nil, err
	}
	if a.deps.GTD == nil {
		return nil, fmt.Errorf("goal proposal materialisation requires PG gtd store")
	}
	goal, err := a.deps.GTD.WithTx(a.tx).CreateGoal(ctx, gp)
	if err != nil {
		return nil, fmt.Errorf("creating goal: %w", err)
	}
	return goal, nil
}

func (a *pgAcceptAdapter) materializeProject(ctx context.Context, prop *db.PendingProposal) (any, error) {
	pp, err := DecodeProjectParams(prop.Payload)
	if err != nil {
		return nil, err
	}
	if a.deps.GTD == nil {
		return nil, fmt.Errorf("project proposal materialisation requires PG gtd store")
	}
	project, err := a.deps.GTD.WithTx(a.tx).CreateProject(ctx, pp)
	if err != nil {
		return nil, fmt.Errorf("creating project: %w", err)
	}
	return project, nil
}

func (a *pgAcceptAdapter) materializeConcept(ctx context.Context, prop *db.PendingProposal) (any, error) {
	cp, err := DecodeConceptPayload(prop.Payload)
	if err != nil {
		return nil, err
	}
	if a.deps.Learning == nil {
		return nil, fmt.Errorf("concept proposal materialisation requires PG learning store")
	}
	concept, err := a.deps.Learning.WithTx(a.tx).CreateConcept(ctx, cp.Title, cp.Content, cp.Tags)
	if err != nil {
		return nil, fmt.Errorf("creating concept: %w", err)
	}
	return concept, nil
}

func (a *pgAcceptAdapter) materializeDecision(ctx context.Context, prop *db.PendingProposal) (any, error) {
	dp, err := DecodeDecisionParams(prop.Payload)
	if err != nil {
		return nil, err
	}
	if a.deps.Decision == nil {
		return nil, fmt.Errorf("decision proposal materialisation requires PG decision store")
	}
	dec, err := a.deps.Decision.WithTx(a.tx).Log(ctx, dp)
	if err != nil {
		return nil, fmt.Errorf("creating decision: %w", err)
	}
	return dec, nil
}

func (a *pgAcceptAdapter) materializeTask(ctx context.Context, prop *db.PendingProposal) (any, error) {
	params, warnings, err := DecodeTaskParams(prop.Payload, validator.StrictModeEnabled())
	if err != nil {
		return nil, err
	}
	if a.deps.GTD == nil {
		return nil, fmt.Errorf("task proposal materialisation requires PG gtd store")
	}
	task, err := a.deps.GTD.WithTx(a.tx).CreateTask(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("creating task: %w", err)
	}
	if len(warnings) > 0 {
		return map[string]any{"task": task, "warnings": warnings}, nil
	}
	return task, nil
}

func (a *pgAcceptAdapter) ResolveAccepted(ctx context.Context) (*db.PendingProposal, error) {
	resolved, err := a.deps.Proposal.WithTx(a.tx).Resolve(ctx, a.id, StatusAccepted)
	if err != nil {
		return nil, fmt.Errorf("%w", err) // context added one level up by AcceptOrchestration
	}
	return resolved, nil
}

func (a *pgAcceptAdapter) Commit(ctx context.Context) error {
	if err := a.tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w", err) // context added one level up by AcceptOrchestration
	}
	return nil
}

func (a *pgAcceptAdapter) Rollback(ctx context.Context) {
	_ = a.tx.Rollback(ctx)
}
