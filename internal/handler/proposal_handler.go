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
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
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
	//
	// [F981-05] Also now the single 500-response text for every top-level
	// StatusInternalServerError path in this file — 17 call sites previously
	// used a differently-worded literal error string instead of this
	// constant, an inconsistency (not an information-disclosure gap by
	// itself, since neither wording leaks anything) closed by routing them
	// all through the same constant a caller could someday key off
	// consistently.
	errInternalGeneric = "internal error"
)

// proposalListStore covers the operations needed to list and resolve proposals.
type proposalListStore interface {
	ListPending(ctx context.Context) ([]db.PendingProposal, error)
	ListAll(ctx context.Context, proposalType string, limit int32) ([]db.PendingProposal, error)
	Get(ctx context.Context, id uuid.UUID) (*db.PendingProposal, error)
	Resolve(ctx context.Context, id uuid.UUID, status proposal.Status) (*db.PendingProposal, error)
}

// goalProjectAcceptAdapterFactory constructs a fresh proposal.AcceptAdapter
// for one proposal.AcceptOrchestration(ctx, id, adapter) call, given the
// target proposal id. A factory func — rather than a single pre-built
// adapter — because AcceptAdapter implementations hold per-call transaction
// state (see AcceptAdapter's doc comment) and must be constructed new for
// every accept.
//
// This is a func type, not a narrow store interface like proposalListStore
// above, because building proposal.PgAcceptDeps /
// sqlite.AcceptDeps requires backend-specific types (pgxpool.Pool,
// *gtd.Store vs *sqlite.GTDStore, ...) that the handler package has no
// business importing (ADR-0003 keeps pgx/sqlite typing out of the handler
// layer). Backend selection happens once, at wiring time, in
// cmd/server/main.go's wireHandlers — the one place that already knows
// which storage.ServerStores backend is live (mirrors the existing
// `if pool := stores.PgxPool(); pool != nil` branch used there for the
// AI-cost store) — and the resulting factory is threaded in here via
// WithGoalProjectAccept.
type goalProjectAcceptAdapterFactory func(id uuid.UUID) proposal.AcceptAdapter

// ProposalHandler exposes GET /api/proposals/pending and
// POST /api/proposals/:id/confirm.
type ProposalHandler struct {
	proposal proposalListStore
	// goalProjectAdapter is optional; nil disables seam-routed materialisation.
	// When nil and the proposal type is TypeGoal, TypeProject or TypeKnowledge,
	// accept falls back to the legacy "Resolve only" branch (matches
	// pre-A1-http behaviour, and keeps callers that don't wire
	// WithGoalProjectAccept — e.g. existing unit tests — working unchanged).
	// TypeDecision, TypeConcept and TypeTask do NOT fall back: they fail closed
	// (500 / per-row "internal error", no resolve). Wire via WithGoalProjectAccept.
	goalProjectAdapter goalProjectAcceptAdapterFactory
}

// NewProposalHandler creates a ProposalHandler over the list/resolve store p.
// It takes no concept or knowledge store: every accept that materialises a row
// goes through the adapter wired with WithGoalProjectAccept. Accepting a
// decision, concept or task proposal fails with 500 unless WithGoalProjectAccept
// is also used: those three types have no resolve-only fallback.
func NewProposalHandler(p proposalListStore) *ProposalHandler {
	return &ProposalHandler{proposal: p}
}

// WithGoalProjectAccept wires the factory used to construct a fresh
// proposal.AcceptAdapter per accept call for TypeGoal / TypeProject /
// TypeKnowledge / TypeDecision / TypeConcept / TypeTask proposals, routing them
// through the same atomic proposal.AcceptOrchestration flow the MCP path uses.
// Returns the handler for chaining. nil (the zero value) falls back to the
// legacy resolve-only branch for goal/project/knowledge and fails closed for
// TypeDecision, TypeConcept and TypeTask.
func (h *ProposalHandler) WithGoalProjectAccept(f goalProjectAcceptAdapterFactory) *ProposalHandler {
	h.goalProjectAdapter = f
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
		return c.JSON(http.StatusInternalServerError, errResp(errInternalGeneric))
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
		return c.JSON(http.StatusInternalServerError, errResp(errInternalGeneric))
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
			return c.JSON(http.StatusInternalServerError, errResp(errInternalGeneric))
		}
		return c.JSON(http.StatusOK, confirmResponse{Proposal: toResponse(*resolved)})

	case actionAccept:
		return h.handleAccept(c, ctx, id)

	default:
		return c.JSON(http.StatusBadRequest, errResp("action must be 'accept' or 'reject'"))
	}
}

// handleAccept executes the accept flow: Get → status guard → type-specific
// accept. TypeGoal/TypeProject/TypeKnowledge/TypeDecision/TypeConcept/TypeTask
// go through proposal.AcceptOrchestration, which materialises and resolves
// inside ONE transaction, so a failed resolve (including a concurrent accept
// that wins the race) rolls the materialised row back and leaves no orphan.
// Every other type resolves only.
func (h *ProposalHandler) handleAccept(c echo.Context, ctx context.Context, id uuid.UUID) error {
	prop, err := h.proposal.Get(ctx, id)
	if errors.Is(err, proposal.ErrNotFound) {
		return c.JSON(http.StatusNotFound, errResp("proposal not found"))
	}
	if err != nil {
		c.Logger().Errorf("ConfirmProposal get %s: %v", id, err)
		return c.JSON(http.StatusInternalServerError, errResp(errInternalGeneric))
	}
	if prop.Status != string(proposal.StatusPending) {
		return c.JSON(http.StatusConflict, errResp("proposal already resolved"))
	}

	switch proposal.Type(prop.Type) {
	case proposal.TypeGoal, proposal.TypeProject, proposal.TypeKnowledge, proposal.TypeDecision, proposal.TypeConcept, proposal.TypeTask:
		// TypeTask routes through the same atomic proposal.AcceptOrchestration
		// seam: the tasks row and the proposal resolve commit together, so a
		// lost race between two accepts rolls the task insert back (the former
		// two-step CreateTask followed by a separate proposal resolve left an
		// orphan task).
		// TypeConcept routes through the same atomic
		// proposal.AcceptOrchestration seam: the concepts row, its
		// review_schedule row and the proposal resolve commit together, so a
		// failed resolve rolls both rows back (the former two-step concept
		// create followed by a separate proposal resolve left an orphan
		// concept).
		// TypeDecision routes through the same atomic
		// proposal.AcceptOrchestration seam: the decision insert and the
		// proposal resolve commit together, so a failed resolve rolls the
		// decisions row back (the former h.decision.Log + separate
		// h.proposal.Resolve pair left an orphan row).
		// [F184-03] TypeKnowledge routes through the same atomic
		// proposal.AcceptOrchestration seam as TypeGoal/TypeProject —
		// h.goalProjectAdapter's deps already carry a.deps.Knowledge (wired
		// once in cmd/server/main.go's buildGoalProjectAcceptAdapter,
		// predating this task) and both AcceptAdapter.Materialize
		// implementations now handle TypeKnowledge (accept_pg.go /
		// accept_proposal.go's materializeKnowledge). Replaces the former
		// non-tx acceptKnowledge (h.knowledge.AddItem + separate
		// h.proposal.Resolve call).
		return h.acceptGoalOrProject(c, ctx, id, prop)
	default:
		// playbook (and any unrecognised future type) is accepted in the MCP
		// path or is not yet wired through HTTP. Resolve only — preserves
		// prior behaviour for non-materialised rows. NEVER route playbook
		// through h.goalProjectAdapter: both AcceptAdapter implementations
		// return an "A1-seam TODO" error for TypePlaybook (see
		// accept_pg.go / accept_proposal.go's Materialize switches).
		resolved, err := h.proposal.Resolve(ctx, id, proposal.StatusAccepted)
		if errors.Is(err, proposal.ErrNotFound) {
			return c.JSON(http.StatusConflict, errResp("proposal already resolved"))
		}
		if err != nil {
			c.Logger().Errorf("ConfirmProposal resolve %s: %v", id, err)
			return c.JSON(http.StatusInternalServerError, errResp(errInternalGeneric))
		}
		return c.JSON(http.StatusOK, confirmResponse{Proposal: toResponse(*resolved)})
	}
}

// acceptGoalOrProject materialises a TypeGoal, TypeProject, [F184-03]
// TypeKnowledge, TypeDecision, TypeConcept, or TypeTask proposal via the atomic
// proposal.AcceptOrchestration flow (ReadPending → PrepareOutOfBand → BeginTx
// → Materialize → ResolveAccepted → Commit) — the same seam
// internal/proposal/accept_pg.go and internal/storage/sqlite/accept_proposal.go
// already implement for the MCP path. This path is a single atomic
// transaction — AcceptOrchestration owns the materialise + resolve pairing
// itself, so there is no separate "materialise-then-resolve race" to reason
// about here: a lost race rolls the materialised row back.
//
// TypeGoal/TypeProject discard AcceptOrchestration's created value (no
// dedicated confirmResponse field exists for either). TypeKnowledge,
// TypeDecision, TypeConcept and TypeTask do NOT discard it —
// confirmResponse.KnowledgeItem / Decision / Concept / Task are existing API
// fields callers depend on; silently dropping them here would be a
// response-shape regression (see seamAcceptResponse).
//
// h.goalProjectAdapter nil: TypeDecision, TypeConcept and TypeTask fail
// CLOSED — 500 and no resolve, because a resolve-only fallback would flip the
// proposal to accepted with no decisions / concepts / tasks row behind it
// (permanent data loss: Resolve only matches status='pending').
// TypeGoal/TypeProject/TypeKnowledge keep the legacy resolve-only fallback
// (matches the prior default-branch behaviour, and keeps existing callers that
// don't wire WithGoalProjectAccept working unchanged); for TypeKnowledge that
// means no knowledge_items row is materialised when unwired. Production always
// wires WithGoalProjectAccept (cmd/server/main.go), so the unwired branch is
// only reachable from a handler built without it (e.g. a test).
func (h *ProposalHandler) acceptGoalOrProject(c echo.Context, ctx context.Context, id uuid.UUID, prop *db.PendingProposal) error {
	if h.goalProjectAdapter == nil {
		if proposal.Type(prop.Type) == proposal.TypeDecision { // [F205-03] fail closed: no resolve-only fallback for decisions
			c.Logger().Errorf("ConfirmProposal accept %s: decision accept adapter not wired", id)
			return c.JSON(http.StatusInternalServerError, errResp(errInternalGeneric))
		}
		if proposal.Type(prop.Type) == proposal.TypeConcept { // [F205-07] fail closed: no resolve-only fallback for concepts
			c.Logger().Errorf("ConfirmProposal accept %s: concept accept adapter not wired", id)
			return c.JSON(http.StatusInternalServerError, errResp(errInternalGeneric))
		}
		if proposal.Type(prop.Type) == proposal.TypeTask { // [F205-12] fail closed: no resolve-only fallback for tasks
			c.Logger().Errorf("ConfirmProposal accept %s: task accept adapter not wired", id)
			return c.JSON(http.StatusInternalServerError, errResp(errInternalGeneric))
		}
		resolved, err := h.proposal.Resolve(ctx, id, proposal.StatusAccepted)
		if errors.Is(err, proposal.ErrNotFound) {
			return c.JSON(http.StatusConflict, errResp("proposal already resolved"))
		}
		if err != nil {
			c.Logger().Errorf("ConfirmProposal resolve %s: %v", id, err)
			return c.JSON(http.StatusInternalServerError, errResp(errInternalGeneric))
		}
		return c.JSON(http.StatusOK, confirmResponse{Proposal: toResponse(*resolved)})
	}

	// Validate the payload decodes cleanly BEFORE opening a transaction, so a
	// malformed payload surfaces as 400 instead of a generic 500 from deep
	// inside AcceptOrchestration's Materialize step. The adapter decodes the
	// same bytes again inside the tx — cheap for these small JSON payloads, and
	// keeps decode logic single-sourced in proposal.DecodeGoalParams/
	// DecodeProjectParams/DecodeKnowledgePayload/DecodeDecisionParams/
	// DecodeTaskParams rather than duplicated here. For TypeTask the gate also
	// carries the vagueness warnings to the response (and rejects them in
	// strict mode).
	warnings, badRequest := acceptPayloadGate(proposal.Type(prop.Type), prop.Payload)
	if badRequest != nil {
		return c.JSON(http.StatusBadRequest, badRequest)
	}

	adapter := h.goalProjectAdapter(id)
	resolved, created, err := proposal.AcceptOrchestration(ctx, id, adapter)
	if err != nil {
		if errors.Is(err, proposal.ErrNotFound) || errors.Is(err, proposal.ErrAlreadyResolved) {
			return c.JSON(http.StatusConflict, errResp("proposal already resolved"))
		}
		if errors.Is(err, gtd.ErrConflict) {
			return c.JSON(http.StatusConflict, errResp("project name already exists"))
		}
		c.Logger().Errorf("ConfirmProposal accept %s: %v", id, err)
		return c.JSON(http.StatusInternalServerError, errResp(errInternalGeneric))
	}
	resp := seamAcceptResponse(proposal.Type(prop.Type), resolved, created, warnings)
	if proposal.Type(prop.Type) == proposal.TypeTask && resp.Task == nil {
		// The transaction is already committed, so respond 200 without the task
		// rather than a 5xx for a row that exists.
		c.Logger().Errorf("ConfirmProposal accept %s: task adapter returned %T carrying no *db.Task", id, created)
	}
	return c.JSON(http.StatusOK, resp)
}

// acceptPayloadGate is the pre-transaction payload gate of the seam accept
// path. It returns the vagueness warnings to surface in the response (TypeTask
// only) and, when the payload must be refused, the 400 body.
func acceptPayloadGate(t proposal.Type, payload []byte) (warnings []string, badRequest any) {
	if t == proposal.TypeTask {
		return taskPayloadGate(payload)
	}
	if errMsg := validateGoalProjectPayload(t, payload); errMsg != "" {
		return nil, errResp(errMsg)
	}
	return nil, nil
}

// taskPayloadGate decodes a TypeTask payload in warn mode (the adapter
// re-decodes inside the transaction with the server's real strict setting) and
// applies the strict-mode decision HERE, so a vague payload is a 400 carrying
// the warnings array decided before any transaction opens — not a generic 500
// from a failed Materialize. Non-strict warnings are returned for the response.
func taskPayloadGate(payload []byte) (warnings []string, badRequest any) {
	_, warnings, err := proposal.DecodeTaskParams(payload, false)
	if err != nil {
		return nil, errResp(err.Error())
	}
	if len(warnings) > 0 && validator.StrictModeEnabled() {
		return nil, map[string]any{
			"error":    "vagueness check failed",
			"warnings": warnings,
		}
	}
	return warnings, nil
}

// taskFromCreated extracts the created task from the value AcceptOrchestration
// returned for a TypeTask: either a bare *db.Task, or the map[string]any
// {"task": *db.Task, "warnings": []string} both adapters return when the payload
// raised warnings. A handler that recognised only the first shape would
// silently drop `task` on every warn-mode accept. Any other shape yields nil.
func taskFromCreated(created any) *db.Task {
	switch v := created.(type) {
	case *db.Task:
		return v
	case map[string]any:
		if task, ok := v["task"].(*db.Task); ok {
			return task
		}
	}
	return nil
}

// seamAcceptResponse builds the confirm response for a proposal accepted via
// proposal.AcceptOrchestration. Four types carry a dedicated confirmResponse
// field that surfaces the created row: TypeKnowledge (KnowledgeItem),
// TypeDecision (Decision), TypeConcept (Concept) and TypeTask (Task, with the
// gate's warnings); TypeGoal and TypeProject discard it. Both backends' adapters MUST return the concrete db type
// for these — a different shape would silently drop the field from the
// response, which is why the backend parity of each is pinned by a test
// (e.g. TestConfirmProposal_TypeDecision_BackendParity).
func seamAcceptResponse(t proposal.Type, resolved *db.PendingProposal, created any, warnings []string) confirmResponse {
	resp := confirmResponse{Proposal: toResponse(*resolved)}
	switch t {
	case proposal.TypeKnowledge:
		if item, ok := created.(*db.KnowledgeItem); ok {
			resp.KnowledgeItem = item
		}
	case proposal.TypeDecision:
		dec, ok := created.(*db.Decision)
		if ok { // [F205-01] surface the created decision in the response
			resp.Decision = dec
		}
	case proposal.TypeConcept:
		concept, ok := created.(*db.Concept)
		if ok { // [F205-05] surface the created concept in the response
			resp.Concept = concept
		}
	case proposal.TypeTask:
		resp.Task = taskFromCreated(created)
		resp.Warnings = warnings
	default:
		// TypeGoal/TypeProject have no dedicated confirmResponse field, so
		// their created value is discarded.
	}
	return resp
}

// validateGoalProjectPayload decodes payload with the same
// proposal.DecodeGoalParams / proposal.DecodeProjectParams /
// proposal.DecodeKnowledgePayload [F184-03] / proposal.DecodeDecisionParams
// [F205-01] helpers the AcceptAdapter's Materialize step uses (TypeConcept is
// the exception: it is gated by the handler's own, stricter
// decodeConceptCandidatePayload), returning a human-readable message on
// failure ("" = ok). Types outside those five are not reachable here (see
// handleAccept's switch) and fall through to "" (no-op).
func validateGoalProjectPayload(t proposal.Type, payload []byte) string {
	switch t {
	case proposal.TypeGoal:
		if _, err := proposal.DecodeGoalParams(payload); err != nil {
			return "goal proposal payload is malformed"
		}
	case proposal.TypeProject:
		if _, err := proposal.DecodeProjectParams(payload); err != nil {
			return "project proposal payload is malformed"
		}
	case proposal.TypeKnowledge:
		// DecodeKnowledgePayload's own error text is already specific
		// ("knowledge payload missing title", "knowledge title exceeds 512
		// bytes", ...) so it is surfaced verbatim rather than collapsed to a
		// generic "malformed" message — matches this handler's acceptance
		// criteria for knowledge payload decode errors.
		if _, err := proposal.DecodeKnowledgePayload(payload); err != nil {
			return err.Error()
		}
	case proposal.TypeDecision:
		// [F205-01] proposal.DecodeDecisionParams is the single decoder for
		// TypeDecision (the old handler-local one had no length caps). Its
		// error text is already specific ("decision payload missing title",
		// "decision title exceeds 512 bytes", ...) so it is surfaced verbatim,
		// same choice as TypeKnowledge above.
		if _, err := proposal.DecodeDecisionParams(payload); err != nil {
			return err.Error()
		}
	case proposal.TypeConcept:
		// The handler's own decodeConceptCandidatePayload stays the gate: it is
		// the strictest of the three concept decoders, so passing it implies
		// proposal.DecodeConceptPayload (the in-tx decode) passes too, and the
		// 400 texts ("concept title exceeds 512 characters", "too many tags
		// (max 50)", ...) stay exactly what clients already see.
		if _, errMsg := decodeConceptCandidatePayload(payload); errMsg != "" {
			return errMsg
		}
	default:
		// Not reachable with a gated type: handleAccept's switch routes
		// TypeGoal/TypeProject/TypeKnowledge/TypeDecision/TypeConcept/TypeTask
		// to acceptGoalOrProject, and TypeTask is gated by taskPayloadGate
		// before this function is called (acceptPayloadGate). Kept exhaustive
		// to satisfy the exhaustive linter without a //nolint suppression.
	}
	return ""
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
	// Warnings mirrors confirmResponse.Warnings (line 292) for the batch
	// path — set only for the TypeTask success branch today ([F0902-54]).
	Warnings []string `json:"warnings,omitempty"`
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
	// isGoalOrProject marks TypeGoal/TypeProject/TypeKnowledge [F184-03],
	// TypeDecision [F205-01], TypeConcept and TypeTask proposals when
	// h.goalProjectAdapter is wired. These six route straight through
	// proposal.AcceptOrchestration (batchResolveOne returns early for this
	// flag) because AcceptOrchestration owns its own atomic resolve inside the
	// transaction — see acceptGoalOrProject's doc comment. false with
	// h.goalProjectAdapter nil reproduces the pre-A1-http legacy resolve-only
	// fallback for goal/project/knowledge; TypeDecision, TypeConcept and
	// TypeTask set notWired instead.
	isGoalOrProject bool
	// notWired marks a TypeDecision, TypeConcept or TypeTask proposal seen
	// while h.goalProjectAdapter is nil [F205-03]. batchResolveOne fails closed
	// on it — per-row "internal error", never a resolve — because the generic
	// Resolve at the tail of batchResolveOne would flip the proposal to
	// accepted with no decisions / concepts / tasks row behind it (the M2
	// data-loss class lookupFailed documents below).
	notWired bool
	// warnings carries a TypeTask proposal's non-strict vagueness warnings from
	// the pre-fetch gate to batchResolveOne, which copies them onto the
	// success entry (batchConfirmResultEntry.Warnings). nil for every other type.
	warnings []string
	// payloadErr is the human-readable decode failure message; empty = ok.
	// When non-empty the resolve loop reports it as a per-row error and skips
	// the resolve entirely (same semantics as the singular handler returning
	// 400 on a bad payload).
	payloadErr string
	// lookupFailed marks a transient (non-ErrNotFound) error fetching the
	// proposal row during batchProposalMeta's pre-fetch pass — e.g. pgxpool
	// connection reset, statement timeout, pool acquire failure. Without this
	// flag the id would have no meta entry at all, and batchResolveOne's
	// accept path would fall through to the generic h.proposal.Resolve call,
	// which can succeed on a fresh connection and flip the proposal to
	// accepted with NO backing goal/project row ever materialised (M2:
	// permanent, unrecoverable data loss — proposal.sql's Resolve query only
	// matches status='pending'). lookupFailed makes batchResolveOne fail
	// closed instead: report an error, never resolve.
	lookupFailed bool
}

// batchProposalMeta pre-fetches and gates payloads (the seam-routed
// goal/project/knowledge/decision/concept/task types) for the given IDs.
// Proposals genuinely not found are silently skipped — the resolve loop
// surfaces them via its own ErrNotFound check on h.proposal.Resolve. A
// transient (non-ErrNotFound) error fetching the row is NOT treated the same
// way: it is recorded as lookupFailed so batchResolveOne fails closed instead
// of falling through to a generic Resolve call on a row we never actually
// re-read (see proposalBatchMeta.lookupFailed doc comment for the M2 data-
// loss scenario this closes). Malformed payloads are recorded so the resolve
// loop can short-circuit with a per-row error before flipping the status.
func (h *ProposalHandler) batchProposalMeta(c echo.Context, ctx context.Context, ids []uuid.UUID) map[uuid.UUID]proposalBatchMeta {
	meta := make(map[uuid.UUID]proposalBatchMeta, len(ids))
	for _, id := range ids {
		prop, err := h.proposal.Get(ctx, id)
		if err != nil {
			if errors.Is(err, proposal.ErrNotFound) {
				continue
			}
			c.Logger().Errorf("ConfirmBatch: fetching proposal %s failed: %v", id, err)
			meta[id] = proposalBatchMeta{lookupFailed: true}
			continue
		}
		if prop == nil {
			// Defensive: Get should never return (nil, nil), but this is not
			// the same as a confirmed not-found — fail closed rather than
			// silently skip.
			meta[id] = proposalBatchMeta{lookupFailed: true}
			continue
		}
		meta[id] = h.batchProposalMetaForType(prop)
	}
	return meta
}

// batchProposalMetaForType decodes prop's payload into the proposalBatchMeta
// shape batchResolveOne dispatches on, per proposal.Type. Split out of
// batchProposalMeta so the Get-error / fail-closed handling above it stays
// readable on its own (gocyclo).
func (h *ProposalHandler) batchProposalMetaForType(prop *db.PendingProposal) proposalBatchMeta {
	switch proposal.Type(prop.Type) {
	case proposal.TypeGoal, proposal.TypeProject, proposal.TypeKnowledge, proposal.TypeDecision, proposal.TypeConcept, proposal.TypeTask:
		if h.goalProjectAdapter == nil {
			if proposal.Type(prop.Type) == proposal.TypeDecision {
				// [F205-03] No resolve-only fallback for TypeDecision: it would
				// flip the proposal to accepted with no decisions row. Fail
				// closed in batchResolveOne instead (notWired).
				return proposalBatchMeta{notWired: true}
			}
			if proposal.Type(prop.Type) == proposal.TypeConcept {
				// Same reasoning for TypeConcept: a resolve-only fallback would
				// accept the proposal with no concepts row behind it.
				return proposalBatchMeta{notWired: true}
			}
			if proposal.Type(prop.Type) == proposal.TypeTask { // [F205-13] fail closed: no resolve-only fallback for tasks
				// Same reasoning for TypeTask: a resolve-only fallback would
				// accept the proposal with no tasks row behind it.
				return proposalBatchMeta{notWired: true}
			}
			// Legacy fallback: resolve-only, matches pre-A1-http behaviour and
			// keeps existing callers that don't wire WithGoalProjectAccept
			// working unchanged.
			return proposalBatchMeta{}
		}
		if proposal.Type(prop.Type) == proposal.TypeTask {
			// The task gate (decode + strict) sits between the nil-adapter block
			// above and the shared validate below: it needs the warnings, which
			// validateGoalProjectPayload's string-only return cannot carry.
			return batchTaskMeta(prop.Payload)
		}
		// Decode-first, same as acceptGoalOrProject's decode-before-tx
		// pattern: surfaces a specific per-row error instead of a generic one
		// from deep inside AcceptOrchestration.
		if errMsg := validateGoalProjectPayload(proposal.Type(prop.Type), prop.Payload); errMsg != "" {
			return proposalBatchMeta{isGoalOrProject: true, payloadErr: errMsg}
		}
		return proposalBatchMeta{isGoalOrProject: true}
	default:
		return proposalBatchMeta{}
	}
}

// batchTaskMeta is the batch twin of taskPayloadGate: it decodes a TypeTask
// payload in warn mode and applies the strict-mode decision HERE, before any
// transaction, as a per-row payloadErr — a decode failure carries the decoder's
// text, a strict-mode vagueness failure "vagueness check failed: w1; w2".
// Otherwise the warnings ride along in the meta for batchResolveOne to copy
// onto the success entry.
func batchTaskMeta(payload []byte) proposalBatchMeta {
	_, warnings, err := proposal.DecodeTaskParams(payload, false)
	if err != nil {
		return proposalBatchMeta{isGoalOrProject: true, payloadErr: err.Error()}
	}
	if len(warnings) > 0 && validator.StrictModeEnabled() {
		return proposalBatchMeta{isGoalOrProject: true, payloadErr: "vagueness check failed: " + strings.Join(warnings, "; ")}
	}
	return proposalBatchMeta{isGoalOrProject: true, warnings: warnings}
}

// batchResolveOne resolves a single proposal and materialises the appropriate
// entity. Goal/project/knowledge [F184-03], decision [F205-01], concept and
// task route through the atomic proposal.AcceptOrchestration seam below
// (materialise + resolve happen inside one transaction, so a Resolve failure
// never leaves an "accepted" proposal with no backing row and never leaves an
// orphan row either); every other accept falls through to the generic Resolve
// at the tail.
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

	// Fail closed: batchProposalMeta recorded a transient (non-ErrNotFound)
	// error while pre-fetching this proposal's row (pool exhaustion,
	// connection reset, statement timeout, ...). Report an error instead of
	// falling through to the generic h.proposal.Resolve call at the tail of
	// this function — that call can succeed on a fresh connection and mark
	// the proposal accepted with no materialised backing row, which for
	// goal/project is permanent, unrecoverable data loss (M2).
	if status == proposal.StatusAccepted && hasMeta && m.lookupFailed {
		entry.Error = errInternalGeneric
		return entry
	}

	// Fail closed [F205-03]: a TypeDecision, TypeConcept or TypeTask proposal
	// with no accept adapter wired. Same reason as lookupFailed above — the
	// tail Resolve would mark it accepted with no decisions / concepts / tasks
	// row behind it.
	if status == proposal.StatusAccepted && hasMeta && m.notWired {
		c.Logger().Errorf("ConfirmBatch accept %s: accept adapter not wired", id)
		entry.Error = errInternalGeneric
		return entry
	}

	// Goal/project/knowledge [F184-03], decision [F205-01], concept and task
	// route through the same atomic proposal.AcceptOrchestration flow
	// acceptGoalOrProject uses (ReadPending → PrepareOutOfBand → BeginTx →
	// Materialize → ResolveAccepted → Commit). AcceptOrchestration performs its
	// OWN resolve inside the transaction, so this branch returns directly
	// instead of falling through to the tail h.proposal.Resolve call — calling
	// Resolve a second time on an already-accepted proposal would report a
	// spurious "not found or already resolved". The materialised item (e.g.
	// *db.KnowledgeItem, *db.Decision, *db.Concept, *db.Task) is intentionally
	// discarded here (`_`) — unlike the singular acceptGoalOrProject path,
	// batchConfirmResultEntry has no per-type item field. The one thing a TypeTask
	// success does surface is its warn-mode warnings (additive field; the front
	// end does not read it and no MCP tool wraps this endpoint), taken from the
	// pre-fetch gate's meta.
	if status == proposal.StatusAccepted && hasMeta && m.isGoalOrProject {
		if m.payloadErr != "" {
			entry.Error = m.payloadErr
			return entry
		}
		adapter := h.goalProjectAdapter(id)
		if _, _, err := proposal.AcceptOrchestration(ctx, id, adapter); err != nil {
			if errors.Is(err, proposal.ErrNotFound) || errors.Is(err, proposal.ErrAlreadyResolved) {
				entry.Skipped = true
				entry.Error = "not found or already resolved"
				return entry
			}
			if errors.Is(err, gtd.ErrConflict) {
				entry.Error = "project name already exists"
				return entry
			}
			c.Logger().Errorf("ConfirmBatch accept via seam %s: %v", id, err)
			entry.Error = errInternalGeneric
			return entry
		}
		entry.OK = true
		entry.Warnings = m.warnings
		return entry
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
		meta = h.batchProposalMeta(c, ctx, ids)
	}

	results := make([]batchConfirmResultEntry, 0, len(ids))
	for _, id := range ids {
		results = append(results, h.batchResolveOne(c, ctx, id, status, meta))
	}
	return c.JSON(http.StatusOK, batchConfirmResponse{Results: results})
}
