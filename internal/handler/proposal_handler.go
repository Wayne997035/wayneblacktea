package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

const (
	proposalBodyLimit = 32 * 1024 // 32 KB — enough for 100 UUIDs and action
	actionAccept      = "accept"
	actionReject      = "reject"
	// errInternalGeneric is the per-row error text returned to batch callers
	// when a downstream materialise/resolve fails for reasons we don't want
	// to leak (server-side panic, store init not wired, etc). The detail is
	// already in c.Logger().Errorf — the API surface stays opaque.
	errInternalGeneric = "internal error"
)

// proposalListStore covers the operations needed to list and resolve proposals.
type proposalListStore interface {
	ListPending(ctx context.Context) ([]db.PendingProposal, error)
	ListAll(ctx context.Context, proposalType string, limit int32) ([]db.PendingProposal, error)
	Get(ctx context.Context, id uuid.UUID) (*db.PendingProposal, error)
	Resolve(ctx context.Context, id uuid.UUID, status proposal.Status) (*db.PendingProposal, error)
}

// proposalConceptStore covers the learning operations needed when accepting a
// concept proposal.
type proposalConceptStore interface {
	CreateConcept(ctx context.Context, title, content string, tags []string) (*db.Concept, error)
}

// proposalDecisionStore covers the decision operation needed when accepting
// a TypeDecision proposal. The handler depends on the iface (not the
// concrete *Store) so tests can inject a fake without booting a DB.
type proposalDecisionStore interface {
	Log(ctx context.Context, p decision.LogParams) (*db.Decision, error)
}

// proposalKnowledgeStore covers the knowledge operation needed when accepting
// a TypeKnowledge proposal.
type proposalKnowledgeStore interface {
	AddItem(ctx context.Context, p knowledge.AddItemParams) (*db.KnowledgeItem, error)
}

// proposalTaskStore covers the task-creation operation needed when accepting
// a TypeTask proposal (TASK 3 of feature/gtd-enforce-server-side). Kept narrow
// so tests can inject a fake without pulling the full gtd.StoreIface surface.
type proposalTaskStore interface {
	CreateTask(ctx context.Context, p gtd.CreateTaskParams) (*db.Task, error)
}

// ProposalHandler exposes GET /api/proposals/pending and
// POST /api/proposals/:id/confirm.
type ProposalHandler struct {
	proposal proposalListStore
	learning proposalConceptStore
	// decision is optional; nil disables decision-proposal materialisation
	// (handler returns 500 on accept of a TypeDecision row). For full
	// behaviour wire NewProposalHandlerWithDecision.
	decision proposalDecisionStore
	// knowledge is optional; nil disables knowledge-proposal materialisation.
	// Wire via WithKnowledge.
	knowledge proposalKnowledgeStore
	// task is optional; nil disables task-proposal materialisation. When nil
	// and the proposal type is TypeTask, accept falls back to the legacy
	// "Resolve only" branch (matches pre-TASK-3 behaviour). Wire via WithTask.
	task proposalTaskStore
}

// NewProposalHandler creates a ProposalHandler. Decision-proposal accept will
// fail with 500 unless WithDecision is also used (see
// NewProposalHandlerWithDecision).
func NewProposalHandler(p proposalListStore, l proposalConceptStore) *ProposalHandler {
	return &ProposalHandler{proposal: p, learning: l}
}

// WithDecision wires the decision store used by the TypeDecision accept path.
// Returns the handler for chaining. nil decision = decision-accept disabled.
func (h *ProposalHandler) WithDecision(d proposalDecisionStore) *ProposalHandler {
	h.decision = d
	return h
}

// WithKnowledge wires the knowledge store used by the TypeKnowledge accept path.
// Returns the handler for chaining. nil knowledge = knowledge-accept disabled.
func (h *ProposalHandler) WithKnowledge(k proposalKnowledgeStore) *ProposalHandler {
	h.knowledge = k
	return h
}

// WithTask wires the gtd store used by the TypeTask accept path
// (TASK 3 of feature/gtd-enforce-server-side). Returns the handler for
// chaining. nil = TypeTask accept falls back to the legacy resolve-only branch.
func (h *ProposalHandler) WithTask(t proposalTaskStore) *ProposalHandler {
	h.task = t
	return h
}

// pendingProposalResponse is the JSON shape returned to the frontend.
// payload is decoded from []byte to avoid double-encoding.
//
// Reason carries the resolution rationale persisted by migration 000051
// (`pending_proposals.reason`) — surfaced so the proposal UI can render
// "rejected because ttl-expired-30d" rather than an unattributed cleanup row.
// Pointer + omitempty: rows with a NULL Reason omit the field entirely so the
// frontend can distinguish "no reason" from empty string.
type pendingProposalResponse struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Status     string          `json:"status"`
	Payload    json.RawMessage `json:"payload"`
	ProposedBy *string         `json:"proposed_by"`
	CreatedAt  string          `json:"created_at"`
	ResolvedAt *string         `json:"resolved_at,omitempty"`
	Reason     *string         `json:"reason,omitempty"`
}

func toResponse(p db.PendingProposal) pendingProposalResponse {
	r := pendingProposalResponse{
		ID:      p.ID.String(),
		Type:    p.Type,
		Status:  p.Status,
		Payload: json.RawMessage(p.Payload),
	}
	if p.ProposedBy.Valid {
		s := p.ProposedBy.String
		r.ProposedBy = &s
	}
	if p.CreatedAt.Valid {
		ts := p.CreatedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
		r.CreatedAt = ts
	}
	if p.ResolvedAt.Valid {
		ts := p.ResolvedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
		r.ResolvedAt = &ts
	}
	if p.Reason.Valid {
		s := p.Reason.String
		r.Reason = &s
	}
	return r
}

// ListPendingProposals handles GET /api/proposals/pending.
// Accepts optional ?type=concept query param to filter by type.
func (h *ProposalHandler) ListPendingProposals(c echo.Context) error {
	rows, err := h.proposal.ListPending(c.Request().Context())
	if err != nil {
		c.Logger().Errorf("ListPendingProposals: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	// Optional server-side type filter (client also filters defensively).
	typeFilter := c.QueryParam("type")

	out := make([]pendingProposalResponse, 0, len(rows))
	for _, row := range rows {
		if typeFilter != "" && row.Type != typeFilter {
			continue
		}
		out = append(out, toResponse(row))
	}
	return c.JSON(http.StatusOK, out)
}

// allowedProposalStatuses is the validated set of values for the ?status= query param.
var allowedProposalStatuses = map[string]bool{
	"pending":  true,
	"accepted": true,
	"rejected": true,
	statusAll:  true,
}

// allowedProposalTypes is the validated set of values for the ?type= query param.
var allowedProposalTypes = map[string]bool{
	"concept":   true,
	"goal":      true,
	"project":   true,
	"task":      true,
	"decision":  true,
	"knowledge": true,
}

// ListProposals handles GET /api/proposals?status=pending|accepted|rejected|all.
// Omitting ?status defaults to "pending" for backward compat with the old endpoint.
// The ?type= param is also supported to filter by proposal type.
func (h *ProposalHandler) ListProposals(c echo.Context) error {
	status := c.QueryParam("status")
	if status == "" {
		status = "pending"
	} else if !allowedProposalStatuses[status] {
		return c.JSON(http.StatusBadRequest, errResp("status must be pending, accepted, rejected, or all"))
	}

	proposalType := c.QueryParam("type")
	if proposalType == "" {
		proposalType = "concept"
	} else if !allowedProposalTypes[proposalType] {
		return c.JSON(http.StatusBadRequest, errResp("type must be concept, goal, project, task, decision, or knowledge"))
	}

	rows, err := h.proposal.ListAll(c.Request().Context(), proposalType, 200)
	if err != nil {
		c.Logger().Errorf("ListProposals: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	out := make([]pendingProposalResponse, 0, len(rows))
	for _, p := range rows {
		if status == statusAll || p.Status == status {
			out = append(out, toResponse(p))
		}
	}
	return c.JSON(http.StatusOK, out)
}

type confirmRequest struct {
	Action string `json:"action"`
}

type confirmResponse struct {
	Proposal      pendingProposalResponse `json:"proposal"`
	Concept       *db.Concept             `json:"concept,omitempty"`
	Decision      *db.Decision            `json:"decision,omitempty"`
	KnowledgeItem *db.KnowledgeItem       `json:"knowledge_item,omitempty"`
	Task          *db.Task                `json:"task,omitempty"`
	// Warnings carries validator warnings surfaced when the proposal
	// description / kind fields are vague but server is in warn-mode
	// (WBT_STRICT_VAGUENESS unset). Strict mode rejects with 400 + warnings
	// in the error body so callers always see the same shape.
	Warnings []string `json:"warnings,omitempty"`
}

// ConfirmProposal handles POST /api/proposals/:id/confirm.
// Body: { "action": "accept" | "reject" }
func (h *ProposalHandler) ConfirmProposal(c echo.Context) error {
	rawID := c.Param("id")
	id, err := uuid.Parse(rawID)
	if err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid proposal id"))
	}

	var req confirmRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}

	ctx := c.Request().Context()

	switch req.Action {
	case actionReject:
		resolved, err := h.proposal.Resolve(ctx, id, proposal.StatusRejected)
		if errors.Is(err, proposal.ErrNotFound) {
			return c.JSON(http.StatusConflict, errResp("proposal not found or already resolved"))
		}
		if err != nil {
			c.Logger().Errorf("ConfirmProposal reject %s: %v", id, err)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
		return c.JSON(http.StatusOK, confirmResponse{Proposal: toResponse(*resolved)})

	case actionAccept:
		return h.handleAccept(c, ctx, id)

	default:
		return c.JSON(http.StatusBadRequest, errResp("action must be 'accept' or 'reject'"))
	}
}

// handleAccept executes the accept flow: Get → status guard → materialise
// (type-specific, before Resolve) → Resolve (atomic, WHERE status='pending').
// All types materialise BEFORE Resolve so a Resolve failure never leaves an
// "accepted" proposal with no backing row. Concurrent accepts can both
// materialise a resource before either Resolve fires; the second Resolve
// returns ErrNotFound → 409. Orphan resource > missing resource.
func (h *ProposalHandler) handleAccept(c echo.Context, ctx context.Context, id uuid.UUID) error {
	prop, err := h.proposal.Get(ctx, id)
	if errors.Is(err, proposal.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errResp("proposal not found"))
	}
	if err != nil {
		c.Logger().Errorf("ConfirmProposal get %s: %v", id, err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	if prop.Status != string(proposal.StatusPending) {
		return c.JSON(http.StatusConflict, errResp("proposal already resolved"))
	}

	switch proposal.Type(prop.Type) {
	case proposal.TypeDecision:
		return h.acceptDecision(c, ctx, id, prop)
	case proposal.TypeConcept:
		return h.acceptConcept(c, ctx, id, prop)
	case proposal.TypeKnowledge:
		return h.acceptKnowledge(c, ctx, id, prop)
	case proposal.TypeTask:
		return h.acceptTask(c, ctx, id, prop)
	default:
		// Other types (goal/project/playbook) are accepted in the MCP
		// path or are not yet wired through HTTP. Resolve only — preserves
		// prior behaviour for non-materialised rows.
		resolved, err := h.proposal.Resolve(ctx, id, proposal.StatusAccepted)
		if errors.Is(err, proposal.ErrNotFound) {
			return c.JSON(http.StatusConflict, errResp("proposal already resolved"))
		}
		if err != nil {
			c.Logger().Errorf("ConfirmProposal resolve %s: %v", id, err)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
		return c.JSON(http.StatusOK, confirmResponse{Proposal: toResponse(*resolved)})
	}
}

// acceptTask materialises a TypeTask proposal into the tasks table after
// running the same validator (CheckVagueness + CheckKindFields) that
// handleAddTask runs. In strict mode (WBT_STRICT_VAGUENESS=true) warnings
// abort the accept with HTTP 400; otherwise they are surfaced in the
// response body for the UI to display.
//
// Materialise BEFORE Resolve — matches acceptDecision / acceptConcept /
// acceptKnowledge. If Resolve fails on race, the task row is left behind
// (orphan > missing).
//
// nil task store falls back to a "resolve only" branch so the proposal can
// still be cleared from the queue — this matches the pre-TASK-3 behaviour
// for callers that don't yet wire WithTask.
func (h *ProposalHandler) acceptTask(c echo.Context, ctx context.Context, id uuid.UUID, prop *db.PendingProposal) error {
	tp, errMsg := decodeTaskProposalPayload(prop.Payload)
	if errMsg != "" {
		return c.JSON(http.StatusBadRequest, errResp(errMsg))
	}

	// Run the SAME validator that handleAddTask runs — closing the bypass
	// identified by SA decision 42e0b783. Field name "description" matches the
	// MCP add_task message so existing warnings translate 1:1.
	kind := tp.SuggestedKind
	if kind == "" {
		kind = validator.KindGeneral
	}
	if !validator.IsValidKind(kind) {
		kind = validator.KindGeneral
	}
	warnings := validator.CheckTaskInput(tp.Description, kind)
	if len(warnings) > 0 && validator.StrictModeEnabled() {
		return c.JSON(http.StatusBadRequest, map[string]any{
			"error":    "vagueness check failed",
			"warnings": warnings,
		})
	}

	if h.task == nil {
		// Legacy resolve-only fallback when the gtd store is not wired.
		// Surface warnings if any so the caller still gets visibility.
		resolved, err := h.proposal.Resolve(ctx, id, proposal.StatusAccepted)
		if errors.Is(err, proposal.ErrNotFound) {
			return c.JSON(http.StatusConflict, errResp("proposal already resolved"))
		}
		if err != nil {
			c.Logger().Errorf("ConfirmProposal resolve task %s: %v", id, err)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
		return c.JSON(http.StatusOK, confirmResponse{
			Proposal: toResponse(*resolved),
			Warnings: warnings,
		})
	}

	task, err := h.task.CreateTask(ctx, gtd.CreateTaskParams{
		Title:       tp.Title,
		Description: tp.Description,
		Kind:        kind,
	})
	if err != nil {
		c.Logger().Errorf("ConfirmProposal materialise task %s: %v", id, err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	resolved, err := h.proposal.Resolve(ctx, id, proposal.StatusAccepted)
	if errors.Is(err, proposal.ErrNotFound) {
		c.Logger().Warnf("ConfirmProposal accept %s: resolved concurrently (orphan task %s)", id, task.ID)
		return c.JSON(http.StatusConflict, errResp("proposal already resolved"))
	}
	if err != nil {
		c.Logger().Errorf("ConfirmProposal resolve task %s: %v", id, err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, confirmResponse{
		Proposal: toResponse(*resolved),
		Task:     task,
		Warnings: warnings,
	})
}

// decodeTaskProposalPayload decodes the proposal.TaskPayload JSON shape into
// a value ready for validator + gtd.CreateTask. Title-required matches the
// auto-capture producers; missing title is rejected as 400.
func decodeTaskProposalPayload(payload []byte) (proposal.TaskPayload, string) {
	var tp proposal.TaskPayload
	if err := json.Unmarshal(payload, &tp); err != nil {
		return proposal.TaskPayload{}, "task proposal payload is malformed"
	}
	if tp.Title == "" {
		return proposal.TaskPayload{}, "task proposal payload missing title"
	}
	if len(tp.Title) > maxTaskTitle {
		return proposal.TaskPayload{}, "task proposal title exceeds 500 characters"
	}
	return tp, ""
}

// acceptConcept materialises the concept row BEFORE marking the proposal
// accepted (matching acceptDecision). If Resolve fails after materialise (e.g.
// concurrent accept), an orphaned concept row is left behind; orphan > missing.
func (h *ProposalHandler) acceptConcept(c echo.Context, ctx context.Context, id uuid.UUID, prop *db.PendingProposal) error {
	cp, errMsg := decodeConceptCandidatePayload(prop.Payload)
	if errMsg != "" {
		return c.JSON(http.StatusBadRequest, errResp(errMsg))
	}

	concept, err := h.learning.CreateConcept(ctx, cp.Title, cp.Content, cp.Tags)
	if err != nil {
		c.Logger().Errorf("ConfirmProposal materialise concept %s: %v", id, err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	resolved, err := h.proposal.Resolve(ctx, id, proposal.StatusAccepted)
	if errors.Is(err, proposal.ErrNotFound) {
		c.Logger().Warnf("ConfirmProposal accept %s: resolved concurrently (orphan concept %s)", id, concept.ID)
		return c.JSON(http.StatusConflict, errResp("proposal already resolved"))
	}
	if err != nil {
		c.Logger().Errorf("ConfirmProposal resolve %s: %v", id, err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, confirmResponse{Proposal: toResponse(*resolved), Concept: concept})
}

// acceptDecision materialises the decision row BEFORE marking the proposal
// accepted. This avoids the accept-then-fail-materialise race the round-2
// reviewer flagged: if Resolve succeeded and Log later failed, the proposal
// would be marked accepted with no decision row to back it. Materialise-first
// can leave an orphan on Resolve race (rare); orphan > missing.
func (h *ProposalHandler) acceptDecision(c echo.Context, ctx context.Context, id uuid.UUID, prop *db.PendingProposal) error {
	if h.decision == nil {
		c.Logger().Errorf("ConfirmProposal accept %s: decision store not wired", id)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	dp, errMsg := decodeProposalDecisionPayload(prop.Payload)
	if errMsg != "" {
		return c.JSON(http.StatusBadRequest, errResp(errMsg))
	}

	logged, err := h.decision.Log(ctx, dp)
	if err != nil {
		c.Logger().Errorf("ConfirmProposal materialise decision %s: %v", id, err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	resolved, err := h.proposal.Resolve(ctx, id, proposal.StatusAccepted)
	if errors.Is(err, proposal.ErrNotFound) {
		// The decision row was already inserted; surface a 409 so the caller
		// knows the original proposal was already resolved. The orphan row
		// remains in `decisions` for the user to clean up via list_decisions.
		c.Logger().Warnf("ConfirmProposal accept %s: proposal resolved between materialise and resolve (orphan decision %s)", id, logged.ID)
		return c.JSON(http.StatusConflict, errResp("proposal already resolved"))
	}
	if err != nil {
		c.Logger().Errorf("ConfirmProposal resolve %s: %v", id, err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, confirmResponse{Proposal: toResponse(*resolved), Decision: logged})
}

// acceptKnowledge materialises a knowledge_items row BEFORE marking the
// proposal accepted (matching acceptDecision). If Resolve fails (concurrent
// accept), an orphaned knowledge_items row is left; orphan > missing.
func (h *ProposalHandler) acceptKnowledge(c echo.Context, ctx context.Context, id uuid.UUID, prop *db.PendingProposal) error {
	kp, errMsg := decodeKnowledgeCandidatePayload(prop.Payload)
	if errMsg != "" {
		return c.JSON(http.StatusBadRequest, errResp(errMsg))
	}

	if h.knowledge == nil {
		c.Logger().Errorf("ConfirmProposal knowledge %s: knowledge store not wired", id)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	source := ""
	if prop.ProposedBy.Valid {
		source = prop.ProposedBy.String
		if len(source) > 255 {
			source = source[:255]
		}
	}
	item, err := h.knowledge.AddItem(ctx, knowledge.AddItemParams{
		Type:    "til",
		Title:   kp.Title,
		Content: kp.Content,
		Tags:    kp.Tags,
		Source:  source,
	})
	if err != nil {
		c.Logger().Errorf("ConfirmProposal materialise knowledge %s: %v", id, err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	resolved, err := h.proposal.Resolve(ctx, id, proposal.StatusAccepted)
	if errors.Is(err, proposal.ErrNotFound) {
		c.Logger().Warnf("ConfirmProposal accept %s: resolved concurrently (orphan knowledge_item %s)", id, item.ID)
		return c.JSON(http.StatusConflict, errResp("proposal already resolved"))
	}
	if err != nil {
		c.Logger().Errorf("ConfirmProposal resolve %s: %v", id, err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}
	return c.JSON(http.StatusOK, confirmResponse{Proposal: toResponse(*resolved), KnowledgeItem: item})
}

// decodeProposalDecisionPayload decodes a TypeDecision pending_proposals
// payload (proposal.DecisionProposerPayload JSON shape) into a
// decision.LogParams ready for store.Log.
func decodeProposalDecisionPayload(payload []byte) (decision.LogParams, string) {
	var p proposal.DecisionProposerPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return decision.LogParams{}, "decision proposal payload is malformed"
	}
	if p.Title == "" {
		return decision.LogParams{}, "decision proposal payload missing title"
	}
	rationale := p.Rationale
	if len(p.Alternatives) > 0 {
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

// conceptCandidatePayload mirrors the shape stored by AutoProposeConceptFromKnowledge.
type conceptCandidatePayload struct {
	Title          string   `json:"title"`
	Content        string   `json:"content"`
	Tags           []string `json:"tags,omitempty"`
	SourceItemID   string   `json:"source_item_id,omitempty"`
	SourceItemType string   `json:"source_item_type,omitempty"`
}

const (
	maxConceptTitleBytes   = 512
	maxConceptContentBytes = 65536
	maxConceptTags         = 50
)

func decodeConceptCandidatePayload(payload []byte) (conceptCandidatePayload, string) {
	var p conceptCandidatePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return conceptCandidatePayload{}, "concept proposal payload is malformed"
	}
	if len(p.Title) > maxConceptTitleBytes {
		return conceptCandidatePayload{}, "concept title exceeds 512 characters"
	}
	if len(p.Content) > maxConceptContentBytes {
		return conceptCandidatePayload{}, "concept content exceeds 64 KB"
	}
	if len(p.Tags) > maxConceptTags {
		return conceptCandidatePayload{}, "too many tags (max 50)"
	}
	return p, ""
}

// knowledgeCandidatePayload mirrors proposal.KnowledgePayload for the handler
// layer to avoid importing the proposal package shape directly (the handler
// already imports proposal for proposal.Type constants).
type knowledgeCandidatePayload struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags,omitempty"`
}

func decodeKnowledgeCandidatePayload(payload []byte) (knowledgeCandidatePayload, string) {
	var kp knowledgeCandidatePayload
	if err := json.Unmarshal(payload, &kp); err != nil {
		return knowledgeCandidatePayload{}, "knowledge proposal payload is malformed"
	}
	if kp.Title == "" {
		return knowledgeCandidatePayload{}, "knowledge proposal payload missing title"
	}
	if len(kp.Title) > maxConceptTitleBytes {
		return knowledgeCandidatePayload{}, "knowledge title exceeds 512 characters"
	}
	if len(kp.Content) > maxConceptContentBytes {
		return knowledgeCandidatePayload{}, "knowledge content exceeds 64 KB"
	}
	if len(kp.Tags) > maxConceptTags {
		return knowledgeCandidatePayload{}, "too many tags (max 50)"
	}
	for _, tag := range kp.Tags {
		if len(tag) > 100 {
			return knowledgeCandidatePayload{}, "individual tag exceeds 100 bytes"
		}
	}
	return kp, ""
}

const (
	maxBatchConfirmIDs = 100
	minBatchConfirmIDs = 1
)

// confirmBatchRequest is the JSON body for POST /api/proposals/confirm-batch.
type confirmBatchRequest struct {
	IDs    []string `json:"ids"`
	Action string   `json:"action"`
}

// batchConfirmResultEntry records the per-ID outcome of a batch confirm.
type batchConfirmResultEntry struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped,omitempty"` // true when proposal was already resolved
	Error   string `json:"error,omitempty"`
}

// batchConfirmResponse is returned by ConfirmBatch.
type batchConfirmResponse struct {
	Results []batchConfirmResultEntry `json:"results"`
}

// proposalBatchMeta bundles pre-fetched payload + per-row decode error for
// batch accept. The `payloadErr` field captures decode failures so the resolve
// loop can return a 400-equivalent per-row entry instead of silently dropping
// the proposal (the round-2 reviewer flagged that silent skip on malformed
// decision payloads was the same data-loss class as missing decisions row).
type proposalBatchMeta struct {
	isConcept   bool
	cp          conceptCandidatePayload
	isDecision  bool
	dp          decision.LogParams
	isKnowledge bool
	kp          knowledgeCandidatePayload
	isTask      bool
	tp          proposal.TaskPayload
	// payloadErr is the human-readable decode failure message; empty = ok.
	// When non-empty the resolve loop reports it as a per-row error and skips
	// the resolve entirely (same semantics as the singular handler returning
	// 400 on a bad payload).
	payloadErr string
}

// batchProposalMeta pre-fetches payloads (concept + decision) for the given IDs.
// Missing proposals are silently skipped — the resolve loop surfaces them via
// ErrNotFound. Malformed payloads are recorded so the resolve loop can short-
// circuit with a per-row error before flipping the status.
func (h *ProposalHandler) batchProposalMeta(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]proposalBatchMeta {
	meta := make(map[uuid.UUID]proposalBatchMeta, len(ids))
	for _, id := range ids {
		prop, err := h.proposal.Get(ctx, id)
		if err != nil || prop == nil {
			continue
		}
		switch proposal.Type(prop.Type) {
		case proposal.TypeConcept:
			cp, errMsg := decodeConceptCandidatePayload(prop.Payload)
			if errMsg != "" {
				meta[id] = proposalBatchMeta{isConcept: true, payloadErr: errMsg}
				continue
			}
			meta[id] = proposalBatchMeta{isConcept: true, cp: cp}
		case proposal.TypeDecision:
			dp, errMsg := decodeProposalDecisionPayload(prop.Payload)
			if errMsg != "" {
				meta[id] = proposalBatchMeta{isDecision: true, payloadErr: errMsg}
				continue
			}
			meta[id] = proposalBatchMeta{isDecision: true, dp: dp}
		case proposal.TypeKnowledge:
			kp, errMsg := decodeKnowledgeCandidatePayload(prop.Payload)
			if errMsg != "" {
				meta[id] = proposalBatchMeta{isKnowledge: true, payloadErr: errMsg}
				continue
			}
			meta[id] = proposalBatchMeta{isKnowledge: true, kp: kp}
		case proposal.TypeTask:
			tp, errMsg := decodeTaskProposalPayload(prop.Payload)
			if errMsg != "" {
				meta[id] = proposalBatchMeta{isTask: true, payloadErr: errMsg}
				continue
			}
			meta[id] = proposalBatchMeta{isTask: true, tp: tp}
		default:
			meta[id] = proposalBatchMeta{}
		}
	}
	return meta
}

// batchResolveOne resolves a single proposal and materialises the appropriate
// entity. Decision, concept, and knowledge all materialise BEFORE Resolve so
// a Resolve failure never leaves an "accepted" proposal with no backing row.
//
//nolint:gocyclo // per-type materialisation branches are inherently complex; each branch calls extracted helpers
func (h *ProposalHandler) batchResolveOne(
	c echo.Context,
	ctx context.Context,
	id uuid.UUID,
	status proposal.Status,
	meta map[uuid.UUID]proposalBatchMeta,
) batchConfirmResultEntry {
	entry := batchConfirmResultEntry{ID: id.String()}

	m, hasMeta := meta[id]

	// All types materialise BEFORE Resolve: orphan resource on a concurrent
	// accept is preferable to an "accepted" proposal with no backing row.
	if status == proposal.StatusAccepted && hasMeta {
		if m.isDecision {
			if m.payloadErr != "" {
				entry.Error = m.payloadErr
				return entry
			}
			if h.decision == nil {
				c.Logger().Errorf("ConfirmBatch accept decision %s: decision store not wired", id)
				entry.Error = errInternalGeneric
				return entry
			}
			if _, derr := h.decision.Log(ctx, m.dp); derr != nil {
				c.Logger().Errorf("ConfirmBatch materialise decision %s: %v", id, derr)
				entry.Error = errInternalGeneric
				return entry
			}
		}
		if m.isConcept {
			if m.payloadErr != "" {
				entry.Error = m.payloadErr
				return entry
			}
			if h.learning == nil {
				c.Logger().Errorf("ConfirmBatch accept concept %s: learning store not wired", id)
				entry.Error = errInternalGeneric
				return entry
			}
			if _, cerr := h.learning.CreateConcept(ctx, m.cp.Title, m.cp.Content, m.cp.Tags); cerr != nil {
				c.Logger().Errorf("ConfirmBatch materialise concept %s: %v", id, cerr)
				entry.Error = errInternalGeneric
				return entry
			}
		}
		if m.isKnowledge {
			if m.payloadErr != "" {
				entry.Error = m.payloadErr
				return entry
			}
			if h.knowledge == nil {
				c.Logger().Errorf("ConfirmBatch accept knowledge %s: knowledge store not wired", id)
				entry.Error = errInternalGeneric
				return entry
			}
			if _, kerr := h.knowledge.AddItem(ctx, knowledge.AddItemParams{
				Type:    "til",
				Title:   m.kp.Title,
				Content: m.kp.Content,
				Tags:    m.kp.Tags,
			}); kerr != nil {
				c.Logger().Errorf("ConfirmBatch materialise knowledge %s: %v", id, kerr)
				entry.Error = errInternalGeneric
				return entry
			}
		}
		if m.isTask {
			if m.payloadErr != "" {
				entry.Error = m.payloadErr
				return entry
			}
			if h.task == nil {
				// Legacy: resolve-only when task store not wired. Same fallback as
				// singular acceptTask (line 357). Allow resolve to proceed.
			} else {
				kind := m.tp.SuggestedKind
				if kind == "" || !validator.IsValidKind(kind) {
					kind = validator.KindGeneral
				}
				warnings := validator.CheckTaskInput(m.tp.Description, kind)
				if len(warnings) > 0 && validator.StrictModeEnabled() {
					entry.Error = "vagueness check failed: " + strings.Join(warnings, "; ")
					return entry
				}
				if _, terr := h.task.CreateTask(ctx, gtd.CreateTaskParams{
					Title:       m.tp.Title,
					Description: m.tp.Description,
					Kind:        kind,
				}); terr != nil {
					c.Logger().Errorf("ConfirmBatch materialise task %s: %v", id, terr)
					entry.Error = errInternalGeneric
					return entry
				}
			}
		}
	}

	if _, err := h.proposal.Resolve(ctx, id, status); err != nil {
		if errors.Is(err, proposal.ErrNotFound) {
			entry.Skipped = true
			entry.Error = "not found or already resolved"
		} else {
			c.Logger().Errorf("ConfirmBatch resolve %s: %v", id, err)
			entry.Error = errInternalGeneric
		}
		return entry
	}
	entry.OK = true
	return entry
}

// ConfirmBatch handles POST /api/proposals/confirm-batch.
// Body: { "ids": ["uuid1","uuid2",...], "action": "accept" | "reject" }
// For each accepted concept proposal the handler materialises a Concept entity.
// Concept creation failures are non-fatal.
func (h *ProposalHandler) ConfirmBatch(c echo.Context) error {
	var req confirmBatchRequest
	limited := io.LimitReader(c.Request().Body, proposalBodyLimit)
	if err := json.NewDecoder(limited).Decode(&req); err != nil {
		return c.JSON(http.StatusBadRequest, errResp("invalid request body"))
	}

	if req.Action != actionAccept && req.Action != actionReject {
		return c.JSON(http.StatusBadRequest, errResp("action must be 'accept' or 'reject'"))
	}

	if len(req.IDs) < minBatchConfirmIDs {
		return c.JSON(http.StatusBadRequest, errResp("ids must contain at least 1 proposal UUID"))
	}
	if len(req.IDs) > maxBatchConfirmIDs {
		return c.JSON(http.StatusBadRequest, errResp("ids must contain at most 100 proposal UUIDs"))
	}

	ids := make([]uuid.UUID, 0, len(req.IDs))
	for i, raw := range req.IDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return c.JSON(http.StatusBadRequest, errResp(fmt.Sprintf("ids[%d] is not a valid UUID", i)))
		}
		ids = append(ids, id)
	}

	ctx := c.Request().Context()
	status := proposal.StatusRejected
	if req.Action == actionAccept {
		status = proposal.StatusAccepted
	}

	var meta map[uuid.UUID]proposalBatchMeta
	if req.Action == actionAccept {
		meta = h.batchProposalMeta(ctx, ids)
	}

	results := make([]batchConfirmResultEntry, 0, len(ids))
	for _, id := range ids {
		results = append(results, h.batchResolveOne(c, ctx, id, status, meta))
	}
	return c.JSON(http.StatusOK, batchConfirmResponse{Results: results})
}
