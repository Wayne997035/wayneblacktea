package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

const (
	proposalBodyLimit = 32 * 1024 // 32 KB — enough for 100 UUIDs and action
	actionAccept      = "accept"
	actionReject      = "reject"
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

// ProposalHandler exposes GET /api/proposals/pending and
// POST /api/proposals/:id/confirm.
type ProposalHandler struct {
	proposal proposalListStore
	learning proposalConceptStore
}

// NewProposalHandler creates a ProposalHandler.
func NewProposalHandler(p proposalListStore, l proposalConceptStore) *ProposalHandler {
	return &ProposalHandler{proposal: p, learning: l}
}

// pendingProposalResponse is the JSON shape returned to the frontend.
// payload is decoded from []byte to avoid double-encoding.
type pendingProposalResponse struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Status     string          `json:"status"`
	Payload    json.RawMessage `json:"payload"`
	ProposedBy *string         `json:"proposed_by"`
	CreatedAt  string          `json:"created_at"`
	ResolvedAt *string         `json:"resolved_at,omitempty"`
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
	"all":      true,
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
	}

	rows, err := h.proposal.ListAll(c.Request().Context(), proposalType, 200)
	if err != nil {
		c.Logger().Errorf("ListProposals: %v", err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	out := make([]pendingProposalResponse, 0, len(rows))
	for _, p := range rows {
		if status == "all" || p.Status == status {
			out = append(out, toResponse(p))
		}
	}
	return c.JSON(http.StatusOK, out)
}

type confirmRequest struct {
	Action string `json:"action"`
}

type confirmResponse struct {
	Proposal pendingProposalResponse `json:"proposal"`
	Concept  *db.Concept             `json:"concept,omitempty"`
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

// handleAccept executes the accept flow with optimistic-lock ordering:
// Get → status guard → Resolve (atomic, WHERE status='pending') → CreateConcept.
// Concurrent accepts on the same proposal see a 409 from Resolve before any
// concept is materialised.
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

	var cp conceptCandidatePayload
	isConcept := proposal.Type(prop.Type) == proposal.TypeConcept
	if isConcept {
		var errMsg string
		cp, errMsg = decodeConceptCandidatePayload(prop.Payload)
		if errMsg != "" {
			return c.JSON(http.StatusBadRequest, errResp(errMsg))
		}
	}

	resolved, err := h.proposal.Resolve(ctx, id, proposal.StatusAccepted)
	if errors.Is(err, proposal.ErrNotFound) {
		return c.JSON(http.StatusConflict, errResp("proposal already resolved"))
	}
	if err != nil {
		c.Logger().Errorf("ConfirmProposal resolve %s: %v", id, err)
		return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
	}

	var concept *db.Concept
	if isConcept {
		concept, err = h.learning.CreateConcept(ctx, cp.Title, cp.Content, cp.Tags)
		if err != nil {
			c.Logger().Errorf("ConfirmProposal materialise concept %s: %v", id, err)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
	}
	return c.JSON(http.StatusOK, confirmResponse{Proposal: toResponse(*resolved), Concept: concept})
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
}

// batchConfirmResponse is returned by ConfirmBatch.
type batchConfirmResponse struct {
	Results []batchConfirmResultEntry `json:"results"`
}

// proposalConceptMeta bundles pre-fetched concept payload for batch accept.
type proposalConceptMeta struct {
	isConcept bool
	cp        conceptCandidatePayload
}

// batchConceptMeta pre-fetches concept payloads for the given IDs.
// Missing or malformed proposals are silently skipped — the resolve loop surfaces them.
func (h *ProposalHandler) batchConceptMeta(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]proposalConceptMeta {
	meta := make(map[uuid.UUID]proposalConceptMeta, len(ids))
	for _, id := range ids {
		prop, err := h.proposal.Get(ctx, id)
		if err != nil || prop == nil {
			continue
		}
		if proposal.Type(prop.Type) != proposal.TypeConcept {
			meta[id] = proposalConceptMeta{}
			continue
		}
		cp, errMsg := decodeConceptCandidatePayload(prop.Payload)
		if errMsg != "" {
			continue
		}
		meta[id] = proposalConceptMeta{isConcept: true, cp: cp}
	}
	return meta
}

// batchResolveOne resolves a single proposal and materialises a concept if applicable.
// Concept materialisation failures are non-fatal.
func (h *ProposalHandler) batchResolveOne(
	c echo.Context,
	ctx context.Context,
	id uuid.UUID,
	status proposal.Status,
	meta map[uuid.UUID]proposalConceptMeta,
) batchConfirmResultEntry {
	entry := batchConfirmResultEntry{ID: id.String()}
	if _, err := h.proposal.Resolve(ctx, id, status); err != nil {
		if errors.Is(err, proposal.ErrNotFound) {
			entry.Skipped = true
			entry.Error = "not found or already resolved"
		} else {
			c.Logger().Errorf("ConfirmBatch resolve %s: %v", id, err)
			entry.Error = "internal error"
		}
		return entry
	}
	entry.OK = true
	if status == proposal.StatusAccepted {
		if m, ok := meta[id]; ok && m.isConcept {
			if _, cerr := h.learning.CreateConcept(ctx, m.cp.Title, m.cp.Content, m.cp.Tags); cerr != nil {
				c.Logger().Errorf("ConfirmBatch materialise concept %s: %v", id, cerr)
			}
		}
	}
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

	var meta map[uuid.UUID]proposalConceptMeta
	if req.Action == actionAccept {
		meta = h.batchConceptMeta(ctx, ids)
	}

	results := make([]batchConfirmResultEntry, 0, len(ids))
	for _, id := range ids {
		results = append(results, h.batchResolveOne(c, ctx, id, status, meta))
	}
	return c.JSON(http.StatusOK, batchConfirmResponse{Results: results})
}
