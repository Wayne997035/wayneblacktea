package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

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

func (s *Server) registerProposalTools(ms *server.MCPServer) {
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
	case "reject":
		row, err := s.proposal.Resolve(ctx, id, proposal.StatusRejected)
		if errors.Is(err, proposal.ErrNotFound) {
			return mcp.NewToolResultError("proposal not found or already resolved"), nil
		}
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("rejecting: %v", err)), nil
		}
		return jsonText(confirmResult{Proposal: row})
	case "accept":
		return s.acceptProposal(ctx, id)
	default:
		return mcp.NewToolResultError("action must be 'accept' or 'reject'"), nil
	}
}

func (s *Server) acceptProposal(ctx context.Context, id uuid.UUID) (*mcp.CallToolResult, error) {
	if s.pool != nil {
		return s.acceptProposalPg(ctx, id)
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

// materializeFromPayloadPg decodes the proposal's payload and creates the
// concrete entity inside the given pgx transaction. Returns the created
// entity or an error message string (empty = success). Postgres-only.
func (s *Server) materializeFromPayloadPg(ctx context.Context, tx pgx.Tx, prop *db.PendingProposal) (any, string) {
	switch proposal.Type(prop.Type) {
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
	case proposal.TypeTask:
		return nil, "task proposals are not materialized via confirm_proposal in Phase B1; use add_task directly"
	default:
		return nil, fmt.Sprintf("unknown proposal type %q", prop.Type)
	}
}

// materializeFromPayloadIface is the SQLite-backed counterpart that calls
// through the backend-agnostic StoreIface methods. No tx — see
// acceptProposalSequential doc for the ordering / failure tradeoff.
func (s *Server) materializeFromPayloadIface(ctx context.Context, prop *db.PendingProposal) (any, string) {
	switch proposal.Type(prop.Type) {
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
	case proposal.TypeTask:
		return nil, "task proposals are not materialized via confirm_proposal in Phase B1; use add_task directly"
	default:
		return nil, fmt.Sprintf("unknown proposal type %q", prop.Type)
	}
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
	return p, ""
}
