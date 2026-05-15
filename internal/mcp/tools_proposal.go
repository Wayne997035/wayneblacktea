package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/playbook"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const errTaskProposalNotMaterialized = "task proposals are not materialized via confirm_proposal in Phase B1; use add_task directly"

// goalPayload is the JSONB shape stored in pending_proposals when type=goal.
type goalPayload struct {
	Title       string `json:"title"`
	Area        string `json:"area"`
	Description string `json:"description,omitempty"`
	DueDate     string `json:"due_date,omitempty"` // RFC3339; empty → no due date
}

// projectPayload is the JSONB shape stored in pending_proposals when type=project.
type projectPayload struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Area        string `json:"area"`
	Description string `json:"description,omitempty"`
	GoalID      string `json:"goal_id,omitempty"` // UUID string
	Priority    int32  `json:"priority,omitempty"`
}

// conceptPayload is the JSONB shape stored when add_knowledge auto-proposes
// a spaced-repetition concept card.
type conceptPayload struct {
	Title          string   `json:"title"`
	Content        string   `json:"content"`
	Tags           []string `json:"tags,omitempty"`
	SourceItemID   string   `json:"source_item_id,omitempty"`   // knowledge_items.id that triggered the proposal
	SourceItemType string   `json:"source_item_type,omitempty"` // "article" / "til" / etc.
}

const (
	maxBatchConfirmIDs = 100
	minBatchConfirmIDs = 1

	actionAccept = "accept"
	actionReject = "reject"
)

func (s *Server) registerProposalTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("confirm_proposals",
		mcp.WithDescription(
			"Batch accept or reject multiple pending proposals in a single call. "+
				"Accepts 1–100 proposal UUIDs and a single action ('accept' or 'reject'). "+
				"On Postgres the batch is atomic — any single failure rolls back all. "+
				"On SQLite each proposal is processed independently (best-effort).",
		),
		mcp.WithArray("ids", mcp.Description("Array of proposal UUID strings to resolve (1–100)"), mcp.Required()),
		mcp.WithString("action", mcp.Description("'accept' or 'reject'"), mcp.Required()),
	), s.handleConfirmProposals)

	ms.AddTool(mcp.NewTool("propose_goal",
		mcp.WithDescription(
			"Propose a new goal for user confirmation. Stays pending until confirm_proposal "+
				"is called with action='accept'. Use this when an agent suggests a goal "+
				"rather than creating one directly (proposal gate).",
		),
		mcp.WithString("title", mcp.Description("Goal title"), mcp.Required()),
		mcp.WithString("area", mcp.Description("Life area (e.g. career, health, personal)"), mcp.Required()),
		mcp.WithString("description", mcp.Description("Detailed description")),
		mcp.WithString("due_date", mcp.Description("Target date in RFC3339 (e.g. 2026-12-31T00:00:00Z)")),
		mcp.WithString("proposed_by", mcp.Description("Agent identity (e.g. claude-code, discord-bot)")),
	), s.handleProposeGoal)

	ms.AddTool(mcp.NewTool("propose_project",
		mcp.WithDescription(
			"Propose a new project for user confirmation. Stays pending until confirm_proposal "+
				"is called with action='accept'.",
		),
		mcp.WithString("name", mcp.Description("Short slug identifier"), mcp.Required()),
		mcp.WithString("title", mcp.Description("Display title"), mcp.Required()),
		mcp.WithString("area", mcp.Description("Work area (e.g. engineering, personal)"), mcp.Required()),
		mcp.WithString("description", mcp.Description("Detailed description")),
		mcp.WithString("goal_id", mcp.Description("Parent goal UUID")),
		mcp.WithNumber("priority", mcp.Description("Priority 1-5, lower is higher")),
		mcp.WithString("proposed_by", mcp.Description("Agent identity")),
	), s.handleProposeProject)

	ms.AddTool(mcp.NewTool("list_pending_proposals",
		mcp.WithDescription("Lists all proposals awaiting user resolution, newest first."),
	), s.handleListPendingProposals)

	ms.AddTool(mcp.NewTool("confirm_proposal",
		mcp.WithDescription(
			"Resolves a pending proposal. action='accept' materializes the entity (goal/project/concept) "+
				"atomically; action='reject' marks the proposal rejected without materializing.",
		),
		mcp.WithString("proposal_id", mcp.Description("Proposal UUID"), mcp.Required()),
		mcp.WithString("action", mcp.Description("'accept' or 'reject'"), mcp.Required()),
	), s.handleConfirmProposal)
}

func (s *Server) handleProposeGoal(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	title, area := stringArg(args, "title"), stringArg(args, "area")
	if title == "" || area == "" {
		return mcp.NewToolResultError("title and area are required"), nil
	}

	dueDate := stringArg(args, "due_date")
	if dueDate != "" {
		if _, err := time.Parse(time.RFC3339, dueDate); err != nil {
			return mcp.NewToolResultError("invalid due_date: must be RFC3339"), nil
		}
	}

	payload, err := json.Marshal(goalPayload{
		Title:       title,
		Area:        area,
		Description: stringArg(args, "description"),
		DueDate:     dueDate,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding payload: %v", err)), nil
	}

	row, err := s.proposal.Create(ctx, proposal.CreateParams{
		Type:       proposal.TypeGoal,
		Payload:    payload,
		ProposedBy: stringArg(args, "proposed_by"),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating proposal: %v", err)), nil
	}
	return jsonText(row)
}

func (s *Server) handleProposeProject(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	name, title, area := stringArg(args, "name"), stringArg(args, "title"), stringArg(args, "area")
	if name == "" || title == "" || area == "" {
		return mcp.NewToolResultError("name, title and area are required"), nil
	}

	priority := numberArg(args, "priority")
	if priority != 0 && (priority < 1 || priority > 5) {
		return mcp.NewToolResultError("priority must be between 1 and 5"), nil
	}

	goalID := stringArg(args, "goal_id")
	if goalID != "" {
		if _, err := uuid.Parse(goalID); err != nil {
			return mcp.NewToolResultError("invalid goal_id UUID"), nil
		}
	}

	payload, err := json.Marshal(projectPayload{
		Name:        name,
		Title:       title,
		Area:        area,
		Description: stringArg(args, "description"),
		GoalID:      goalID,
		Priority:    priority,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("encoding payload: %v", err)), nil
	}

	row, err := s.proposal.Create(ctx, proposal.CreateParams{
		Type:       proposal.TypeProject,
		Payload:    payload,
		ProposedBy: stringArg(args, "proposed_by"),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating proposal: %v", err)), nil
	}
	return jsonText(row)
}

func (s *Server) handleListPendingProposals(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rows, err := s.proposal.ListPending(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("listing pending proposals: %v", err)), nil
	}
	return jsonText(rows)
}

func (s *Server) handleConfirmProposals(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	action := stringArg(args, "action")
	if action != actionAccept && action != actionReject {
		return mcp.NewToolResultError("action must be 'accept' or 'reject'"), nil
	}

	rawIDs, _ := args["ids"].([]any)
	if len(rawIDs) < minBatchConfirmIDs {
		return mcp.NewToolResultError("ids must contain at least 1 proposal UUID"), nil
	}
	if len(rawIDs) > maxBatchConfirmIDs {
		return mcp.NewToolResultError(fmt.Sprintf("ids must contain at most %d proposal UUIDs", maxBatchConfirmIDs)), nil
	}

	ids := make([]uuid.UUID, 0, len(rawIDs))
	for i, raw := range rawIDs {
		rawStr, ok := raw.(string)
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("ids[%d] is not a string", i)), nil
		}
		id, err := uuid.Parse(rawStr)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("ids[%d] %q is not a valid UUID", i, rawStr)), nil
		}
		ids = append(ids, id)
	}

	if action == actionAccept {
		// Accept path MUST go through acceptProposal so TypeDecision rows are
		// materialised into the decisions table (same data-loss class as C-1
		// from round-1: BatchConfirm flips status='accepted' but never inserts
		// the decisions row). Per-id dispatch preserves uniform behaviour
		// with the singular handleConfirmProposal flow at the cost of one tx
		// per id instead of one tx per batch — acceptable for personal-OS
		// scale (max 100 ids).
		return s.batchAccept(ctx, ids)
	}

	// Reject path stays on BatchConfirm — no materialiser to run.
	result, err := s.proposal.BatchConfirm(ctx, ids, proposal.StatusRejected)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("batch confirm: %v", err)), nil
	}
	return jsonText(result)
}

// batchAccept runs acceptProposal once per id and aggregates the per-id
// outcomes into a BatchConfirmResult shape. Errors from individual ids are
// captured in the result entry (not fatal to the batch); only a result-level
// failure (e.g. JSON encoding the summary) propagates as a tool error.
func (s *Server) batchAccept(ctx context.Context, ids []uuid.UUID) (*mcp.CallToolResult, error) {
	results := make([]proposal.BatchItemResult, 0, len(ids))
	accepted, failed := 0, 0
	for _, id := range ids {
		res, err := s.acceptProposal(ctx, id)
		if err != nil {
			results = append(results, proposal.BatchItemResult{ID: id.String(), OK: false, ErrMsg: err.Error()})
			failed++
			continue
		}
		if res.IsError {
			results = append(results, proposal.BatchItemResult{
				ID:     id.String(),
				OK:     false,
				ErrMsg: resultErrorText(res),
			})
			failed++
			continue
		}
		results = append(results, proposal.BatchItemResult{ID: id.String(), OK: true})
		accepted++
	}
	return jsonText(proposal.BatchConfirmResult{
		Results:  results,
		Accepted: accepted,
		Failed:   failed,
	})
}

// resultErrorText extracts the first text content from an error CallToolResult
// for inclusion in a batch-aggregate error message. Empty if the result has no
// text content (defensive — acceptProposal always includes a message).
func resultErrorText(r *mcp.CallToolResult) string {
	for _, c := range r.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	return "unknown error"
}

// confirmResult is what confirm_proposal returns when an entity is materialized.
type confirmResult struct {
	Proposal any `json:"proposal"`
	Created  any `json:"created,omitempty"` // the materialized entity (goal/project/concept), nil for rejection
}

func (s *Server) handleConfirmProposal(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	rawID, action := stringArg(args, "proposal_id"), stringArg(args, "action")
	if rawID == "" || action == "" {
		return mcp.NewToolResultError("proposal_id and action are required"), nil
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		return mcp.NewToolResultError("invalid proposal_id UUID"), nil
	}

	switch action {
	case actionReject:
		row, err := s.proposal.Resolve(ctx, id, proposal.StatusRejected)
		if errors.Is(err, proposal.ErrNotFound) {
			return mcp.NewToolResultError("proposal not found or already resolved"), nil
		}
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("rejecting: %v", err)), nil
		}
		return jsonText(confirmResult{Proposal: row})
	case actionAccept:
		return s.acceptProposal(ctx, id)
	default:
		return mcp.NewToolResultError("action must be 'accept' or 'reject'"), nil
	}
}

func (s *Server) acceptProposal(ctx context.Context, id uuid.UUID) (*mcp.CallToolResult, error) {
	if s.pool != nil {
		return s.acceptProposalPg(ctx, id)
	}
	if s.sqliteProposal != nil {
		return s.acceptProposalSQLite(ctx, id)
	}
	return s.acceptProposalSequential(ctx, id)
}

// acceptProposalPg runs the materialise + resolve sequence inside a single
// pgx.Tx so partial failures don't leave a half-materialised proposal in the
// accepted state. Postgres-only path.
func (s *Server) acceptProposalPg(ctx context.Context, id uuid.UUID) (*mcp.CallToolResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("beginning tx: %v", err)), nil
	}
	defer func() { _ = tx.Rollback(ctx) }() // safe: no-op if already committed

	prop, err := s.pgProposal.WithTx(tx).Get(ctx, id)
	if errors.Is(err, proposal.ErrNotFound) {
		return mcp.NewToolResultError("proposal not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("fetching proposal: %v", err)), nil
	}
	if prop.Status != string(proposal.StatusPending) {
		return mcp.NewToolResultError(fmt.Sprintf("proposal already %s", prop.Status)), nil
	}

	created, errMsg := s.materializeFromPayloadPg(ctx, tx, prop)
	if errMsg != "" {
		_ = tx.Rollback(ctx)
		return mcp.NewToolResultError(errMsg), nil
	}

	resolved, err := s.pgProposal.WithTx(tx).Resolve(ctx, id, proposal.StatusAccepted)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolving proposal: %v", err)), nil
	}
	if err := tx.Commit(ctx); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("committing: %v", err)), nil
	}
	return jsonText(confirmResult{Proposal: resolved, Created: created})
}

// acceptProposalSequential is the SQLite-backed best-effort path. modernc.org/
// sqlite supports transactions but the StoreIface does not expose a WithTx
// hook for either backend, so this path materialises the entity first and
// then resolves the proposal as two separate writes. If the second write
// fails the entity is created but the proposal stays pending — callers
// re-invoke confirm_proposal which will reject due to the duplicate. The
// audit-trail compromise is acceptable for the friend-grade self-host path
// and is documented in ServerStores doc.
func (s *Server) acceptProposalSequential(ctx context.Context, id uuid.UUID) (*mcp.CallToolResult, error) {
	prop, err := s.proposal.Get(ctx, id)
	if errors.Is(err, proposal.ErrNotFound) {
		return mcp.NewToolResultError("proposal not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("fetching proposal: %v", err)), nil
	}
	if prop.Status != string(proposal.StatusPending) {
		return mcp.NewToolResultError(fmt.Sprintf("proposal already %s", prop.Status)), nil
	}

	created, errMsg := s.materializeFromPayloadIface(ctx, prop)
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}

	resolved, err := s.proposal.Resolve(ctx, id, proposal.StatusAccepted)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolving proposal (entity already created): %v", err)), nil
	}
	return jsonText(confirmResult{Proposal: resolved, Created: created})
}

// acceptProposalSQLite runs the materialise + resolve sequence inside a single
// *sql.Tx begun on the shared SQLite connection. This eliminates the race
// condition in acceptProposalSequential where two goroutines calling accept on
// the same proposal can each see status='pending' and materialise the entity,
// resulting in a duplicate with one proposal remaining pending.
//
// With this path:
//   - The entity is written inside the tx.
//   - ResolveTx atomically flips the proposal to accepted (WHERE status='pending').
//   - If a concurrent goroutine already committed first, ResolveTx returns
//     ErrNotFound and the whole tx is rolled back — no orphan entity is left.
func (s *Server) acceptProposalSQLite(ctx context.Context, id uuid.UUID) (*mcp.CallToolResult, error) {
	prop, err := s.proposal.Get(ctx, id)
	if errors.Is(err, proposal.ErrNotFound) {
		return mcp.NewToolResultError("proposal not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("fetching proposal: %v", err)), nil
	}
	if prop.Status != string(proposal.StatusPending) {
		return mcp.NewToolResultError(fmt.Sprintf("proposal already %s", prop.Status)), nil
	}

	tx, err := s.sqliteProposal.DB().BeginTx(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("beginning transaction: %v", err)), nil
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit

	created, errMsg := s.materializeFromPayloadSQLiteTx(ctx, tx, prop)
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}

	if err := s.sqliteProposal.ResolveTx(ctx, tx, id, proposal.StatusAccepted); err != nil {
		if errors.Is(err, proposal.ErrNotFound) {
			return mcp.NewToolResultError("proposal already resolved by concurrent request"), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("resolving proposal: %v", err)), nil
	}

	if err := tx.Commit(); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("committing transaction: %v", err)), nil
	}

	// Post-commit: for TypeKnowledge the tx path only validated the payload
	// and flipped the proposal status. The actual AddItem write must happen
	// here — outside the tx — because knowledge.Store has no WithTx variant
	// and SQLite is single-writer (calling AddItem inside the tx would deadlock).
	if proposal.Type(prop.Type) == proposal.TypeKnowledge && created == nil {
		postCreated, errMsg := s.materializeKnowledgeIface(ctx, prop)
		if errMsg == "" {
			created = postCreated
		} else {
			slog.Warn("acceptProposalSQLite: knowledge post-commit materialise failed",
				"proposal_id", id, "err", errMsg)
		}
	}

	// Fetch the resolved proposal for the response (committed tx is closed).
	resolved, err := s.proposal.Get(ctx, id)
	if err != nil {
		// Commit succeeded — return partial result rather than an error.
		return jsonText(confirmResult{Created: created})
	}
	return jsonText(confirmResult{Proposal: resolved, Created: created})
}

// proposalWorkspaceID safely unpacks the optional workspace UUID from a
// pending_proposals row.
func proposalWorkspaceID(prop *db.PendingProposal) *uuid.UUID {
	if !prop.WorkspaceID.Valid {
		return nil
	}
	id := uuid.UUID(prop.WorkspaceID.Bytes)
	return &id
}

// materializeFromPayloadSQLiteTx creates the concrete entity for a proposal
// inside the provided *sql.Tx, using the Tx-aware store methods. Returns the
// created entity (or nil for types with no materialisation) plus an error
// message string (empty = success). All writes go through the open tx so the
// caller can atomically commit entity + proposal-resolve in one transaction.
//
//nolint:gocyclo // each case dispatches to an extracted sub-function; switch over proposal.Type is inherently branchy
func (s *Server) materializeFromPayloadSQLiteTx(ctx context.Context, tx *sql.Tx, prop *db.PendingProposal) (any, string) {
	switch proposal.Type(prop.Type) {
	case proposal.TypeDecision:
		return s.materializeDecisionSQLite(ctx, tx, prop)
	case proposal.TypeGoal:
		gp, errMsg := decodeGoalParams(prop.Payload)
		if errMsg != "" {
			return nil, errMsg
		}
		goalID, err := s.sqliteGTD.CreateGoalTx(ctx, tx, gp)
		if err != nil {
			return nil, fmt.Sprintf("creating goal: %v", err)
		}
		return map[string]string{"id": goalID.String(), "title": gp.Title}, ""
	case proposal.TypeProject:
		pp, errMsg := decodeProjectParams(prop.Payload)
		if errMsg != "" {
			return nil, errMsg
		}
		projectID, err := s.sqliteGTD.CreateProjectTx(ctx, tx, pp)
		if err != nil {
			return nil, fmt.Sprintf("creating project: %v", err)
		}
		return map[string]string{"id": projectID.String(), "name": pp.Name, "title": pp.Title}, ""
	case proposal.TypeConcept:
		cp, errMsg := decodeConceptPayload(prop.Payload)
		if errMsg != "" {
			return nil, errMsg
		}
		conceptID, err := s.sqliteLearning.CreateConceptTx(ctx, tx, cp.Title, cp.Content, cp.Tags)
		if err != nil {
			return nil, fmt.Sprintf("creating concept: %v", err)
		}
		return map[string]string{"id": conceptID.String(), "title": cp.Title}, ""
	case proposal.TypeKnowledge:
		// Validate payload inside the tx so a bad payload aborts cleanly.
		// Actual AddItem write happens after tx.Commit (post-commit path in
		// acceptProposalSQLite) because knowledge.Store does not expose a
		// tx-aware variant and SQLite is single-writer — calling AddItem with
		// the same db.conn while a tx is open would deadlock. Per A3 the
		// sequential fallback is acceptable at personal-OS scale.
		if _, errMsg := decodeKnowledgePayload(prop.Payload); errMsg != "" {
			return nil, errMsg
		}
		return nil, "" // signals tx can commit; post-commit path does AddItem
	case proposal.TypeTask:
		return nil, errTaskProposalNotMaterialized
	case proposal.TypePlaybook:
		cp, errMsg := decodePlaybookPayload(prop.Payload, proposalWorkspaceID(prop))
		if errMsg != "" {
			return nil, errMsg
		}
		pb, err := s.playbook.Create(ctx, cp)
		if err != nil {
			return nil, fmt.Sprintf("creating playbook: %v", err)
		}
		return pb, ""
	default:
		return nil, fmt.Sprintf("unknown proposal type %q", prop.Type)
	}
}

// materializeDecisionSQLite is the per-type SQLite-Tx materialiser for
// TypeDecision. Extracted from the switch to keep gocyclo low; the decision
// path is the only one that requires the dedicated *DecisionStore handle.
func (s *Server) materializeDecisionSQLite(ctx context.Context, tx *sql.Tx, prop *db.PendingProposal) (any, string) {
	dp, errMsg := decodeDecisionParams(prop.Payload)
	if errMsg != "" {
		return nil, errMsg
	}
	if s.sqliteDecision == nil {
		return nil, "decision proposal materialisation requires SQLite decision store"
	}
	decisionID, err := s.sqliteDecision.LogTx(ctx, tx, dp)
	if err != nil {
		return nil, fmt.Sprintf("creating decision: %v", err)
	}
	return map[string]string{"id": decisionID.String(), "title": dp.Title}, ""
}

// materializeFromPayloadPg decodes the proposal's payload and creates the
// concrete entity inside the given pgx transaction. Returns the created
// entity or an error message string (empty = success). Postgres-only.
//
//nolint:gocyclo // each case dispatches to an extracted sub-function; switch over proposal.Type is inherently branchy
func (s *Server) materializeFromPayloadPg(ctx context.Context, tx pgx.Tx, prop *db.PendingProposal) (any, string) {
	switch proposal.Type(prop.Type) {
	case proposal.TypeDecision:
		return s.materializeDecisionPg(ctx, tx, prop)
	case proposal.TypeGoal:
		gp, errMsg := decodeGoalParams(prop.Payload)
		if errMsg != "" {
			return nil, errMsg
		}
		goal, err := s.pgGTD.WithTx(tx).CreateGoal(ctx, gp)
		if err != nil {
			return nil, fmt.Sprintf("creating goal: %v", err)
		}
		return goal, ""
	case proposal.TypeProject:
		pp, errMsg := decodeProjectParams(prop.Payload)
		if errMsg != "" {
			return nil, errMsg
		}
		project, err := s.pgGTD.WithTx(tx).CreateProject(ctx, pp)
		if err != nil {
			return nil, fmt.Sprintf("creating project: %v", err)
		}
		return project, ""
	case proposal.TypeConcept:
		cp, errMsg := decodeConceptPayload(prop.Payload)
		if errMsg != "" {
			return nil, errMsg
		}
		concept, err := s.pgLearning.WithTx(tx).CreateConcept(ctx, cp.Title, cp.Content, cp.Tags)
		if err != nil {
			return nil, fmt.Sprintf("creating concept: %v", err)
		}
		return concept, ""
	case proposal.TypeKnowledge:
		return s.materializeKnowledgePg(ctx, prop)
	case proposal.TypeTask:
		return nil, errTaskProposalNotMaterialized
	case proposal.TypePlaybook:
		cp, errMsg := decodePlaybookPayload(prop.Payload, proposalWorkspaceID(prop))
		if errMsg != "" {
			return nil, errMsg
		}
		pb, err := s.playbook.Create(ctx, cp)
		if err != nil {
			return nil, fmt.Sprintf("creating playbook: %v", err)
		}
		return pb, ""
	default:
		return nil, fmt.Sprintf("unknown proposal type %q", prop.Type)
	}
}

// materializeDecisionPg is the per-type Postgres-Tx materialiser for
// TypeDecision. Extracted from the switch to keep gocyclo low.
func (s *Server) materializeDecisionPg(ctx context.Context, tx pgx.Tx, prop *db.PendingProposal) (any, string) {
	dp, errMsg := decodeDecisionParams(prop.Payload)
	if errMsg != "" {
		return nil, errMsg
	}
	if s.pgDecision == nil {
		return nil, "decision proposal materialisation requires PG decision store"
	}
	dec, err := s.pgDecision.WithTx(tx).Log(ctx, dp)
	if err != nil {
		return nil, fmt.Sprintf("creating decision: %v", err)
	}
	return dec, ""
}

// materializeFromPayloadIface is the SQLite-backed counterpart that calls
// through the backend-agnostic StoreIface methods. No tx — see
// acceptProposalSequential doc for the ordering / failure tradeoff.
//
//nolint:gocyclo // each case dispatches to an extracted sub-function; switch over proposal.Type is inherently branchy
func (s *Server) materializeFromPayloadIface(ctx context.Context, prop *db.PendingProposal) (any, string) {
	switch proposal.Type(prop.Type) {
	case proposal.TypeDecision:
		return s.materializeDecisionIface(ctx, prop)
	case proposal.TypeGoal:
		gp, errMsg := decodeGoalParams(prop.Payload)
		if errMsg != "" {
			return nil, errMsg
		}
		goal, err := s.gtd.CreateGoal(ctx, gp)
		if err != nil {
			return nil, fmt.Sprintf("creating goal: %v", err)
		}
		return goal, ""
	case proposal.TypeProject:
		pp, errMsg := decodeProjectParams(prop.Payload)
		if errMsg != "" {
			return nil, errMsg
		}
		project, err := s.gtd.CreateProject(ctx, pp)
		if err != nil {
			return nil, fmt.Sprintf("creating project: %v", err)
		}
		return project, ""
	case proposal.TypeConcept:
		cp, errMsg := decodeConceptPayload(prop.Payload)
		if errMsg != "" {
			return nil, errMsg
		}
		concept, err := s.learning.CreateConcept(ctx, cp.Title, cp.Content, cp.Tags)
		if err != nil {
			return nil, fmt.Sprintf("creating concept: %v", err)
		}
		return concept, ""
	case proposal.TypeKnowledge:
		return s.materializeKnowledgeIface(ctx, prop)
	case proposal.TypeTask:
		return nil, errTaskProposalNotMaterialized
	case proposal.TypePlaybook:
		cp, errMsg := decodePlaybookPayload(prop.Payload, proposalWorkspaceID(prop))
		if errMsg != "" {
			return nil, errMsg
		}
		pb, err := s.playbook.Create(ctx, cp)
		if err != nil {
			return nil, fmt.Sprintf("creating playbook: %v", err)
		}
		return pb, ""
	default:
		return nil, fmt.Sprintf("unknown proposal type %q", prop.Type)
	}
}

// materializeDecisionIface is the per-type backend-agnostic materialiser for
// TypeDecision. Extracted from the switch to keep gocyclo low.
func (s *Server) materializeDecisionIface(ctx context.Context, prop *db.PendingProposal) (any, string) {
	dp, errMsg := decodeDecisionParams(prop.Payload)
	if errMsg != "" {
		return nil, errMsg
	}
	if s.decision == nil {
		return nil, "decision proposal materialisation requires decision store"
	}
	dec, err := s.decision.Log(ctx, dp)
	if err != nil {
		return nil, fmt.Sprintf("creating decision: %v", err)
	}
	return dec, ""
}

// materializeKnowledgePg creates a knowledge_items row inside the given pgx
// transaction. Uses the backend-agnostic knowledge.StoreIface (not WithTx)
// because knowledge.Store does not expose a tx-aware variant; per A3 the
// sequential fallback is acceptable at personal-OS scale.
func (s *Server) materializeKnowledgePg(ctx context.Context, prop *db.PendingProposal) (any, string) {
	return s.materializeKnowledgeIface(ctx, prop)
}

// materializeKnowledgeIface decodes a TypeKnowledge proposal payload and
// calls knowledge.StoreIface.AddItem. The item type is fixed to "til"
// (TypeTIL) per Lead decision A1 — closest semantic match for reflection
// distillations.
func (s *Server) materializeKnowledgeIface(ctx context.Context, prop *db.PendingProposal) (any, string) {
	kp, errMsg := decodeKnowledgePayload(prop.Payload)
	if errMsg != "" {
		return nil, errMsg
	}
	if s.knowledge == nil {
		return nil, "knowledge proposal materialisation requires knowledge store"
	}
	item, err := s.knowledge.AddItem(ctx, knowledge.AddItemParams{
		Type:    string(knowledge.TypeTIL),
		Title:   kp.Title,
		Content: kp.Content,
		Tags:    kp.Tags,
		Source:  prop.ProposedBy.String,
	})
	if err != nil {
		return nil, fmt.Sprintf("creating knowledge item: %v", err)
	}
	return item, ""
}

// decodeKnowledgePayload decodes the JSON stored in pending_proposals.payload
// for type='knowledge', applying the same length caps as concept payloads.
func decodeKnowledgePayload(payload []byte) (proposal.KnowledgePayload, string) {
	var kp proposal.KnowledgePayload
	if err := json.Unmarshal(payload, &kp); err != nil {
		return proposal.KnowledgePayload{}, fmt.Sprintf("decoding knowledge payload: %v", err)
	}
	if kp.Title == "" {
		return proposal.KnowledgePayload{}, "knowledge payload missing title"
	}
	if len(kp.Title) > 512 {
		return proposal.KnowledgePayload{}, "knowledge title exceeds 512 bytes"
	}
	if len(kp.Content) > 65536 {
		return proposal.KnowledgePayload{}, "knowledge content exceeds 64 KB"
	}
	if len(kp.Tags) > 50 {
		return proposal.KnowledgePayload{}, "too many tags (max 50)"
	}
	return kp, ""
}

// decisionMaterialiseLogParams converts the proposer payload (from
// internal/proposal/payload.go) into the decision.LogParams shape used by all
// three backends. Any payload-level validation (length caps, control-char
// strip) is the drafter's job — by the time the proposal is on the queue
// the user has reviewed it. Empty title still produces an error so the
// proposal stays pending instead of materialising garbage.
func decodeDecisionParams(payload []byte) (decision.LogParams, string) {
	var p proposal.DecisionProposerPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return decision.LogParams{}, fmt.Sprintf("decoding decision payload: %v", err)
	}
	if p.Title == "" {
		return decision.LogParams{}, "decision payload missing title"
	}
	rationale := p.Rationale
	if len(p.Alternatives) > 0 {
		// Stash alternatives at the tail of rationale — the decisions table
		// has a free-form alternatives column but the model often produces
		// short-list strings; concatenating is lossless and keeps the
		// existing list_decisions UI unchanged.
		rationale += "\n\nAlternatives considered:"
		for _, a := range p.Alternatives {
			rationale += "\n- " + a
		}
	}
	return decision.LogParams{
		Title:     p.Title,
		Context:   fmt.Sprintf("auto-proposed by trigger_tool=%s session=%s", p.TriggerTool, p.SessionID),
		Decision:  p.Decision,
		Rationale: rationale,
	}, ""
}

// decodeGoalParams centralises the goal-payload JSON decode + RFC3339 parse
// so the pg-tx and iface paths don't drift.
func decodeGoalParams(payload []byte) (gtd.CreateGoalParams, string) {
	var p goalPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return gtd.CreateGoalParams{}, fmt.Sprintf("decoding goal payload: %v", err)
	}
	gp := gtd.CreateGoalParams{
		Title:       p.Title,
		Area:        p.Area,
		Description: p.Description,
	}
	if p.DueDate != "" {
		t, err := time.Parse(time.RFC3339, p.DueDate)
		if err != nil {
			return gtd.CreateGoalParams{}, fmt.Sprintf("invalid due_date in payload: %v", err)
		}
		gp.DueDate = &t
	}
	return gp, ""
}

func decodeProjectParams(payload []byte) (gtd.CreateProjectParams, string) {
	var p projectPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return gtd.CreateProjectParams{}, fmt.Sprintf("decoding project payload: %v", err)
	}
	pp := gtd.CreateProjectParams{
		Name:        p.Name,
		Title:       p.Title,
		Area:        p.Area,
		Description: p.Description,
		Priority:    p.Priority,
	}
	if p.GoalID != "" {
		gid, err := uuid.Parse(p.GoalID)
		if err != nil {
			return gtd.CreateProjectParams{}, fmt.Sprintf("invalid goal_id in payload: %v", err)
		}
		pp.GoalID = &gid
	}
	return pp, ""
}

func decodeConceptPayload(payload []byte) (conceptPayload, string) {
	var p conceptPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return conceptPayload{}, fmt.Sprintf("decoding concept payload: %v", err)
	}
	if len(p.Title) > 512 {
		return conceptPayload{}, "concept title exceeds 512 bytes"
	}
	if len(p.Content) > 65536 {
		return conceptPayload{}, "concept content exceeds 64 KB"
	}
	if len(p.Tags) > 50 {
		return conceptPayload{}, "too many tags (max 50)"
	}
	return p, ""
}

// decodePlaybookPayload extracts a playbook.CreateParams from a proposal payload JSON.
func decodePlaybookPayload(payload []byte, wsID *uuid.UUID) (playbook.CreateParams, string) {
	var p struct {
		TriggerPattern    string      `json:"trigger_pattern"`
		ActionTemplate    string      `json:"action_template"`
		SourceDecisionIDs []uuid.UUID `json:"source_decision_ids"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return playbook.CreateParams{}, fmt.Sprintf("decoding playbook payload: %v", err)
	}
	if p.TriggerPattern == "" || p.ActionTemplate == "" {
		return playbook.CreateParams{}, "playbook payload missing trigger_pattern or action_template"
	}
	const maxTriggerLen = 500
	const maxContentLen = 600
	if len([]rune(p.TriggerPattern)) > maxTriggerLen || len([]rune(p.ActionTemplate)) > maxContentLen {
		return playbook.CreateParams{}, "playbook payload fields exceed length limits"
	}
	return playbook.CreateParams{
		WorkspaceID:       wsID,
		TriggerPattern:    p.TriggerPattern,
		ActionTemplate:    p.ActionTemplate,
		SourceDecisionIDs: p.SourceDecisionIDs,
		Confidence:        0.50,
	}, ""
}
