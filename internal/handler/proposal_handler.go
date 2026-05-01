package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// proposalListStore covers the operations needed to list pending proposals.
type proposalListStore interface {
	ListPending(ctx context.Context) ([]db.PendingProposal, error)
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
func NewProposalHandler(p proposal.StoreIface, l learning.StoreIface) *ProposalHandler {
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
	case "reject":
		resolved, err := h.proposal.Resolve(ctx, id, proposal.StatusRejected)
		if errors.Is(err, proposal.ErrNotFound) {
			return c.JSON(http.StatusConflict, errResp("proposal not found or already resolved"))
		}
		if err != nil {
			c.Logger().Errorf("ConfirmProposal reject %s: %v", id, err)
			return c.JSON(http.StatusInternalServerError, errResp("internal server error"))
		}
		return c.JSON(http.StatusOK, confirmResponse{Proposal: toResponse(*resolved)})

	case "accept":
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
